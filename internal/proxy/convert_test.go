package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sandbox0-ai/llmproxy/internal/anthropic"
	"github.com/sandbox0-ai/llmproxy/internal/openairesp"
)

func TestConvertResponsesToAnthropicTextAndTool(t *testing.T) {
	req := openairesp.Request{
		Model:        "test-model",
		Instructions: "Be brief.",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		]`),
		Tools: []json.RawMessage{json.RawMessage(`{"type":"function","name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}`)},
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.Request.System != "Be brief." {
		t.Fatalf("system = %#v", got.Request.System)
	}
	if len(got.Request.Messages) != 3 {
		t.Fatalf("messages len = %d", len(got.Request.Messages))
	}
	toolUse := got.Request.Messages[1].Content[0]
	if toolUse.Type != "tool_use" || toolUse.Name != "get_weather" || string(toolUse.Input) != `{"city":"Paris"}` {
		t.Fatalf("tool use = %#v", toolUse)
	}
	toolResult := got.Request.Messages[2].Content[0]
	if toolResult.Type != "tool_result" || toolResult.ToolUseID != "call_1" || toolResult.Content != "sunny" {
		t.Fatalf("tool result = %#v", toolResult)
	}
	if len(got.Request.Tools) != 1 || got.Request.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %#v", got.Request.Tools)
	}
}

func TestConvertResponsesWebSearchTool(t *testing.T) {
	req := openairesp.Request{
		Model: "test-model",
		Input: json.RawMessage(`"what is current news?"`),
		Tools: []json.RawMessage{json.RawMessage(`{"type":"web_search_preview"}`)},
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Request.Tools) != 1 || got.Request.Tools[0].Name != proxyWebSearchToolName {
		t.Fatalf("tools = %#v", got.Request.Tools)
	}
	if got.Request.Tools[0].Type != "web_search_20250305" {
		t.Fatalf("tool type = %q", got.Request.Tools[0].Type)
	}
}

func TestConvertResponsesNamespaceTool(t *testing.T) {
	req := openairesp.Request{
		Model: "test-model",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"echo"}]},
			{"type":"function_call","call_id":"call_1","namespace":"mcp__contract","name":"echo","arguments":"{\"text\":\"ok\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]`),
		Tools: []json.RawMessage{json.RawMessage(`{
			"type":"namespace",
			"name":"mcp__contract",
			"description":"Contract MCP tools.",
			"tools":[{
				"type":"function",
				"name":"echo",
				"description":"Echo text.",
				"parameters":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}
			}]
		}`)},
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Request.Tools) != 1 {
		t.Fatalf("tools = %#v", got.Request.Tools)
	}
	if got.Request.Tools[0].Name != "mcp__contract__echo" {
		t.Fatalf("tool name = %q", got.Request.Tools[0].Name)
	}
	if !strings.Contains(got.Request.Tools[0].Description, "Contract MCP tools.") ||
		!strings.Contains(got.Request.Tools[0].Description, "Echo text.") {
		t.Fatalf("tool description = %q", got.Request.Tools[0].Description)
	}
	toolUse := got.Request.Messages[1].Content[0]
	if toolUse.Name != "mcp__contract__echo" || string(toolUse.Input) != `{"text":"ok"}` {
		t.Fatalf("tool use = %#v", toolUse)
	}
	if got.Tools["mcp__contract__echo"] != (responseToolName{Namespace: "mcp__contract", Name: "echo"}) {
		t.Fatalf("tool map = %#v", got.Tools)
	}
}

func TestConvertAnthropicToResponsesToolUse(t *testing.T) {
	resp := anthropic.Response{
		ID:    "msg_1",
		Model: "claude-ish",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Checking."},
			{Type: "tool_use", ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
		},
		Usage: &anthropic.Usage{InputTokens: 10, OutputTokens: 5},
	}
	got := convertAnthropicToResponses(resp, "codex-model", nil)
	if got.Model != "codex-model" {
		t.Fatalf("model = %q", got.Model)
	}
	if len(got.Output) != 2 {
		t.Fatalf("output len = %d", len(got.Output))
	}
	if got.Output[1].Type != "function_call" || got.Output[1].CallID != "toolu_1" || got.Output[1].Arguments != `{"city":"Paris"}` {
		t.Fatalf("function call = %#v", got.Output[1])
	}
	if got.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v", got.Usage)
	}
}

func TestConvertAnthropicToResponsesNamespaceToolUse(t *testing.T) {
	resp := anthropic.Response{
		ID:    "msg_1",
		Model: "claude-ish",
		Content: []anthropic.ContentBlock{
			{Type: "tool_use", ID: "toolu_1", Name: "mcp__contract__echo", Input: json.RawMessage(`{"text":"ok"}`)},
		},
	}
	got := convertAnthropicToResponses(resp, "codex-model", responseToolNameMap{
		"mcp__contract__echo": {Namespace: "mcp__contract", Name: "echo"},
	})
	if len(got.Output) != 1 {
		t.Fatalf("output len = %d", len(got.Output))
	}
	if got.Output[0].Type != "function_call" ||
		got.Output[0].Namespace != "mcp__contract" ||
		got.Output[0].Name != "echo" ||
		got.Output[0].Arguments != `{"text":"ok"}` {
		t.Fatalf("function call = %#v", got.Output[0])
	}
}

func TestConvertAnthropicToResponsesKeepsZeroUsageFields(t *testing.T) {
	resp := anthropic.Response{
		ID:    "msg_1",
		Model: "claude-ish",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Done."},
		},
	}
	got := convertAnthropicToResponses(resp, "codex-model", nil)
	if got.Usage == nil {
		t.Fatal("usage is nil")
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, field := range []string{`"input_tokens":0`, `"output_tokens":0`, `"total_tokens":0`} {
		if !strings.Contains(body, field) {
			t.Fatalf("response JSON missing %s: %s", field, body)
		}
	}
}
