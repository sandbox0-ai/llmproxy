package proxy

import (
	"testing"
)

func TestParseClaude2CodexRouteRawURL(t *testing.T) {
	route, err := parseClaude2CodexRoute("/claude2codex/https://api.z.ai/anthropic/v1/messages/responses")
	if err != nil {
		t.Fatalf("parse route: %v", err)
	}
	if route.AnthropicMessagesURL != "https://api.z.ai/anthropic/v1/messages" {
		t.Fatalf("upstream = %q", route.AnthropicMessagesURL)
	}
	if route.Endpoint != "/responses" {
		t.Fatalf("endpoint = %q", route.Endpoint)
	}
}

func TestParseClaude2CodexRouteV1ResponsesSuffix(t *testing.T) {
	route, err := parseClaude2CodexRoute("/claude2codex/https://api.z.ai/anthropic/v1/messages/v1/responses")
	if err != nil {
		t.Fatalf("parse route: %v", err)
	}
	if route.AnthropicMessagesURL != "https://api.z.ai/anthropic/v1/messages" {
		t.Fatalf("upstream = %q", route.AnthropicMessagesURL)
	}
}

func TestParseClaude2CodexRouteRejectsNonMessagesURL(t *testing.T) {
	_, err := parseClaude2CodexRoute("/claude2codex/https://api.z.ai/anthropic/v1/responses")
	if err == nil {
		t.Fatal("parse route succeeded, want error")
	}
}

func TestValidateUpstreamURLRejectsUnsafeTargets(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/v1",
		"http://metadata.google.internal/v1",
		"file:///tmp/provider",
		"https://user:pass@example.com/v1",
		"https://api.example.com:8443/v1",
	}
	for _, tc := range cases {
		if _, err := validateUpstreamURL(tc); err == nil {
			t.Fatalf("validateUpstreamURL(%q) succeeded, want error", tc)
		}
	}
}
