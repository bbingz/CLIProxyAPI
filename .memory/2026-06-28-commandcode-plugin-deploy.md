# 2026-06-28 Command Code plugin deployment

## Summary

- Implemented the Command Code provider as a Go plugin with OAuth callback support through `/v0/plugin/oauth-callback`.
- Deployed the feature branch build to `10.0.8.9` for live stability testing.
- Installed the Command Code plugin at `/Users/bing/-Tools-/CLIProxyAPI/plugins/commandcode.dylib`.
- Installed Command Code auth material at `/Users/bing/.cli-proxy-api/commandcode.json` with mode `0600`.
- Added short aliases for every Command Code canonical model after UI validation showed clients commonly use un-namespaced names such as `glm-5.2`.
- Fixed streaming usage accounting so New API-style gateways can read completion tokens and token-rate data from Command Code streams.

## Live host state

- Host: `10.0.8.9`
- Service command: `/Users/bing/-Tools-/CLIProxyAPI/bin/CLIProxyAPI -config /Users/bing/-Tools-/CLIProxyAPI/config.yaml`
- Active PID after streaming-usage deploy: `29877`
- Listening port: `8317`
- Main deployment backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-181740`
- Stream-fix plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-182420-streamfix`
- GLM alias plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-184036-glm-alias`
- All-alias plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-192513-all-aliases`
- Stream-usage plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-193800-stream-usage-openai`
- Deployed stream-usage plugin sha256: `ed4289653d253334bf7e39f7894f8a61b930d639e0ff66a090d0feb195f8b496`

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

## Notes

- The initial streaming deploy emitted plugin chunks as full SSE frames, while CLIProxyAPI's OpenAI handler also wraps chunks as SSE. That produced downstream `data: data: {...}` lines.
- The stream fix changes Command Code plugin stream output to raw OpenAI chat-completion chunk JSON. The existing OpenAI handler remains responsible for `data:` framing and terminal `[DONE]`.
- GLM-5.2 and GLM-5.1 are part of the Command Code plugin model list in this branch.
- Short aliases normalize only for upstream execution; response `model` currently reports the canonical Command Code ID such as `zai-org/GLM-5.2` or `deepseek/deepseek-v4-flash`.
- During this debugging session, repeated CLIProxyAPI restarts on `10.0.8.9` would have interrupted in-flight requests and can explain short-term split-routing instability seen during deployment windows. Long-run stability still needs a post-fix split test after Channel #10 is enabled in New API.
