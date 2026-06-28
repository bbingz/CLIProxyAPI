package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestStartCommandCodeLoginBuildsStudioURL(t *testing.T) {
	resp, err := startCommandCodeLogin(pluginapi.AuthLoginStartRequest{
		Provider: "commandcode",
		BaseURL:  "http://127.0.0.1:8317/v0/plugin/oauth-callback",
	}, func() (string, error) { return "state-123", nil }, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("start login: %v", err)
	}
	if resp.Provider != providerID {
		t.Fatalf("provider = %q, want %q", resp.Provider, providerID)
	}
	if resp.State != "state-123" {
		t.Fatalf("state = %q, want fixed state", resp.State)
	}
	loginURL, errParse := url.Parse(resp.URL)
	if errParse != nil {
		t.Fatalf("parse login URL: %v", errParse)
	}
	if loginURL.Scheme != "https" || loginURL.Host != "commandcode.ai" || loginURL.Path != "/studio/auth/cli" {
		t.Fatalf("login URL = %s, want commandcode studio auth", resp.URL)
	}
	callbackURL, errParseCallback := url.Parse(loginURL.Query().Get("callback"))
	if errParseCallback != nil {
		t.Fatalf("parse callback URL: %v", errParseCallback)
	}
	if callbackURL.Scheme != "http" || callbackURL.Host != "127.0.0.1:8317" || callbackURL.Path != "/v0/plugin/oauth-callback" {
		t.Fatalf("callback URL = %s", callbackURL.String())
	}
	if callbackURL.Query().Get("state") != "state-123" || callbackURL.Query().Get("provider") != providerID {
		t.Fatalf("callback query = %s", callbackURL.RawQuery)
	}
	allowed, ok := resp.Metadata["allowed_origins"].([]string)
	if !ok || len(allowed) == 0 || allowed[0] != "https://commandcode.ai" {
		t.Fatalf("allowed origins = %#v", resp.Metadata["allowed_origins"])
	}
}

func TestPollCommandCodeLoginReturnsAuthAfterCallback(t *testing.T) {
	resp, err := pollCommandCodeLogin(pluginapi.AuthLoginPollRequest{
		State: "state-123",
		Metadata: map[string]any{
			"apiKey":   "user_test_commandcode_token",
			"userId":   "user-123",
			"userName": "Ada",
			"keyName":  "CPA",
		},
	})
	if err != nil {
		t.Fatalf("poll login: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("status = %q, want success", resp.Status)
	}
	if resp.Auth.Provider != providerID || resp.Auth.ID == "" || resp.Auth.FileName == "" {
		t.Fatalf("unexpected auth metadata: %+v", resp.Auth)
	}
	if resp.Auth.Label != "Command Code (Ada)" {
		t.Fatalf("label = %q", resp.Auth.Label)
	}
	var storage commandCodeAuthStorage
	if errUnmarshal := json.Unmarshal(resp.Auth.StorageJSON, &storage); errUnmarshal != nil {
		t.Fatalf("unmarshal storage: %v", errUnmarshal)
	}
	if storage.APIKey != "user_test_commandcode_token" || storage.UserID != "user-123" {
		t.Fatalf("storage = %+v", storage)
	}
}

func TestBuildCommandCodeHTTPRequestUsesAlphaGenerateEnvelope(t *testing.T) {
	payload := []byte(`{
		"model":"deepseek/deepseek-v4-pro",
		"stream":false,
		"messages":[
			{"role":"system","content":"system prompt"},
			{"role":"user","content":"hello"}
		],
		"max_tokens":123,
		"temperature":0.2,
		"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object"}}}]
	}`)
	req, err := buildCommandCodeHTTPRequest(pluginapi.ExecutorRequest{
		Model:       "deepseek/deepseek-v4-pro",
		Payload:     payload,
		StorageJSON: []byte(`{"apiKey":"user_test_commandcode_token"}`),
	}, commandCodeConfig{
		APIBaseURL:  "https://api.commandcode.ai",
		CLIVersion:  "0.40.11",
		Environment: "production",
	}, "session-123", "2026-06-28")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost || req.URL != "https://api.commandcode.ai/alpha/generate" {
		t.Fatalf("upstream = %s %s", req.Method, req.URL)
	}
	if got := req.Headers.Get("Authorization"); got != "Bearer user_test_commandcode_token" {
		t.Fatalf("authorization = %q", got)
	}
	if got := req.Headers.Get("x-command-code-version"); got != "0.40.11" {
		t.Fatalf("version header = %q", got)
	}
	var body map[string]any
	if errUnmarshal := json.Unmarshal(req.Body, &body); errUnmarshal != nil {
		t.Fatalf("unmarshal body: %v", errUnmarshal)
	}
	params := body["params"].(map[string]any)
	if params["stream"] != true {
		t.Fatalf("params.stream = %#v, want true", params["stream"])
	}
	if params["system"] != "system prompt" {
		t.Fatalf("system = %#v", params["system"])
	}
	messages := params["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "user" || !strings.Contains(string(mustJSON(t, first["content"])), "hello") {
		t.Fatalf("messages = %#v", messages)
	}
	tools := params["tools"].([]any)
	if !strings.Contains(string(mustJSON(t, tools[0])), "input_schema") {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestCommandCodeNDJSONToChatCompletion(t *testing.T) {
	body, err := commandCodeNDJSONToChatCompletion([]byte(strings.Join([]string{
		`{"type":"reasoning-delta","id":"reasoning-0","text":"think"}`,
		`{"type":"text-delta","id":"txt-0","text":"hello"}`,
		`{"type":"text-delta","id":"txt-0","text":" world"}`,
		`{"type":"finish-step","finishReason":"stop","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5}}`,
		``,
	}, "\n")), "deepseek/deepseek-v4-pro", 123)
	if err != nil {
		t.Fatalf("convert ndjson: %v", err)
	}
	var completion map[string]any
	if errUnmarshal := json.Unmarshal(body, &completion); errUnmarshal != nil {
		t.Fatalf("unmarshal completion: %v", errUnmarshal)
	}
	choices := completion["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "hello world" || message["reasoning_content"] != "think" {
		t.Fatalf("message = %#v", message)
	}
	usage := completion["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(3) || usage["completion_tokens"] != float64(2) || usage["total_tokens"] != float64(5) {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestCommandCodeUsageUsesOpenAICompatibleTotals(t *testing.T) {
	body, err := commandCodeNDJSONToChatCompletion([]byte(`{"type":"finish-step","finishReason":"stop","usage":{"inputTokens":10,"cachedInputTokens":3,"outputTokens":4,"totalTokens":999}}`), "glm-5.2", 123)
	if err != nil {
		t.Fatalf("convert ndjson: %v", err)
	}
	var completion map[string]any
	if errUnmarshal := json.Unmarshal(body, &completion); errUnmarshal != nil {
		t.Fatalf("unmarshal completion: %v", errUnmarshal)
	}
	usage := completion["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(4) || usage["total_tokens"] != float64(14) {
		t.Fatalf("usage = %#v", usage)
	}
	details := usage["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"] != float64(3) {
		t.Fatalf("prompt_tokens_details = %#v", details)
	}
}

func TestCommandCodeEventToOpenAIStreamChunksReturnsPayloadOnly(t *testing.T) {
	chunks, err := commandCodeEventToOpenAIStreamChunks([]byte(`{"type":"text-delta","id":"txt-0","text":"hello"}`), "deepseek/deepseek-v4-pro", 123)
	if err != nil {
		t.Fatalf("convert stream event: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if strings.HasPrefix(string(chunks[0]), "data:") || strings.Contains(string(chunks[0]), "[DONE]") {
		t.Fatalf("chunk must be a raw JSON payload, got %q", string(chunks[0]))
	}
	var chunk map[string]any
	if errUnmarshal := json.Unmarshal(chunks[0], &chunk); errUnmarshal != nil {
		t.Fatalf("unmarshal chunk: %v", errUnmarshal)
	}
	choice := chunk["choices"].([]any)[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	if delta["content"] != "hello" {
		t.Fatalf("delta = %#v", delta)
	}

	finishChunks, err := commandCodeEventToOpenAIStreamChunks([]byte(`{"type":"finish","finishReason":"stop"}`), "deepseek/deepseek-v4-pro", 123)
	if err != nil {
		t.Fatalf("convert finish event: %v", err)
	}
	if len(finishChunks) != 1 || strings.Contains(string(finishChunks[0]), "[DONE]") {
		t.Fatalf("finish chunks = %#v", finishChunks)
	}
}

func TestCommandCodeStreamConverterEmitsFinalUsageOnce(t *testing.T) {
	converter := newCommandCodeStreamConverter("glm-5.2", 123)
	stepChunks, err := converter.ConvertLine([]byte(`{"type":"finish-step","finishReason":"stop","usage":{"inputTokens":10,"cachedInputTokens":3,"outputTokens":4,"totalTokens":14}}`))
	if err != nil {
		t.Fatalf("convert finish-step: %v", err)
	}
	if len(stepChunks) != 0 {
		t.Fatalf("finish-step chunks = %d, want usage cached until terminal finish", len(stepChunks))
	}
	finishChunks, err := converter.ConvertLine([]byte(`{"type":"finish","finishReason":"stop"}`))
	if err != nil {
		t.Fatalf("convert finish: %v", err)
	}
	if len(finishChunks) != 2 {
		t.Fatalf("finish chunks = %d, want finish and usage chunks", len(finishChunks))
	}
	var finish map[string]any
	if errUnmarshal := json.Unmarshal(finishChunks[0], &finish); errUnmarshal != nil {
		t.Fatalf("unmarshal finish chunk: %v", errUnmarshal)
	}
	choice := finish["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish chunk = %#v", finish)
	}
	var usage map[string]any
	if errUnmarshal := json.Unmarshal(finishChunks[1], &usage); errUnmarshal != nil {
		t.Fatalf("unmarshal usage chunk: %v", errUnmarshal)
	}
	if len(usage["choices"].([]any)) != 0 {
		t.Fatalf("usage choices = %#v, want empty", usage["choices"])
	}
	gotUsage := usage["usage"].(map[string]any)
	if gotUsage["prompt_tokens"] != float64(10) || gotUsage["completion_tokens"] != float64(4) || gotUsage["total_tokens"] != float64(14) {
		t.Fatalf("usage = %#v", gotUsage)
	}
	details := gotUsage["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"] != float64(3) {
		t.Fatalf("prompt_tokens_details = %#v", details)
	}
	duplicate, err := converter.ConvertLine([]byte(`{"type":"finish","finishReason":"stop"}`))
	if err != nil {
		t.Fatalf("convert duplicate finish: %v", err)
	}
	if len(duplicate) != 0 {
		t.Fatalf("duplicate finish emitted %d chunks", len(duplicate))
	}
}

func TestCommandCodeShortAliases(t *testing.T) {
	models := defaultCommandCodeModels()
	seen := map[string]bool{}
	for _, model := range models {
		seen[model.ID] = true
	}
	aliases := map[string]string{
		"deepseek-v4-pro":            "deepseek/deepseek-v4-pro",
		"deepseek-v4-flash":          "deepseek/deepseek-v4-flash",
		"kimi-k2.7-code":             "moonshotai/Kimi-K2.7-Code",
		"kimi-k2.7-code-highspeed":   "moonshotai/Kimi-K2.7-Code-Highspeed",
		"kimi-k2.6":                  "moonshotai/Kimi-K2.6",
		"kimi-k2.5":                  "moonshotai/Kimi-K2.5",
		"glm-5.2":                    "zai-org/GLM-5.2",
		"glm-5.1":                    "zai-org/GLM-5.1",
		"minimax-m3":                 "MiniMaxAI/MiniMax-M3",
		"qwen3.7-max":                "Qwen/Qwen3.7-Max",
		"step-3.7-flash":             "stepfun/Step-3.7-Flash",
		"nemotron-3-ultra-550b-a55b": "nvidia/nemotron-3-ultra-550b-a55b",
	}
	for alias, canonical := range aliases {
		if !seen[alias] {
			t.Fatalf("alias model %q not registered", alias)
		}
		if !seen[canonical] {
			t.Fatalf("canonical model %q not registered", canonical)
		}
		if got := normalizeCommandCodeModel(alias); got != canonical {
			t.Fatalf("normalize %q = %q, want %q", alias, got, canonical)
		}
		if got := normalizeCommandCodeModel("commandcode/" + alias); got != canonical {
			t.Fatalf("normalize commandcode/%s = %q, want %q", alias, got, canonical)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
