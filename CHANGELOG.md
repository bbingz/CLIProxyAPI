# Changelog

## 2026-06-28

- Added Command Code short model aliases `glm-5.2` and `glm-5.1` while normalizing upstream requests to `zai-org/GLM-5.2` and `zai-org/GLM-5.1`; verified on `10.0.8.9` that `/v1/models` exposes both short aliases, `glm-5.2` non-streaming returns HTTP 200/content `OK`, and `glm-5.2` streaming returns HTTP 200/content `OK`/3 chunks/0 malformed chunks.
- Added a Command Code Go plugin with OAuth callback support, model registration, OpenAI chat-completion request translation, and Command Code `/alpha/generate` response translation.
- Deployed the branch build to `10.0.8.9` with the plugin enabled under `/Users/bing/-Tools-/CLIProxyAPI/plugins/commandcode.dylib` and Command Code auth stored at `/Users/bing/.cli-proxy-api/commandcode.json`.
- Fixed Command Code streaming output so plugin chunks are raw OpenAI chat-completion JSON payloads; CLIProxyAPI's OpenAI handler owns SSE `data:` framing and terminal `[DONE]`.
- Verified on `10.0.8.9`: `/v1/models` HTTP 200 with 12 `owned_by=commandcode` models including `deepseek/deepseek-v4-flash`, `deepseek/deepseek-v4-pro`, `zai-org/GLM-5.2`, and `zai-org/GLM-5.1`; Grok count 12; Gemini count 8; Command Code non-streaming smoke HTTP 200/content `OK`; Command Code streaming smoke HTTP 200/content `OK`/15 chunks/0 malformed chunks; `zai-org/GLM-5.2` non-streaming smoke HTTP 200/content `OK`.
