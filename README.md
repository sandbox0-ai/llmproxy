# llmproxy

Protocol-translating LLM proxy for coding agents.

The first supported mode is `claude2codex`: clients speak OpenAI Responses API
and upstream providers speak Anthropic Messages API. This lets Codex and
OpenAI Agents use Claude-compatible providers.

## Run

```bash
go run ./cmd/llmproxy
```

The service listens on `:8080` by default. Override with `PORT` or
`LLMPROXY_ADDR`.

## URL Shape

Take the provider's Anthropic Messages URL:

```text
https://api.z.ai/anthropic/v1/messages
```

Put `https://llmproxy.sandbox0.ai/claude2codex/` in front of it:

```text
https://llmproxy.sandbox0.ai/claude2codex/https://api.z.ai/anthropic/v1/messages
```

## Environment

| Variable | Description |
| --- | --- |
| `PORT` | HTTP port when `LLMPROXY_ADDR` is unset. |
| `LLMPROXY_ADDR` | Full listen address, for example `:8080`. |
| `LLMPROXY_WEB_SEARCH_KEY` | Tavily (`tvly-...`) or Brave (`BSA...`) key for proxy-side web search. |

## Current Status

Implemented:

- `claude2codex` for Anthropic Messages URLs
- OpenAI Responses text input to Anthropic Messages
- Function tool call and tool result conversion
- Anthropic Messages text/tool output to OpenAI Responses
- SSE response shape for streaming clients
- Proxy-side `web_search` tool loop when `LLMPROXY_WEB_SEARCH_KEY` is configured
- Static landing page and config generator at `/`
- SSRF hardening for dynamic upstream URLs

Planned:

- `codex2claude`
- `/responses/compact`
- richer multimodal mapping
- persisted usage logging and per-key policy
