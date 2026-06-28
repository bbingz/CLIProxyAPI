package management

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type oauthCallbackRequest struct {
	Provider    string `json:"provider"`
	RedirectURL string `json:"redirect_url"`
	Code        string `json:"code"`
	State       string `json:"state"`
	Error       string `json:"error"`
}

type pluginOAuthCallbackRequest map[string]any

func (h *Handler) PostOAuthCallback(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "handler not initialized"})
		return
	}

	var req oauthCallbackRequest
	if errBindJSON := c.ShouldBindJSON(&req); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid body"})
		return
	}
	h.handleOAuthCallback(c, req)
}

func (h *Handler) PostPluginOAuthCallback(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "handler not initialized"})
		return
	}

	var req pluginOAuthCallbackRequest
	if errBindJSON := c.ShouldBindJSON(&req); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid body"})
		return
	}
	state := firstNonEmpty(c.Query("state"), stringFromAny(req["state"]))
	provider := firstNonEmpty(c.Query("provider"), stringFromAny(req["provider"]))
	h.handlePluginOAuthCallback(c, state, provider, map[string]any(req))
}

func (h *Handler) OptionsPluginOAuthCallback(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	state := strings.TrimSpace(c.Query("state"))
	provider := strings.TrimSpace(c.Query("provider"))
	if ok := h.preparePluginOAuthCallback(c, state, provider); !ok {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) GetOAuthCallback(c *gin.Context) {
	req := oauthCallbackRequest{
		Provider: strings.TrimSpace(c.Query("provider")),
		Code:     strings.TrimSpace(c.Query("code")),
		State:    strings.TrimSpace(c.Query("state")),
		Error:    firstNonEmpty(c.Query("error"), c.Query("error_description")),
	}
	h.handleOAuthCallback(c, req)
}

func (h *Handler) handlePluginOAuthCallback(c *gin.Context, state, provider string, metadata map[string]any) {
	if ok := h.preparePluginOAuthCallback(c, state, provider); !ok {
		return
	}
	cleanMetadata := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cleanMetadata[key] = value
	}
	cleanMetadata["callback_received_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if len(cleanMetadata) == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "callback payload is required"})
		return
	}
	canonicalProvider := strings.TrimSpace(provider)
	if canonicalProvider == "" {
		canonicalProvider, _, _, _, _ = GetOAuthSessionDetails(state)
	}
	if canonicalProvider == "" {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "unknown or expired state"})
		return
	}
	if errMerge := MergePluginOAuthSessionMetadata(state, canonicalProvider, cleanMetadata); errMerge != nil {
		if errors.Is(errMerge, errOAuthSessionNotPending) {
			c.JSON(http.StatusConflict, gin.H{"status": "error", "error": "oauth flow is not pending"})
			return
		}
		if errors.Is(errMerge, errUnsupportedOAuthFlow) {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "provider does not match state"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid state"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) preparePluginOAuthCallback(c *gin.Context, state, provider string) bool {
	state = strings.TrimSpace(state)
	provider = strings.TrimSpace(provider)
	if state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "state is required"})
		return false
	}
	if err := ValidateOAuthState(state); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid state"})
		return false
	}

	sessionProvider, sessionStatus, isPlugin, metadata, ok := GetOAuthSessionDetails(state)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "unknown or expired state"})
		return false
	}
	if !isPlugin {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "state is not a plugin oauth session"})
		return false
	}
	if sessionStatus != "" {
		c.JSON(http.StatusConflict, gin.H{"status": "error", "error": sessionStatus})
		return false
	}
	if provider != "" {
		canonicalProvider, errNormalize := NormalizePluginOAuthCallbackProvider(provider)
		if errNormalize != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "unsupported provider"})
			return false
		}
		if !strings.EqualFold(sessionProvider, canonicalProvider) {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "provider does not match state"})
			return false
		}
	}
	if !allowPluginOAuthOrigin(c, metadata) {
		c.JSON(http.StatusForbidden, gin.H{"status": "error", "error": "origin is not allowed"})
		return false
	}
	return true
}

func allowPluginOAuthOrigin(c *gin.Context, metadata map[string]any) bool {
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin == "" {
		return true
	}
	for _, allowed := range pluginOAuthAllowedOrigins(metadata) {
		if origin == allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Header("Access-Control-Max-Age", "600")
			c.Header("Vary", "Origin")
			return true
		}
	}
	return false
}

func pluginOAuthAllowedOrigins(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["allowed_origins"]
	if !ok {
		raw = metadata["allowedOrigins"]
	}
	switch values := raw.(type) {
	case []string:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if trimmed := strings.TrimSpace(stringFromAny(value)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case string:
		if trimmed := strings.TrimSpace(values); trimmed != "" {
			return []string{trimmed}
		}
	}
	return nil
}

func (h *Handler) handleOAuthCallback(c *gin.Context, req oauthCallbackRequest) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "handler not initialized"})
		return
	}

	state := strings.TrimSpace(req.State)
	code := strings.TrimSpace(req.Code)
	errMsg := strings.TrimSpace(req.Error)

	if rawRedirect := strings.TrimSpace(req.RedirectURL); rawRedirect != "" {
		u, errParse := url.Parse(rawRedirect)
		if errParse != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid redirect_url"})
			return
		}
		q := u.Query()
		if state == "" {
			state = strings.TrimSpace(q.Get("state"))
		}
		if code == "" {
			code = strings.TrimSpace(q.Get("code"))
		}
		if errMsg == "" {
			errMsg = strings.TrimSpace(q.Get("error"))
			if errMsg == "" {
				errMsg = strings.TrimSpace(q.Get("error_description"))
			}
		}
	}

	if state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "state is required"})
		return
	}
	if err := ValidateOAuthState(state); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid state"})
		return
	}
	if code == "" && errMsg == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "code or error is required"})
		return
	}

	sessionProvider, sessionStatus, isPlugin, _, ok := GetOAuthSessionDetails(state)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "unknown or expired state"})
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = sessionProvider
	}
	var canonicalProvider string
	var errNormalize error
	if isPlugin {
		canonicalProvider, errNormalize = NormalizePluginOAuthCallbackProvider(provider)
	} else {
		canonicalProvider, errNormalize = NormalizeOAuthCallbackProvider(provider)
	}
	if errNormalize != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "unsupported provider"})
		return
	}
	if sessionStatus != "" {
		c.JSON(http.StatusConflict, gin.H{"status": "error", "error": sessionStatus})
		return
	}
	if !strings.EqualFold(sessionProvider, canonicalProvider) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "provider does not match state"})
		return
	}

	if _, errWrite := WriteOAuthCallbackFileForPendingSession(h.cfg.AuthDir, canonicalProvider, state, code, errMsg); errWrite != nil {
		if errors.Is(errWrite, errOAuthSessionNotPending) {
			_, status, okSession := GetOAuthSession(state)
			if okSession && status != "" {
				c.JSON(http.StatusConflict, gin.H{"status": "error", "error": status})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"status": "error", "error": "oauth flow is not pending"})
			return
		}
		log.WithError(errWrite).Error("failed to persist oauth callback")
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "failed to persist oauth callback"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}
