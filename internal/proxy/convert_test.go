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

func TestConvertResponsesGroupsParallelToolCallsAndOutputs(t *testing.T) {
	req := openairesp.Request{
		Model: "test-model",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"run checks"}]},
			{"type":"function_call","call_id":"exec_command:1","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},
			{"type":"function_call","call_id":"exec_command:2","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
			{"type":"function_call_output","call_id":"exec_command:1","output":"ok 1"},
			{"type":"function_call_output","call_id":"exec_command:2","output":"ok 2"}
		]`),
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Request.Messages) != 3 {
		t.Fatalf("messages len = %d", len(got.Request.Messages))
	}
	toolUses := got.Request.Messages[1]
	if toolUses.Role != "assistant" || len(toolUses.Content) != 2 {
		t.Fatalf("tool use message = %#v", toolUses)
	}
	if toolUses.Content[0].Type != "tool_use" || toolUses.Content[0].ID != "exec_command:1" {
		t.Fatalf("first tool use = %#v", toolUses.Content[0])
	}
	if toolUses.Content[1].Type != "tool_use" || toolUses.Content[1].ID != "exec_command:2" {
		t.Fatalf("second tool use = %#v", toolUses.Content[1])
	}
	toolResults := got.Request.Messages[2]
	if toolResults.Role != "user" || len(toolResults.Content) != 2 {
		t.Fatalf("tool result message = %#v", toolResults)
	}
	if toolResults.Content[0].Type != "tool_result" || toolResults.Content[0].ToolUseID != "exec_command:1" || toolResults.Content[0].Content != "ok 1" {
		t.Fatalf("first tool result = %#v", toolResults.Content[0])
	}
	if toolResults.Content[1].Type != "tool_result" || toolResults.Content[1].ToolUseID != "exec_command:2" || toolResults.Content[1].Content != "ok 2" {
		t.Fatalf("second tool result = %#v", toolResults.Content[1])
	}
}

func TestConvertResponsesWebSearchTool(t *testing.T) {
	req := openairesp.Request{
		Model: "test-model",
		Input: json.RawMessage(`"what is current news?"`),
		Tools: []json.RawMessage{json.RawMessage(`{
			"type":"web_search",
			"max_uses":3,
			"filters":{"allowed_domains":["docs.example.com"],"blocked_domains":["blocked.example.com"]},
			"user_location":{"type":"approximate","city":"San Francisco"},
			"response_inclusion":"excluded"
		}`)},
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Request.Tools) != 1 {
		t.Fatalf("tools = %#v", got.Request.Tools)
	}
	searchTool := got.Request.Tools[0]
	if searchTool.Name != proxyWebSearchToolName || searchTool.Type != anthropicWebSearchToolType {
		t.Fatalf("search tool = %#v", searchTool)
	}
	if searchTool.MaxUses != 3 || searchTool.ResponseInclusion != "excluded" {
		t.Fatalf("search tool options = %#v", searchTool)
	}
	if string(searchTool.UserLocation) != `{"type":"approximate","city":"San Francisco"}` {
		t.Fatalf("user location = %s", searchTool.UserLocation)
	}
	if len(searchTool.AllowedDomains) != 1 || searchTool.AllowedDomains[0] != "docs.example.com" {
		t.Fatalf("allowed domains = %#v", searchTool.AllowedDomains)
	}
	if len(searchTool.BlockedDomains) != 1 || searchTool.BlockedDomains[0] != "blocked.example.com" {
		t.Fatalf("blocked domains = %#v", searchTool.BlockedDomains)
	}
}

func TestConvertResponsesWebFetchTool(t *testing.T) {
	req := openairesp.Request{
		Model: "test-model",
		Input: json.RawMessage(`"read this url"`),
		Tools: []json.RawMessage{json.RawMessage(`{
			"type":"web_fetch",
			"max_uses":2,
			"allowed_domains":["example.com"],
			"max_content_tokens":2048,
			"citations":{"enabled":true},
			"response_inclusion":"excluded",
			"use_cache":false
		}`)},
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got.Request.Tools) != 1 {
		t.Fatalf("tools = %#v", got.Request.Tools)
	}
	tool := got.Request.Tools[0]
	if tool.Name != proxyWebFetchToolName || tool.Type != anthropicWebFetchToolType {
		t.Fatalf("tool = %#v", tool)
	}
	if tool.MaxUses != 2 || tool.MaxContentTokens != 2048 {
		t.Fatalf("tool options = %#v", tool)
	}
	if tool.ResponseInclusion != "excluded" {
		t.Fatalf("response inclusion = %q", tool.ResponseInclusion)
	}
	if tool.UseCache == nil || *tool.UseCache {
		t.Fatalf("use cache = %#v", tool.UseCache)
	}
	if len(tool.AllowedDomains) != 1 || tool.AllowedDomains[0] != "example.com" {
		t.Fatalf("allowed domains = %#v", tool.AllowedDomains)
	}
	if string(tool.Citations) != `{"enabled":true}` {
		t.Fatalf("citations = %s", tool.Citations)
	}
}

func TestConvertResponsesHostedWebToolChoice(t *testing.T) {
	got := convertResponsesToolChoice(json.RawMessage(`"required"`), true)
	if choice, ok := got.(map[string]any); !ok || choice["type"] != "any" {
		t.Fatalf("required choice = %#v", got)
	}
	got = convertResponsesToolChoice(json.RawMessage(`{"type":"web_fetch"}`), true)
	if choice, ok := got.(map[string]any); !ok || choice["type"] != "tool" || choice["name"] != proxyWebFetchToolName {
		t.Fatalf("web fetch choice = %#v", got)
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
	if got.Tools["mcp__contract__echo"] != (responseToolName{Namespace: "mcp__contract", Name: "echo", Kind: responseToolKindNamespace}) {
		t.Fatalf("tool map = %#v", got.Tools)
	}
}

func TestConvertResponsesToAnthropicMediaStopMetadataAndCustomTool(t *testing.T) {
	req := openairesp.Request{
		Model:     "test-model",
		MaxTokens: intPtr(1234),
		Stop:      json.RawMessage(`["END"]`),
		Metadata:  json.RawMessage(`{"session":"abc"}`),
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"data:image/png;base64,abc123"},{"type":"input_file","file_url":"https://example.com/report.pdf","media_type":"application/pdf"}]},
			{"type":"custom_tool_call","call_id":"call_custom","name":"write_note","input":"{\"looks\":\"json\"}"}
		]`),
		Tools:             []json.RawMessage{json.RawMessage(`{"type":"custom","name":"write_note","description":"Write a note"}`)},
		ToolChoice:        json.RawMessage(`{"type":"custom","name":"write_note"}`),
		ParallelToolCalls: boolPtr(false),
	}
	got, err := convertResponsesToAnthropic(req, "")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.Request.MaxTokens != 1234 {
		t.Fatalf("max tokens = %d", got.Request.MaxTokens)
	}
	if len(got.Request.StopSequences) != 1 || got.Request.StopSequences[0] != "END" {
		t.Fatalf("stop = %#v", got.Request.StopSequences)
	}
	if string(got.Request.Metadata) != `{"session":"abc"}` {
		t.Fatalf("metadata = %s", got.Request.Metadata)
	}
	blocks := got.Request.Messages[0].Content
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[1].Type != "image" || !strings.Contains(string(blocks[1].Source), `"type":"base64"`) {
		t.Fatalf("image block = %#v", blocks[1])
	}
	if blocks[2].Type != "document" || !strings.Contains(string(blocks[2].Source), `"url":"https://example.com/report.pdf"`) {
		t.Fatalf("document block = %#v", blocks[2])
	}
	if len(got.Request.Tools) != 1 || got.Request.Tools[0].Name != "write_note" {
		t.Fatalf("tools = %#v", got.Request.Tools)
	}
	toolUse := got.Request.Messages[1].Content[0]
	if toolUse.Type != "tool_use" || string(toolUse.Input) != `{"input":"{\"looks\":\"json\"}"}` {
		t.Fatalf("custom tool input = %#v", toolUse)
	}
	choice, ok := got.Request.ToolChoice.(map[string]any)
	if !ok || choice["type"] != "tool" || choice["name"] != "write_note" {
		t.Fatalf("tool choice = %#v", got.Request.ToolChoice)
	}
}

func TestConvertAnthropicToResponsesReasoningCustomToolAndIncompleteUsage(t *testing.T) {
	resp := anthropic.Response{
		ID:         "msg_1",
		Model:      "claude-ish",
		StopReason: "max_tokens",
		Content: []anthropic.ContentBlock{
			{Type: "thinking", Thinking: "Need a note."},
			{Type: "tool_use", ID: "toolu_1", Name: "write_note", Input: json.RawMessage(`{"input":"raw note"}`)},
		},
		Usage: &anthropic.Usage{
			InputTokens:              10,
			OutputTokens:             5,
			CacheReadInputTokens:     3,
			CacheCreationInputTokens: 2,
		},
	}
	got := convertAnthropicToResponses(resp, "codex-model", responseToolNameMap{
		"write_note": {Name: "write_note", Kind: responseToolKindCustom},
	})
	if got.Status != "incomplete" || got.IncompleteDetails == nil || got.IncompleteDetails.Reason != "max_output_tokens" {
		t.Fatalf("status = %#v incomplete = %#v", got.Status, got.IncompleteDetails)
	}
	if len(got.Output) != 2 {
		t.Fatalf("output len = %d", len(got.Output))
	}
	if got.Output[0].Type != "reasoning" || got.Output[0].Summary[0].Text != "Need a note." {
		t.Fatalf("reasoning = %#v", got.Output[0])
	}
	if got.Output[1].Type != "custom_tool_call" || got.Output[1].Input != "raw note" {
		t.Fatalf("custom tool = %#v", got.Output[1])
	}
	if got.Usage.InputTokens != 15 || got.Usage.TotalTokens != 20 {
		t.Fatalf("usage = %#v", got.Usage)
	}
	if got.Usage.InputTokensDetails == nil || got.Usage.InputTokensDetails.CachedTokens != 3 {
		t.Fatalf("input details = %#v", got.Usage.InputTokensDetails)
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

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
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

func TestConvertAnthropicToResponsesHostedWebTools(t *testing.T) {
	resp := anthropic.Response{
		ID:    "msg_1",
		Model: "claude-ish",
		Content: []anthropic.ContentBlock{
			{Type: "server_tool_use", ID: "srv_1", Name: proxyWebSearchToolName, Input: json.RawMessage(`{"query":"sandbox0"}`)},
			{Type: "server_tool_use", ID: "srv_2", Name: proxyWebFetchToolName, Input: json.RawMessage(`{"url":"https://sandbox0.ai/docs"}`)},
			{
				Type: "text",
				Text: "Sandbox0 docs explain managed agents.",
				Citations: []anthropic.Citation{{
					Type:      "web_search_result_location",
					URL:       "https://sandbox0.ai/docs",
					Title:     "Sandbox0 docs",
					CitedText: "Sandbox0 docs",
				}},
			},
		},
	}
	got := convertAnthropicToResponses(resp, "codex-model", nil)
	if len(got.Output) != 3 {
		t.Fatalf("output len = %d", len(got.Output))
	}
	searchCall := got.Output[0]
	if searchCall.Type != "web_search_call" || searchCall.ID != "srv_1" {
		t.Fatalf("search call = %#v", searchCall)
	}
	searchAction, ok := searchCall.Action.(map[string]any)
	if !ok || searchAction["type"] != "search" || searchAction["query"] != "sandbox0" {
		t.Fatalf("search action = %#v", searchCall.Action)
	}
	fetchCall := got.Output[1]
	if fetchCall.Type != "web_search_call" || fetchCall.ID != "srv_2" {
		t.Fatalf("fetch call = %#v", fetchCall)
	}
	fetchAction, ok := fetchCall.Action.(map[string]any)
	if !ok || fetchAction["type"] != "open_page" || fetchAction["url"] != "https://sandbox0.ai/docs" {
		t.Fatalf("fetch action = %#v", fetchCall.Action)
	}
	annotations := got.Output[2].Content[0].Annotations
	if len(annotations) != 1 {
		t.Fatalf("annotations = %#v", annotations)
	}
	citation, ok := annotations[0].(map[string]any)
	if !ok ||
		citation["type"] != "url_citation" ||
		citation["url"] != "https://sandbox0.ai/docs" ||
		citation["title"] != "Sandbox0 docs" ||
		citation["start_index"] != 0 ||
		citation["end_index"] != 13 {
		t.Fatalf("citation = %#v", annotations[0])
	}
}

func TestConvertAnthropicToResponsesWebFetchCitationUsesFetchedURL(t *testing.T) {
	documentIndex := 0
	resp := anthropic.Response{
		ID:    "msg_1",
		Model: "claude-ish",
		Content: []anthropic.ContentBlock{
			{Type: "server_tool_use", ID: "srv_1", Name: proxyWebFetchToolName, Input: json.RawMessage(`{"url":"https://example.com/article"}`)},
			{
				Type:      "web_fetch_tool_result",
				ToolUseID: "srv_1",
				Content: map[string]any{
					"type": "web_fetch_result",
					"url":  "https://example.com/article",
					"content": map[string]any{
						"type":  "document",
						"title": "Article Title",
					},
				},
			},
			{
				Type: "text",
				Text: "The article says AI will transform healthcare.",
				Citations: []anthropic.Citation{{
					Type:          "char_location",
					DocumentIndex: &documentIndex,
					DocumentTitle: "Article Title",
					CitedText:     "AI will transform healthcare",
				}},
			},
		},
	}
	got := convertAnthropicToResponses(resp, "codex-model", nil)
	if len(got.Output) != 2 {
		t.Fatalf("output len = %d", len(got.Output))
	}
	annotations := got.Output[1].Content[0].Annotations
	if len(annotations) != 1 {
		t.Fatalf("annotations = %#v", annotations)
	}
	citation, ok := annotations[0].(map[string]any)
	if !ok ||
		citation["url"] != "https://example.com/article" ||
		citation["title"] != "Article Title" ||
		citation["start_index"] != 17 ||
		citation["end_index"] != 45 {
		t.Fatalf("citation = %#v", annotations[0])
	}
}

func TestConvertAnthropicToResponsesIgnoresUnknownServerToolUse(t *testing.T) {
	resp := anthropic.Response{
		ID:    "msg_1",
		Model: "claude-ish",
		Content: []anthropic.ContentBlock{
			{Type: "server_tool_use", ID: "srv_1", Name: "code_execution", Input: json.RawMessage(`{"code":"print(1)"}`)},
		},
	}
	got := convertAnthropicToResponses(resp, "codex-model", nil)
	if len(got.Output) != 0 {
		t.Fatalf("output = %#v", got.Output)
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
