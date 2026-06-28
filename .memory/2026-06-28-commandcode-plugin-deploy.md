# 2026-06-28 Command Code plugin deployment

## Summary

- Implemented the Command Code provider as a Go plugin with OAuth callback support through `/v0/plugin/oauth-callback`.
- Deployed the feature branch build to `10.0.8.9` for live stability testing.
- Installed the Command Code plugin at `/Users/bing/-Tools-/CLIProxyAPI/plugins/commandcode.dylib`.
- Installed Command Code auth material at `/Users/bing/.cli-proxy-api/commandcode.json` with mode `0600`.
- Added short aliases for every Command Code canonical model after UI validation showed clients commonly use un-namespaced names such as `glm-5.2`.
- Fixed streaming usage accounting so New API-style gateways can read completion tokens and token-rate data from Command Code streams.
- Normalized Command Code responses to strict OpenAI Chat Completions field shapes by removing `reasoning_content`, adding generated completion IDs, emitting stream role chunks, converting streamed tool calls into `delta.tool_calls`, and preserving reasoning token counts only under `completion_tokens_details.reasoning_tokens`.

## Live host state

- Host: `10.0.8.9`
- Service command: `/Users/bing/-Tools-/CLIProxyAPI/bin/CLIProxyAPI -config /Users/bing/-Tools-/CLIProxyAPI/config.yaml`
- Active PID after strict-response deploy: `32493`
- Listening port: `8317`
- Main deployment backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-181740`
- Stream-fix plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-182420-streamfix`
- GLM alias plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-184036-glm-alias`
- All-alias plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-192513-all-aliases`
- Stream-usage plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-193800-stream-usage-openai`
- Strict-response v1 plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-200248-strict-openai-response`
- Strict-response v2 plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-201000-strict-openai-response-v2`
- Deployed strict-response plugin sha256: `4ab10669b86f75d3cd0346659c9e3e5cd37328c25dd5746474030776e7d591bb`

## Verification

- `/v1/models` on `10.0.8.9` returned HTTP 200 with 47 models.
- Command Code models visible: 24 `owned_by=commandcode` models, covering 12 canonical model IDs plus 12 short aliases.
- Existing model counts remained unchanged after plugin enablement: Grok 12, Gemini 8.
- `zai-org/GLM-5.2` is provided by the Command Code plugin and passed a non-streaming chat smoke test.
- Command Code non-streaming smoke: `deepseek/deepseek-v4-flash` returned HTTP 200, content `OK`, usage present.
- Command Code streaming smoke after fix: `deepseek/deepseek-v4-flash` returned HTTP 200, content `OK`, 15 JSON chunks, 12 reasoning chunks, `[DONE]` observed, malformed chunk count 0.
- Short alias smoke after fix: `/v1/models` exposes all 12 expected short aliases; `glm-5.2` and `deepseek-v4-flash` both returned HTTP 200/content `OK` for non-streaming and streaming requests, with `[DONE]` observed and malformed chunk count 0 for both streams.
- Streaming usage smoke after fix: direct `glm-5.2` streaming returned one finish chunk, one final usage chunk, `completion_tokens=3`, `prompt_tokens=7394`, `total_tokens=7397`, `prompt_tokens_details.cached_tokens=7360`, `[DONE]` observed, malformed chunk count 0. Direct non-streaming returned the same OpenAI-compatible usage totals.
- New API evidence: historical Channel #10 `glm-5.2` streaming logs before the fix had `completion_tokens=0`; Channel #3 Volcengine rows had non-zero completion tokens. New API Channel #10 currently has `status=2` while Channel #3 has `status=1`, so split-route stability tests may not include Command Code until Channel #10 is re-enabled.
- Strict-response unit coverage: Command Code plugin `go test .` covers non-streaming generated IDs, no `reasoning_content`, usage cached/reasoning token details, full `tool-call.input` overriding empty streamed arguments, stream role chunks, standard `delta.tool_calls`, finish reason `tool_calls`, final usage chunks, and duplicate terminal event suppression.
- Strict-response live verifier on `10.0.8.9:8317`: `glm-5.2` non-streaming returned HTTP 200, object `chat.completion`, message keys `content,role`, no `reasoning_content`, content `OK`, and OpenAI-compatible usage totals. `glm-5.2` streaming returned HTTP 200, one stream id, assistant role chunk, content `OK`, one finish chunk, one usage chunk, `[DONE]`, no malformed SSE lines, no `reasoning_content`, `prompt_tokens=7395`, `completion_tokens=36`, `total_tokens=7431`, `prompt_tokens_details.cached_tokens=7360`, and `completion_tokens_details.reasoning_tokens=31`.
- New API comparison on `10.0.8.9:3001`: active `glm-5.2` route is Channel #3 `火山-Coding`; Channel #10 `CommanCode` remains disabled by the user. Volcengine non-streaming and streaming responses include `reasoning_content`, so Volcengine is useful as a New API compatibility comparison but is not a strict OpenAI-field baseline. Direct `8317` Command Code responses are stricter than Volcengine on field shape while retaining reasoning counts in usage details.

## Notes

- The initial streaming deploy emitted plugin chunks as full SSE frames, while CLIProxyAPI's OpenAI handler also wraps chunks as SSE. That produced downstream `data: data: {...}` lines.
- The stream fix changes Command Code plugin stream output to raw OpenAI chat-completion chunk JSON. The existing OpenAI handler remains responsible for `data:` framing and terminal `[DONE]`.
- GLM-5.2 and GLM-5.1 are part of the Command Code plugin model list in this branch.
- Short aliases normalize only for upstream execution; response `model` currently reports the canonical Command Code ID such as `zai-org/GLM-5.2` or `deepseek/deepseek-v4-flash`.
- During this debugging session, repeated CLIProxyAPI restarts on `10.0.8.9` would have interrupted in-flight requests and can explain short-term split-routing instability seen during deployment windows. Long-run New API split stability still requires explicitly re-enabling Channel #10, which was not done during the strict-response comparison because the user disabled it to protect current Claude Code usage.
