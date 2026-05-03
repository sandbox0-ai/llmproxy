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
https://api.z.ai/api/anthropic/v1/messages
```

Put `https://llmproxy.sandbox0.ai/claude2codex/` in front of it:

```text
https://llmproxy.sandbox0.ai/claude2codex/https://api.z.ai/api/anthropic/v1/messages
```

The proxy service handles protocol routes only. Hosted marketing pages and
public ingress can be managed by a separate website deployment that forwards
proxy paths to this service.

## Environment

| Variable | Description |
| --- | --- |
| `PORT` | HTTP port when `LLMPROXY_ADDR` is unset. |
| `LLMPROXY_ADDR` | Full listen address, for example `:8080`. |

## Current Status

Implemented:

- `claude2codex` for Anthropic Messages URLs
- OpenAI Responses text input to Anthropic Messages
- Function tool call and tool result conversion
- OpenAI `web_search` tool to Anthropic `web_search_20250305` server tool conversion
- Anthropic Messages text/tool output to OpenAI Responses
- SSE response shape for streaming clients
- SSRF hardening for dynamic upstream URLs

Planned:

- `codex2claude`
- `/responses/compact`
- richer multimodal mapping
- persisted usage logging and per-key policy
