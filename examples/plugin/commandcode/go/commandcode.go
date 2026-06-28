package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	providerID                    = "commandcode"
	defaultCommandCodeAPIBaseURL  = "https://api.commandcode.ai"
	defaultCommandCodeStudioURL   = "https://commandcode.ai/studio/auth/cli"
	defaultCommandCodeCLIVersion  = "0.40.11"
	defaultCommandCodeEnvironment = "production"
)

var commandCodeAllowedOrigins = []string{
	"https://commandcode.ai",
	"https://staging.commandcode.ai",
	"http://localhost:3000",
}

type commandCodeConfig struct {
	APIBaseURL  string
	StudioURL   string
	CLIVersion  string
	Environment string
}

func (c commandCodeConfig) withDefaults() commandCodeConfig {
	if strings.TrimSpace(c.APIBaseURL) == "" {
		c.APIBaseURL = defaultCommandCodeAPIBaseURL
	}
	if strings.TrimSpace(c.StudioURL) == "" {
		c.StudioURL = defaultCommandCodeStudioURL
	}
	if strings.TrimSpace(c.CLIVersion) == "" {
		c.CLIVersion = defaultCommandCodeCLIVersion
	}
	if strings.TrimSpace(c.Environment) == "" {
		c.Environment = defaultCommandCodeEnvironment
	}
	c.APIBaseURL = strings.TrimRight(strings.TrimSpace(c.APIBaseURL), "/")
	c.StudioURL = strings.TrimSpace(c.StudioURL)
	c.CLIVersion = strings.TrimSpace(c.CLIVersion)
	c.Environment = strings.TrimSpace(c.Environment)
	return c
}

type commandCodeAuthStorage struct {
	Type            string `json:"type,omitempty"`
	APIKey          string `json:"apiKey,omitempty"`
	UserID          string `json:"userId,omitempty"`
	UserName        string `json:"userName,omitempty"`
	KeyName         string `json:"keyName,omitempty"`
	AuthenticatedAt string `json:"authenticatedAt,omitempty"`
	CLIVersion      string `json:"cliVersion,omitempty"`
	Environment     string `json:"environment,omitempty"`
}

func startCommandCodeLogin(req pluginapi.AuthLoginStartRequest, stateFn func() (string, error), now time.Time) (pluginapi.AuthLoginStartResponse, error) {
	if stateFn == nil {
		stateFn = randomOAuthState
	}
	state, errState := stateFn()
	if errState != nil {
		return pluginapi.AuthLoginStartResponse{}, errState
	}
	state = strings.TrimSpace(state)
	if state == "" {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("empty oauth state")
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("callback base url is required")
	}
	callbackURL, errParseCallback := url.Parse(baseURL)
	if errParseCallback != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("parse callback url: %w", errParseCallback)
	}
	callbackQuery := callbackURL.Query()
	callbackQuery.Set("state", state)
	callbackQuery.Set("provider", providerID)
	callbackURL.RawQuery = callbackQuery.Encode()

	loginURL, errParseLogin := url.Parse(defaultCommandCodeStudioURL)
	if errParseLogin != nil {
		return pluginapi.AuthLoginStartResponse{}, errParseLogin
	}
	loginQuery := loginURL.Query()
	loginQuery.Set("callback", callbackURL.String())
	loginQuery.Set("redirect_uri", callbackURL.String())
	loginQuery.Set("state", state)
	loginURL.RawQuery = loginQuery.Encode()

	return pluginapi.AuthLoginStartResponse{
		Provider:  providerID,
		URL:       loginURL.String(),
		State:     state,
		ExpiresAt: now.Add(10 * time.Minute).UTC(),
		Metadata: map[string]any{
			"login_kind":      "commandcode-studio",
			"callback_url":    callbackURL.String(),
			"allowed_origins": append([]string(nil), commandCodeAllowedOrigins...),
			"started_at":      now.UTC().Format(time.RFC3339Nano),
		},
	}, nil
}

func pollCommandCodeLogin(req pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	if message := firstMetadataString(req.Metadata, "error", "error_description"); message != "" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: message}, nil
	}
	apiKey := firstMetadataString(req.Metadata, "apiKey", "api_key", "token")
	if apiKey == "" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}, nil
	}
	auth, errAuth := commandCodeAuthData(commandCodeAuthStorage{
		Type:            providerID,
		APIKey:          apiKey,
		UserID:          firstMetadataString(req.Metadata, "userId", "user_id"),
		UserName:        firstMetadataString(req.Metadata, "userName", "user_name", "name"),
		KeyName:         firstMetadataString(req.Metadata, "keyName", "key_name"),
		AuthenticatedAt: firstMetadataString(req.Metadata, "authenticatedAt", "authenticated_at", "callback_received_at"),
		CLIVersion:      defaultCommandCodeCLIVersion,
		Environment:     defaultCommandCodeEnvironment,
	})
	if errAuth != nil {
		return pluginapi.AuthLoginPollResponse{}, errAuth
	}
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Auth: auth}, nil
}

func parseCommandCodeAuth(raw []byte) (pluginapi.AuthParseResponse, error) {
	var storage commandCodeAuthStorage
	if errUnmarshal := json.Unmarshal(raw, &storage); errUnmarshal != nil {
		return pluginapi.AuthParseResponse{}, nil
	}
	if storage.APIKey == "" {
		var generic map[string]any
		if errUnmarshal := json.Unmarshal(raw, &generic); errUnmarshal == nil {
			storage.APIKey = firstMetadataString(generic, "api_key", "token")
			storage.UserID = firstNonEmptyString(storage.UserID, firstMetadataString(generic, "user_id"))
			storage.UserName = firstNonEmptyString(storage.UserName, firstMetadataString(generic, "user_name", "name"))
			storage.AuthenticatedAt = firstNonEmptyString(storage.AuthenticatedAt, firstMetadataString(generic, "authenticated_at"))
		}
	}
	if storage.APIKey == "" {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	if strings.TrimSpace(storage.Type) != "" && !strings.EqualFold(storage.Type, providerID) {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	if strings.TrimSpace(storage.Type) == "" && !strings.HasPrefix(storage.APIKey, "user_") {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	auth, errAuth := commandCodeAuthData(storage)
	if errAuth != nil {
		return pluginapi.AuthParseResponse{}, errAuth
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: auth}, nil
}

func commandCodeAuthData(storage commandCodeAuthStorage) (pluginapi.AuthData, error) {
	storage.APIKey = strings.TrimSpace(storage.APIKey)
	if storage.APIKey == "" {
		return pluginapi.AuthData{}, fmt.Errorf("commandcode api key is required")
	}
	if strings.TrimSpace(storage.Type) == "" {
		storage.Type = providerID
	}
	if strings.TrimSpace(storage.CLIVersion) == "" {
		storage.CLIVersion = defaultCommandCodeCLIVersion
	}
	if strings.TrimSpace(storage.Environment) == "" {
		storage.Environment = defaultCommandCodeEnvironment
	}
	if strings.TrimSpace(storage.AuthenticatedAt) == "" {
		storage.AuthenticatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	raw, errMarshal := json.Marshal(storage)
	if errMarshal != nil {
		return pluginapi.AuthData{}, errMarshal
	}
	suffix := shortSecretHash(storage.APIKey)
	label := "Command Code"
	if strings.TrimSpace(storage.UserName) != "" {
		label += " (" + strings.TrimSpace(storage.UserName) + ")"
	}
	return pluginapi.AuthData{
		Provider:    providerID,
		ID:          providerID + "-" + suffix,
		FileName:    providerID + "-" + suffix + ".json",
		Label:       label,
		StorageJSON: raw,
		Metadata: map[string]any{
			"type":             providerID,
			"auth_kind":        "oauth",
			"user_id":          strings.TrimSpace(storage.UserID),
			"user_name":        strings.TrimSpace(storage.UserName),
			"key_name":         strings.TrimSpace(storage.KeyName),
			"authenticated_at": strings.TrimSpace(storage.AuthenticatedAt),
		},
		Attributes: map[string]string{
			"auth_kind":   "oauth",
			"environment": storage.Environment,
		},
	}, nil
}

func buildCommandCodeHTTPRequest(exec pluginapi.ExecutorRequest, cfg commandCodeConfig, sessionID, today string) (pluginapi.HTTPRequest, error) {
	cfg = cfg.withDefaults()
	auth, errAuth := commandCodeAuthFromStorage(exec.StorageJSON)
	if errAuth != nil {
		return pluginapi.HTTPRequest{}, errAuth
	}
	var chat chatCompletionRequest
	if errUnmarshal := json.Unmarshal(exec.Payload, &chat); errUnmarshal != nil {
		return pluginapi.HTTPRequest{}, fmt.Errorf("decode chat completions payload: %w", errUnmarshal)
	}
	model := normalizeCommandCodeModel(firstNonEmptyString(exec.Model, chat.Model))
	if model == "" {
		return pluginapi.HTTPRequest{}, fmt.Errorf("model is required")
	}
	if today == "" {
		today = time.Now().UTC().Format(time.DateOnly)
	}
	if sessionID == "" {
		sessionID, _ = randomOAuthState()
	}
	envelope, errEnvelope := buildCommandCodeEnvelope(chat, model, cfg.Environment, today)
	if errEnvelope != nil {
		return pluginapi.HTTPRequest{}, errEnvelope
	}
	body, errMarshal := json.Marshal(envelope)
	if errMarshal != nil {
		return pluginapi.HTTPRequest{}, errMarshal
	}
	return pluginapi.HTTPRequest{
		Method: http.MethodPost,
		URL:    cfg.APIBaseURL + "/alpha/generate",
		Headers: http.Header{
			"Authorization":          []string{"Bearer " + auth.APIKey},
			"Content-Type":           []string{"application/json"},
			"Accept":                 []string{"application/x-ndjson"},
			"X-Cli-Environment":      []string{cfg.Environment},
			"X-Command-Code-Version": []string{cfg.CLIVersion},
			"X-Session-Id":           []string{sessionID},
		},
		Body: body,
	}, nil
}

type chatCompletionRequest struct {
	Model               string              `json:"model"`
	Messages            []openAIChatMessage `json:"messages"`
	Tools               []openAITool        `json:"tools,omitempty"`
	MaxTokens           *int                `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                `json:"max_completion_tokens,omitempty"`
	Temperature         *float64            `json:"temperature,omitempty"`
	Stream              bool                `json:"stream,omitempty"`
}

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type commandCodeEnvelope struct {
	Config         commandCodeSidecar `json:"config"`
	Memory         string             `json:"memory"`
	Taste          any                `json:"taste"`
	Skills         any                `json:"skills"`
	PermissionMode string             `json:"permissionMode"`
	Params         commandCodeParams  `json:"params"`
}

type commandCodeSidecar struct {
	WorkingDir    string   `json:"workingDir"`
	Date          string   `json:"date"`
	Environment   string   `json:"environment"`
	Structure     []any    `json:"structure"`
	IsGitRepo     bool     `json:"isGitRepo"`
	CurrentBranch string   `json:"currentBranch"`
	MainBranch    string   `json:"mainBranch"`
	GitStatus     string   `json:"gitStatus"`
	RecentCommits []string `json:"recentCommits"`
}

type commandCodeParams struct {
	Model       string               `json:"model"`
	System      string               `json:"system"`
	Messages    []commandCodeMessage `json:"messages"`
	Tools       []commandCodeTool    `json:"tools,omitempty"`
	MaxTokens   int                  `json:"max_tokens"`
	Temperature *float64             `json:"temperature,omitempty"`
	Stream      bool                 `json:"stream"`
}

type commandCodeMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type commandCodeTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

func buildCommandCodeEnvelope(chat chatCompletionRequest, model, environment, today string) (commandCodeEnvelope, error) {
	system, messages, errMessages := convertOpenAIChatMessages(chat.Messages)
	if errMessages != nil {
		return commandCodeEnvelope{}, errMessages
	}
	maxTokens := 8192
	if chat.MaxCompletionTokens != nil && *chat.MaxCompletionTokens > 0 {
		maxTokens = *chat.MaxCompletionTokens
	} else if chat.MaxTokens != nil && *chat.MaxTokens > 0 {
		maxTokens = *chat.MaxTokens
	}
	workingDir, errWd := os.Getwd()
	if errWd != nil || strings.TrimSpace(workingDir) == "" {
		workingDir = "."
	}
	return commandCodeEnvelope{
		Config: commandCodeSidecar{
			WorkingDir:    workingDir,
			Date:          today,
			Environment:   environment,
			Structure:     []any{},
			IsGitRepo:     false,
			CurrentBranch: "",
			MainBranch:    "",
			GitStatus:     "",
			RecentCommits: []string{},
		},
		Memory:         "",
		Taste:          nil,
		Skills:         nil,
		PermissionMode: "standard",
		Params: commandCodeParams{
			Model:       model,
			System:      system,
			Messages:    messages,
			Tools:       convertOpenAITools(chat.Tools),
			MaxTokens:   maxTokens,
			Temperature: chat.Temperature,
			Stream:      true,
		},
	}, nil
}

func convertOpenAIChatMessages(messages []openAIChatMessage) (string, []commandCodeMessage, error) {
	out := make([]commandCodeMessage, 0, len(messages))
	var systemParts []string
	toolNames := make(map[string]string)
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system", "developer":
			if text := openAIContentText(msg.Content); text != "" {
				systemParts = append(systemParts, text)
			}
		case "user":
			content := openAIUserContent(msg.Content)
			if len(content) > 0 {
				out = append(out, commandCodeMessage{Role: "user", Content: content})
			}
		case "assistant":
			content := make([]any, 0, 1+len(msg.ToolCalls))
			if text := openAIContentText(msg.Content); text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			for _, call := range msg.ToolCalls {
				name := strings.TrimSpace(call.Function.Name)
				if call.ID != "" && name != "" {
					toolNames[call.ID] = name
				}
				content = append(content, map[string]any{
					"type":       "tool-call",
					"toolCallId": strings.TrimSpace(call.ID),
					"toolName":   name,
					"input":      parseToolArguments(call.Function.Arguments),
				})
			}
			if len(content) > 0 {
				out = append(out, commandCodeMessage{Role: "assistant", Content: content})
			}
		case "tool":
			toolCallID := strings.TrimSpace(msg.ToolCallID)
			name := firstNonEmptyString(strings.TrimSpace(msg.Name), toolNames[toolCallID], "unknown")
			block := map[string]any{
				"type":       "tool-result",
				"toolCallId": toolCallID,
				"toolName":   name,
				"output": map[string]any{
					"type":  "text",
					"value": openAIContentText(msg.Content),
				},
			}
			lastIndex := len(out) - 1
			if lastIndex >= 0 && out[lastIndex].Role == "tool" {
				out[lastIndex].Content = append(out[lastIndex].Content, block)
			} else {
				out = append(out, commandCodeMessage{Role: "tool", Content: []any{block}})
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), out, nil
}

func openAIUserContent(content any) []any {
	if text, ok := content.(string); ok {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": text}}
	}
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		item, ok := block.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "text":
			if text := stringFromAny(item["text"]); text != "" {
				out = append(out, map[string]any{"type": "text", "text": text})
			}
		case "image_url":
			imageURL, _ := item["image_url"].(map[string]any)
			if rawURL := stringFromAny(imageURL["url"]); rawURL != "" {
				out = append(out, map[string]any{"type": "image", "image": rawURL})
			}
		}
	}
	return out
}

func openAIContentText(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, block := range typed {
			item, ok := block.(map[string]any)
			if !ok || item["type"] != "text" {
				continue
			}
			if text := stringFromAny(item["text"]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func convertOpenAITools(tools []openAITool) []commandCodeTool {
	out := make([]commandCodeTool, 0, len(tools))
	for _, tool := range tools {
		if !strings.EqualFold(strings.TrimSpace(tool.Type), "function") {
			continue
		}
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			continue
		}
		out = append(out, commandCodeTool{
			Name:        name,
			Description: strings.TrimSpace(tool.Function.Description),
			InputSchema: tool.Function.Parameters,
		})
	}
	return out
}

func parseToolArguments(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var out map[string]any
	if errUnmarshal := json.Unmarshal([]byte(raw), &out); errUnmarshal != nil {
		return map[string]any{"_raw": raw}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func commandCodeNDJSONToChatCompletion(raw []byte, model string, created int64) ([]byte, error) {
	agg := newCommandCodeAggregate(model, created)
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if errApply := agg.ApplyLine(line); errApply != nil {
			return nil, errApply
		}
	}
	return agg.ChatCompletion()
}

type commandCodeAggregate struct {
	Model        string
	Created      int64
	Content      strings.Builder
	Reasoning    strings.Builder
	FinishReason string
	Usage        commandCodeUsage
	ToolCalls    []openAIResponseToolCall
	toolInputs   map[string]*toolInputState
}

type toolInputState struct {
	ID   string
	Name string
	JSON strings.Builder
}

type commandCodeUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	TotalTokens       int `json:"totalTokens"`
	CachedInputTokens int `json:"cachedInputTokens"`
}

type openAIResponseToolCall struct {
	ID       string                     `json:"id"`
	Type     string                     `json:"type"`
	Function openAIResponseToolFunction `json:"function"`
}

type openAIResponseToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func newCommandCodeAggregate(model string, created int64) *commandCodeAggregate {
	if created == 0 {
		created = time.Now().Unix()
	}
	return &commandCodeAggregate{
		Model:      normalizeCommandCodeModel(model),
		Created:    created,
		toolInputs: make(map[string]*toolInputState),
	}
}

func (a *commandCodeAggregate) ApplyLine(line []byte) error {
	var event map[string]any
	if errUnmarshal := json.Unmarshal(line, &event); errUnmarshal != nil {
		return fmt.Errorf("decode commandcode ndjson: %w", errUnmarshal)
	}
	switch stringFromAny(event["type"]) {
	case "reasoning-delta":
		a.Reasoning.WriteString(rawStringFromAny(event["text"]))
	case "text-delta":
		a.Content.WriteString(rawStringFromAny(event["text"]))
	case "tool-input-start":
		id := firstNonEmptyString(stringFromAny(event["id"]), stringFromAny(event["toolCallId"]))
		if id != "" {
			a.toolInputs[id] = &toolInputState{ID: id, Name: stringFromAny(event["toolName"])}
		}
	case "tool-input-delta":
		id := firstNonEmptyString(stringFromAny(event["id"]), stringFromAny(event["toolCallId"]))
		if state := a.toolInputs[id]; state != nil {
			state.JSON.WriteString(rawStringFromAny(event["delta"]))
		}
	case "tool-input-end":
		id := firstNonEmptyString(stringFromAny(event["id"]), stringFromAny(event["toolCallId"]))
		if state := a.toolInputs[id]; state != nil {
			a.appendToolCall(state.ID, state.Name, state.JSON.String())
			delete(a.toolInputs, id)
		}
	case "tool-call":
		id := firstNonEmptyString(stringFromAny(event["toolCallId"]), stringFromAny(event["id"]))
		name := stringFromAny(event["toolName"])
		args := "{}"
		if input, ok := event["input"]; ok {
			if raw, errMarshal := json.Marshal(input); errMarshal == nil {
				args = string(raw)
			}
		}
		a.appendToolCall(id, name, args)
	case "finish-step", "finish":
		a.FinishReason = mapFinishReason(stringFromAny(event["finishReason"]))
		if usage, ok := event["usage"].(map[string]any); ok {
			a.Usage = parseCommandCodeUsage(usage)
		}
	case "error":
		return fmt.Errorf("commandcode upstream error: %s", firstNonEmptyString(stringFromAny(event["message"]), stringFromAny(event["error"])))
	}
	return nil
}

func (a *commandCodeAggregate) appendToolCall(id, name, arguments string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	for _, existing := range a.ToolCalls {
		if existing.ID == id {
			return
		}
	}
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	a.ToolCalls = append(a.ToolCalls, openAIResponseToolCall{
		ID:   id,
		Type: "function",
		Function: openAIResponseToolFunction{
			Name:      strings.TrimSpace(name),
			Arguments: arguments,
		},
	})
}

func (a *commandCodeAggregate) ChatCompletion() ([]byte, error) {
	finish := a.FinishReason
	if finish == "" {
		finish = "stop"
	}
	message := map[string]any{
		"role":    "assistant",
		"content": a.Content.String(),
	}
	if reasoning := a.Reasoning.String(); reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(a.ToolCalls) > 0 {
		message["tool_calls"] = a.ToolCalls
	}
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-commandcode",
		"object":  "chat.completion",
		"created": a.Created,
		"model":   a.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": openAIUsageMap(a.Usage),
	})
}

func commandCodeEventToOpenAIStreamChunks(line []byte, model string, created int64) ([][]byte, error) {
	return newCommandCodeStreamConverter(model, created).ConvertLine(line)
}

type commandCodeStreamConverter struct {
	model         string
	created       int64
	pendingUsage  *commandCodeUsage
	finishEmitted bool
	usageEmitted  bool
}

func newCommandCodeStreamConverter(model string, created int64) *commandCodeStreamConverter {
	if created == 0 {
		created = time.Now().Unix()
	}
	return &commandCodeStreamConverter{
		model:   normalizeCommandCodeModel(model),
		created: created,
	}
}

func (c *commandCodeStreamConverter) ConvertLine(line []byte) ([][]byte, error) {
	var event map[string]any
	if errUnmarshal := json.Unmarshal(bytes.TrimSpace(line), &event); errUnmarshal != nil {
		return nil, fmt.Errorf("decode commandcode ndjson: %w", errUnmarshal)
	}
	switch stringFromAny(event["type"]) {
	case "reasoning-delta":
		text := rawStringFromAny(event["text"])
		if text == "" {
			return nil, nil
		}
		return [][]byte{chatCompletionStreamChunk(c.model, c.created, map[string]any{"reasoning_content": text}, "")}, nil
	case "text-delta":
		text := rawStringFromAny(event["text"])
		if text == "" {
			return nil, nil
		}
		return [][]byte{chatCompletionStreamChunk(c.model, c.created, map[string]any{"content": text}, "")}, nil
	case "finish-step":
		if usage, ok := commandCodeUsageFromEvent(event); ok {
			c.pendingUsage = &usage
		}
		return nil, nil
	case "finish":
		finish := mapFinishReason(stringFromAny(event["finishReason"]))
		if finish == "" {
			finish = "stop"
		}
		usage, hasUsage := commandCodeUsageFromEvent(event)
		if !hasUsage && c.pendingUsage != nil {
			usage = *c.pendingUsage
			hasUsage = true
		}
		return c.finishChunks(finish, usage, hasUsage), nil
	case "error":
		return nil, fmt.Errorf("commandcode upstream error: %s", firstNonEmptyString(stringFromAny(event["message"]), stringFromAny(event["error"])))
	default:
		return nil, nil
	}
}

func (c *commandCodeStreamConverter) FlushFinal() [][]byte {
	if c.finishEmitted || c.pendingUsage == nil {
		return nil
	}
	return c.finishChunks("stop", *c.pendingUsage, true)
}

func (c *commandCodeStreamConverter) finishChunks(finish string, usage commandCodeUsage, hasUsage bool) [][]byte {
	if c.finishEmitted {
		return nil
	}
	c.finishEmitted = true
	chunks := [][]byte{chatCompletionStreamChunk(c.model, c.created, map[string]any{}, finish)}
	if hasUsage && !c.usageEmitted {
		c.usageEmitted = true
		chunks = append(chunks, chatCompletionUsageStreamChunk(c.model, c.created, usage))
	}
	return chunks
}

func chatCompletionStreamChunk(model string, created int64, delta map[string]any, finish string) []byte {
	choice := map[string]any{
		"index": 0,
		"delta": delta,
	}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	body, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-commandcode",
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   normalizeCommandCodeModel(model),
		"choices": []any{choice},
	})
	return body
}

func chatCompletionUsageStreamChunk(model string, created int64, usage commandCodeUsage) []byte {
	body, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-commandcode",
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   normalizeCommandCodeModel(model),
		"choices": []any{},
		"usage":   openAIUsageMap(usage),
	})
	return body
}

func commandCodeUsageFromEvent(event map[string]any) (commandCodeUsage, bool) {
	usageMap, ok := event["usage"].(map[string]any)
	if !ok {
		return commandCodeUsage{}, false
	}
	usage := parseCommandCodeUsage(usageMap)
	return usage, usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.TotalTokens != 0 || usage.CachedInputTokens != 0
}

func parseCommandCodeUsage(raw map[string]any) commandCodeUsage {
	input := intFromAny(raw["inputTokens"])
	cached := intFromAny(raw["cachedInputTokens"])
	output := intFromAny(raw["outputTokens"])
	total := input + output
	if total == 0 {
		total = intFromAny(raw["totalTokens"])
	}
	return commandCodeUsage{
		InputTokens:       input,
		OutputTokens:      output,
		TotalTokens:       total,
		CachedInputTokens: cached,
	}
}

func openAIUsageMap(usage commandCodeUsage) map[string]any {
	out := map[string]any{
		"prompt_tokens":     usage.InputTokens,
		"completion_tokens": usage.OutputTokens,
		"total_tokens":      usage.TotalTokens,
	}
	if usage.CachedInputTokens > 0 {
		out["prompt_tokens_details"] = map[string]int{"cached_tokens": usage.CachedInputTokens}
	}
	return out
}

func commandCodeAuthFromStorage(raw []byte) (commandCodeAuthStorage, error) {
	var storage commandCodeAuthStorage
	if errUnmarshal := json.Unmarshal(raw, &storage); errUnmarshal != nil {
		return commandCodeAuthStorage{}, fmt.Errorf("decode commandcode auth storage: %w", errUnmarshal)
	}
	if storage.APIKey == "" {
		var generic map[string]any
		if errUnmarshal := json.Unmarshal(raw, &generic); errUnmarshal == nil {
			storage.APIKey = firstMetadataString(generic, "api_key", "token")
		}
	}
	if strings.TrimSpace(storage.APIKey) == "" {
		return commandCodeAuthStorage{}, fmt.Errorf("commandcode api key is missing")
	}
	return storage, nil
}

type commandCodeModelDefinition struct {
	id      string
	display string
	aliases []string
	ctx     int64
	out     int64
}

func commandCodeModelDefinitions() []commandCodeModelDefinition {
	return []commandCodeModelDefinition{
		{"deepseek/deepseek-v4-pro", "DeepSeek V4 Pro (Command Code)", []string{"deepseek-v4-pro"}, 1_000_000, 131072},
		{"deepseek/deepseek-v4-flash", "DeepSeek V4 Flash (Command Code)", []string{"deepseek-v4-flash"}, 1_000_000, 131072},
		{"moonshotai/Kimi-K2.7-Code", "Kimi K2.7 Code (Command Code)", []string{"kimi-k2.7-code"}, 256000, 65536},
		{"moonshotai/Kimi-K2.7-Code-Highspeed", "Kimi K2.7 Code HighSpeed (Command Code)", []string{"kimi-k2.7-code-highspeed"}, 262000, 65536},
		{"moonshotai/Kimi-K2.6", "Kimi K2.6 (Command Code)", []string{"kimi-k2.6"}, 256000, 65536},
		{"moonshotai/Kimi-K2.5", "Kimi K2.5 (Command Code)", []string{"kimi-k2.5"}, 256000, 65536},
		{"zai-org/GLM-5.2", "GLM 5.2 (Command Code)", []string{"glm-5.2"}, 1_000_000, 131072},
		{"zai-org/GLM-5.1", "GLM 5.1 (Command Code)", []string{"glm-5.1"}, 1_000_000, 131072},
		{"MiniMaxAI/MiniMax-M3", "MiniMax M3 (Command Code)", []string{"minimax-m3"}, 1_000_000, 131072},
		{"Qwen/Qwen3.7-Max", "Qwen 3.7 Max (Command Code)", []string{"qwen3.7-max"}, 1_000_000, 131072},
		{"stepfun/Step-3.7-Flash", "Step 3.7 Flash (Command Code)", []string{"step-3.7-flash"}, 1_000_000, 131072},
		{"nvidia/nemotron-3-ultra-550b-a55b", "Nemotron 3 Ultra (Command Code)", []string{"nemotron-3-ultra-550b-a55b"}, 1_000_000, 131072},
	}
}

func defaultCommandCodeModels() []pluginapi.ModelInfo {
	defs := commandCodeModelDefinitions()
	models := make([]pluginapi.ModelInfo, 0, len(defs)*2)
	seen := make(map[string]struct{}, len(defs)*2)
	add := func(id, display string, ctx, out int64) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		models = append(models, commandCodeModelInfo(id, display, ctx, out))
	}
	for _, def := range defs {
		add(def.id, def.display, def.ctx, def.out)
		for _, alias := range def.aliases {
			add(alias, commandCodeAliasDisplay(def.display), def.ctx, def.out)
		}
	}
	return models
}

func commandCodeModelInfo(id, display string, ctx, out int64) pluginapi.ModelInfo {
	return pluginapi.ModelInfo{
		ID:                         id,
		Object:                     "model",
		OwnedBy:                    providerID,
		Type:                       "chat",
		DisplayName:                display,
		Name:                       id,
		Description:                "Command Code /alpha/generate model",
		InputTokenLimit:            ctx,
		OutputTokenLimit:           out,
		ContextLength:              ctx,
		MaxCompletionTokens:        out,
		SupportedGenerationMethods: []string{"chat"},
		SupportedInputModalities:   []string{"text"},
		SupportedOutputModalities:  []string{"text"},
		UserDefined:                true,
	}
}

func commandCodeAliasDisplay(display string) string {
	return strings.TrimSuffix(display, " (Command Code)") + " (Command Code Alias)"
}

func randomOAuthState() (string, error) {
	buf := make([]byte, 18)
	if _, errRead := rand.Read(buf); errRead != nil {
		return "", errRead
	}
	return hex.EncodeToString(buf), nil
}

func shortSecretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])[:12]
}

func firstMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringFromAny(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
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

func rawStringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		i, _ := strconv.Atoi(typed.String())
		return i
	default:
		return 0
	}
}

func normalizeCommandCodeModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, providerID+"/")
	for _, def := range commandCodeModelDefinitions() {
		if strings.EqualFold(model, def.id) {
			return def.id
		}
		for _, alias := range def.aliases {
			if strings.EqualFold(model, alias) {
				return def.id
			}
		}
	}
	return model
}

func mapFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "tool-calls", "tool_calls", "tool_use":
		return "tool_calls"
	case "length":
		return "length"
	case "stop", "":
		return "stop"
	default:
		return reason
	}
}
