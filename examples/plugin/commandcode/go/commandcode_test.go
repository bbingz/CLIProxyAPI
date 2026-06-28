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
		`{"type":"finish-step","finishReason":"stop","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5,"reasoningTokens":1}}`,
		``,
	}, "\n")), "deepseek/deepseek-v4-pro", 123)
	if err != nil {
		t.Fatalf("convert ndjson: %v", err)
	}
	var completion map[string]any
	if errUnmarshal := json.Unmarshal(body, &completion); errUnmarshal != nil {
		t.Fatalf("unmarshal completion: %v", errUnmarshal)
	}
	if id, ok := completion["id"].(string); !ok || !strings.HasPrefix(id, "chatcmpl-commandcode-") {
		t.Fatalf("id = %#v, want generated chat completion id", completion["id"])
	}
	if completion["object"] != "chat.completion" || completion["created"] != float64(123) || completion["model"] != "deepseek/deepseek-v4-pro" {
		t.Fatalf("completion metadata = %#v", completion)
	}
	choices := completion["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["role"] != "assistant" || message["content"] != "hello world" {
		t.Fatalf("message = %#v", message)
	}
	if _, ok := message["reasoning_content"]; ok {
		t.Fatalf("message has non-standard reasoning_content: %#v", message)
	}
	usage := completion["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(3) || usage["completion_tokens"] != float64(2) || usage["total_tokens"] != float64(5) {
		t.Fatalf("usage = %#v", usage)
	}
	completionDetails := usage["completion_tokens_details"].(map[string]any)
	if completionDetails["reasoning_tokens"] != float64(1) {
		t.Fatalf("completion_tokens_details = %#v", completionDetails)
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

func TestCommandCodeNDJSONToChatCompletionPrefersFullToolCallInput(t *testing.T) {
	body, err := commandCodeNDJSONToChatCompletion([]byte(strings.Join([]string{
		`{"type":"tool-input-start","id":"call_1","toolName":"read_file"}`,
		`{"type":"tool-input-end","id":"call_1"}`,
		`{"type":"tool-call","toolCallId":"call_1","toolName":"read_file","input":{"path":"README.md"}}`,
		`{"type":"finish-step","finishReason":"stop","usage":{"inputTokens":10,"outputTokens":1,"totalTokens":11}}`,
	}, "\n")), "glm-5.2", 123)
	if err != nil {
		t.Fatalf("convert ndjson: %v", err)
	}
	var completion map[string]any
	if errUnmarshal := json.Unmarshal(body, &completion); errUnmarshal != nil {
		t.Fatalf("unmarshal completion: %v", errUnmarshal)
	}
	choice := completion["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", choice["finish_reason"])
	}
	message := choice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	call := toolCalls[0].(map[string]any)
	fn := call["function"].(map[string]any)
	if fn["name"] != "read_file" || fn["arguments"] != `{"path":"README.md"}` {
		t.Fatalf("tool call function = %#v", fn)
	}
}

func TestCommandCodeEventToOpenAIStreamChunksReturnsPayloadOnly(t *testing.T) {
	chunks, err := commandCodeEventToOpenAIStreamChunks([]byte(`{"type":"text-delta","id":"txt-0","text":"hello"}`), "deepseek/deepseek-v4-pro", 123)
	if err != nil {
		t.Fatalf("convert stream event: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want role and content chunks", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.HasPrefix(string(chunk), "data:") || strings.Contains(string(chunk), "[DONE]") {
			t.Fatalf("chunk must be a raw JSON payload, got %q", string(chunk))
		}
	}
	var roleChunk map[string]any
	if errUnmarshal := json.Unmarshal(chunks[0], &roleChunk); errUnmarshal != nil {
		t.Fatalf("unmarshal role chunk: %v", errUnmarshal)
	}
	roleDelta := roleChunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if roleDelta["role"] != "assistant" {
		t.Fatalf("role delta = %#v", roleDelta)
	}
	var chunk map[string]any
	if errUnmarshal := json.Unmarshal(chunks[1], &chunk); errUnmarshal != nil {
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
	if len(finishChunks) != 2 || strings.Contains(string(finishChunks[0]), "[DONE]") || strings.Contains(string(finishChunks[1]), "[DONE]") {
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
	if len(finishChunks) != 3 {
		t.Fatalf("finish chunks = %d, want role, finish, and usage chunks", len(finishChunks))
	}
	var finish map[string]any
	if errUnmarshal := json.Unmarshal(finishChunks[1], &finish); errUnmarshal != nil {
		t.Fatalf("unmarshal finish chunk: %v", errUnmarshal)
	}
	choice := finish["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish chunk = %#v", finish)
	}
	var usage map[string]any
	if errUnmarshal := json.Unmarshal(finishChunks[2], &usage); errUnmarshal != nil {
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

func TestCommandCodeStreamConverterEmitsStrictOpenAIFields(t *testing.T) {
	converter := newCommandCodeStreamConverter("glm-5.2", 123)
	reasoningChunks, err := converter.ConvertLine([]byte(`{"type":"reasoning-delta","id":"reasoning-0","text":"think"}`))
	if err != nil {
		t.Fatalf("convert reasoning: %v", err)
	}
	if len(reasoningChunks) != 0 {
		t.Fatalf("reasoning emitted non-standard chunks: %#v", reasoningChunks)
	}

	textChunks, err := converter.ConvertLine([]byte(`{"type":"text-delta","id":"txt-0","text":"hello"}`))
	if err != nil {
		t.Fatalf("convert text: %v", err)
	}
	if len(textChunks) != 2 {
		t.Fatalf("text chunks = %d, want role and content", len(textChunks))
	}
	firstID := assertOpenAIStreamChunk(t, textChunks[0], map[string]any{"role": "assistant"}, "")
	secondID := assertOpenAIStreamChunk(t, textChunks[1], map[string]any{"content": "hello"}, "")
	if firstID != secondID {
		t.Fatalf("stream chunk ids differ: %q vs %q", firstID, secondID)
	}
	for _, chunk := range textChunks {
		if strings.Contains(string(chunk), "reasoning_content") {
			t.Fatalf("stream chunk has non-standard reasoning_content: %s", string(chunk))
		}
	}
}

func TestCommandCodeStreamConverterStreamsToolCalls(t *testing.T) {
	converter := newCommandCodeStreamConverter("glm-5.2", 123)
	lines := []string{
		`{"type":"tool-input-start","id":"call_1","toolName":"read_file"}`,
		`{"type":"tool-input-delta","id":"call_1","delta":"{\"path\""}`,
		`{"type":"tool-input-delta","id":"call_1","delta":":\"README.md\"}"}`,
		`{"type":"tool-input-end","id":"call_1"}`,
		`{"type":"tool-call","toolCallId":"call_1","toolName":"read_file","input":{"path":"README.md"}}`,
		`{"type":"finish-step","finishReason":"tool-calls","usage":{"inputTokens":10,"cachedInputTokens":2,"outputTokens":4,"totalTokens":14,"outputTokenDetails":{"reasoningTokens":3}}}`,
		`{"type":"finish","finishReason":"tool-calls"}`,
	}
	var chunks [][]byte
	for _, line := range lines {
		out, err := converter.ConvertLine([]byte(line))
		if err != nil {
			t.Fatalf("convert %s: %v", line, err)
		}
		chunks = append(chunks, out...)
	}
	if len(chunks) != 6 {
		t.Fatalf("chunks = %d, want role, tool start, two args deltas, finish, usage", len(chunks))
	}
	assertOpenAIStreamChunk(t, chunks[0], map[string]any{"role": "assistant"}, "")
	assertToolCallDelta(t, chunks[1], 0, "call_1", "function", "read_file", "")
	assertToolCallDelta(t, chunks[2], 0, "", "", "", `{"path"`)
	assertToolCallDelta(t, chunks[3], 0, "", "", "", `:"README.md"}`)
	assertOpenAIStreamChunk(t, chunks[4], map[string]any{}, "tool_calls")

	var usage map[string]any
	if errUnmarshal := json.Unmarshal(chunks[5], &usage); errUnmarshal != nil {
		t.Fatalf("unmarshal usage chunk: %v", errUnmarshal)
	}
	if len(usage["choices"].([]any)) != 0 {
		t.Fatalf("usage choices = %#v, want empty", usage["choices"])
	}
	gotUsage := usage["usage"].(map[string]any)
	if gotUsage["prompt_tokens"] != float64(10) || gotUsage["completion_tokens"] != float64(4) || gotUsage["total_tokens"] != float64(14) {
		t.Fatalf("usage = %#v", gotUsage)
	}
	promptDetails := gotUsage["prompt_tokens_details"].(map[string]any)
	if promptDetails["cached_tokens"] != float64(2) {
		t.Fatalf("prompt_tokens_details = %#v", promptDetails)
	}
	completionDetails := gotUsage["completion_tokens_details"].(map[string]any)
	if completionDetails["reasoning_tokens"] != float64(3) {
		t.Fatalf("completion_tokens_details = %#v", completionDetails)
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

func assertOpenAIStreamChunk(t *testing.T, raw []byte, wantDelta map[string]any, wantFinish string) string {
	t.Helper()
	var chunk map[string]any
	if errUnmarshal := json.Unmarshal(raw, &chunk); errUnmarshal != nil {
		t.Fatalf("unmarshal stream chunk: %v", errUnmarshal)
	}
	id, ok := chunk["id"].(string)
	if !ok || !strings.HasPrefix(id, "chatcmpl-commandcode-") {
		t.Fatalf("id = %#v, want generated chat completion id", chunk["id"])
	}
	if chunk["object"] != "chat.completion.chunk" || chunk["created"] != float64(123) || chunk["model"] != "zai-org/GLM-5.2" {
		t.Fatalf("chunk metadata = %#v", chunk)
	}
	choices := chunk["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %#v", choices)
	}
	choice := choices[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	for key, want := range wantDelta {
		if delta[key] != want {
			t.Fatalf("delta[%s] = %#v, want %#v in %#v", key, delta[key], want, delta)
		}
	}
	if _, ok := delta["reasoning_content"]; ok {
		t.Fatalf("delta has non-standard reasoning_content: %#v", delta)
	}
	if wantFinish == "" {
		if got := choice["finish_reason"]; got != nil {
			t.Fatalf("finish_reason = %#v, want nil or absent", got)
		}
	} else if choice["finish_reason"] != wantFinish {
		t.Fatalf("finish_reason = %#v, want %q", choice["finish_reason"], wantFinish)
	}
	return id
}

func assertToolCallDelta(t *testing.T, raw []byte, wantIndex int, wantID, wantType, wantName, wantArguments string) {
	t.Helper()
	var chunk map[string]any
	if errUnmarshal := json.Unmarshal(raw, &chunk); errUnmarshal != nil {
		t.Fatalf("unmarshal tool chunk: %v", errUnmarshal)
	}
	choice := chunk["choices"].([]any)[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	toolCalls := delta["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v", toolCalls)
	}
	call := toolCalls[0].(map[string]any)
	if call["index"] != float64(wantIndex) {
		t.Fatalf("tool_call.index = %#v, want %d", call["index"], wantIndex)
	}
	if wantID != "" && call["id"] != wantID {
		t.Fatalf("tool_call.id = %#v, want %q", call["id"], wantID)
	}
	if wantType != "" && call["type"] != wantType {
		t.Fatalf("tool_call.type = %#v, want %q", call["type"], wantType)
	}
	fn := call["function"].(map[string]any)
	if wantName != "" && fn["name"] != wantName {
		t.Fatalf("tool_call.function.name = %#v, want %q", fn["name"], wantName)
	}
	if wantArguments != "" && fn["arguments"] != wantArguments {
		t.Fatalf("tool_call.function.arguments = %#v, want %q", fn["arguments"], wantArguments)
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
