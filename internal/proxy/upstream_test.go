package proxy

import (
	"testing"
)

func TestParseClaude2CodexRouteRawURL(t *testing.T) {
	route, err := parseClaude2CodexRoute("/claude2codex/https://api.z.ai/anthropic/v1/responses")
	if err != nil {
		t.Fatalf("parse route: %v", err)
	}
	if route.UpstreamBase != "https://api.z.ai/anthropic" {
		t.Fatalf("upstream = %q", route.UpstreamBase)
	}
	if route.Endpoint != "/responses" {
		t.Fatalf("endpoint = %q", route.Endpoint)
	}
}

func TestAnthropicMessagesURL(t *testing.T) {
	got, err := anthropicMessagesURL("https://api.z.ai/anthropic")
	if err != nil {
		t.Fatalf("anthropic URL: %v", err)
	}
	if got != "https://api.z.ai/anthropic/v1/messages" {
		t.Fatalf("url = %q", got)
	}
	got, err = anthropicMessagesURL("https://api.z.ai/anthropic/v1")
	if err != nil {
		t.Fatalf("anthropic URL: %v", err)
	}
	if got != "https://api.z.ai/anthropic/v1/messages" {
		t.Fatalf("url with v1 = %q", got)
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
