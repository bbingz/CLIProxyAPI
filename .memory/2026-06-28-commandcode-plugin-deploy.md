# 2026-06-28 Command Code plugin deployment

## Summary

- Implemented the Command Code provider as a Go plugin with OAuth callback support through `/v0/plugin/oauth-callback`.
- Deployed the feature branch build to `10.0.8.9` for live stability testing.
- Installed the Command Code plugin at `/Users/bing/-Tools-/CLIProxyAPI/plugins/commandcode.dylib`.
- Installed Command Code auth material at `/Users/bing/.cli-proxy-api/commandcode.json` with mode `0600`.

## Live host state

- Host: `10.0.8.9`
- Service command: `/Users/bing/-Tools-/CLIProxyAPI/bin/CLIProxyAPI -config /Users/bing/-Tools-/CLIProxyAPI/config.yaml`
- Active PID after stream fix: `23830`
- Listening port: `8317`
- Main deployment backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-181740`
- Stream-fix plugin backup: `/Users/bing/-Tools-/CLIProxyAPI/deploy-backups/20260628-182420-streamfix`
- Deployed stream-fix plugin sha256: `e0845c89c3d1e96b332fdcece09cd85abae68b0a4db07394250534c6aeeab27b`

## Verification

- `/v1/models` on `10.0.8.9` returned HTTP 200 with 35 models.
- Command Code models visible: `deepseek/deepseek-v4-flash`, `deepseek/deepseek-v4-pro`.
- Existing model counts remained unchanged after plugin enablement: Grok 12, Gemini 8.
- `zai-org/GLM-5.2` was already visible from the existing provider pool and passed a non-streaming chat smoke test.
- Command Code non-streaming smoke: `deepseek/deepseek-v4-flash` returned HTTP 200, content `OK`, usage present.
- Command Code streaming smoke after fix: `deepseek/deepseek-v4-flash` returned HTTP 200, content `OK`, 15 JSON chunks, 12 reasoning chunks, `[DONE]` observed, malformed chunk count 0.

## Notes

- The initial streaming deploy emitted plugin chunks as full SSE frames, while CLIProxyAPI's OpenAI handler also wraps chunks as SSE. That produced downstream `data: data: {...}` lines.
- The stream fix changes Command Code plugin stream output to raw OpenAI chat-completion chunk JSON. The existing OpenAI handler remains responsible for `data:` framing and terminal `[DONE]`.
- GLM-5.2 is not part of the new Command Code plugin model list in this branch.
