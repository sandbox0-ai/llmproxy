package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sandbox0-ai/llmproxy/internal/anthropic"
	"github.com/sandbox0-ai/llmproxy/internal/openairesp"
)

const proxyWebSearchToolName = "web_search"

type convertedRequest struct {
	Request       anthropic.Request
	UsesWebSearch bool
}

type inputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
	Action    json.RawMessage `json:"action"`
	Status    string          `json:"status"`
}

func convertResponsesToAnthropic(req openairesp.Request, upstreamModel string) (convertedRequest, error) {
	if upstreamModel == "" {
		upstreamModel = req.Model
	}
	out := anthropic.Request{
		Model:       upstreamModel,
		MaxTokens:   4096,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		out.MaxTokens = *req.MaxOutputTokens
	}

	systemParts := make([]string, 0, 2)
	if strings.TrimSpace(req.Instructions) != "" {
		systemParts = append(systemParts, req.Instructions)
	}

	messages, extraSystem, err := convertResponsesInput(req.Input)
	if err != nil {
		return convertedRequest{}, err
	}
	systemParts = append(systemParts, extraSystem...)
	out.Messages = messages
	if len(systemParts) > 0 {
		out.System = strings.Join(systemParts, "\n\n")
	}

	tools, usesSearch, err := convertResponsesTools(req.Tools)
	if err != nil {
		return convertedRequest{}, err
	}
	out.Tools = tools
	out.ToolChoice = convertResponsesToolChoice(req.ToolChoice, len(tools) > 0)
	return convertedRequest{Request: out, UsesWebSearch: usesSearch}, nil
}

func convertResponsesInput(input json.RawMessage) ([]anthropic.Message, []string, error) {
	if len(input) == 0 || string(input) == "null" {
		return nil, nil, fmt.Errorf("input is required")
	}

	var inputString string
	if json.Unmarshal(input, &inputString) == nil {
		return []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: inputString}}}}, nil, nil
	}

	var raws []json.RawMessage
	if err := json.Unmarshal(input, &raws); err != nil {
		return nil, nil, fmt.Errorf("input must be a string or array")
	}

	var messages []anthropic.Message
	var systemParts []string
	for _, raw := range raws {
		var item inputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch {
		case item.Type == "message" || item.Role != "":
			role := item.Role
			if role == "" {
				role = "user"
			}
			blocks := responsesContentToAnthropicBlocks(item.Content, role)
			if role == "developer" || role == "system" {
				systemParts = append(systemParts, textFromBlocks(blocks))
				continue
			}
			if role != "assistant" {
				role = "user"
			}
			if len(blocks) > 0 {
				messages = append(messages, anthropic.Message{Role: role, Content: blocks})
			}
		case item.Type == "function_call" || item.Type == "custom_tool_call" || item.Type == "local_shell_call":
			args := item.Arguments
			if args == "" {
				args = item.Input
			}
			if args == "" && len(item.Action) > 0 && string(item.Action) != "null" {
				args = string(item.Action)
			}
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			name := item.Name
			if name == "" && item.Type == "local_shell_call" {
				name = "shell"
			}
			messages = append(messages, anthropic.Message{
				Role: "assistant",
				Content: []anthropic.ContentBlock{{
					Type:  "tool_use",
					ID:    firstNonEmpty(item.CallID, item.ID, randomID("toolu_")),
					Name:  name,
					Input: json.RawMessage(args),
				}},
			})
		case strings.HasSuffix(item.Type, "_output"):
			messages = append(messages, anthropic.Message{
				Role: "user",
				Content: []anthropic.ContentBlock{{
					Type:      "tool_result",
					ToolUseID: item.CallID,
					Content:   toolOutputText(item.Output),
				}},
			})
		case item.Type == "reasoning" || item.Type == "compaction" || item.Type == "web_search_call" ||
			item.Type == "tool_search_call" || item.Type == "tool_search_output" || item.Type == "mcp_list_tools":
			continue
		}
	}
	if len(messages) == 0 {
		return nil, nil, fmt.Errorf("no supported input items")
	}
	return messages, systemParts, nil
}

func responsesContentToAnthropicBlocks(raw json.RawMessage, role string) []anthropic.ContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []anthropic.ContentBlock{{Type: "text", Text: s}}
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return []anthropic.ContentBlock{{Type: "text", Text: string(raw)}}
	}
	var blocks []anthropic.ContentBlock
	for _, part := range parts {
		var typ string
		_ = json.Unmarshal(part["type"], &typ)
		switch typ {
		case "input_text", "output_text", "text":
			var text string
			_ = json.Unmarshal(part["text"], &text)
			if text != "" {
				blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: text})
			}
		default:
			// Keep unsupported structured content visible rather than silently losing it.
			if role != "assistant" {
				if b, err := json.Marshal(part); err == nil {
					blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: string(b)})
				}
			}
		}
	}
	return blocks
}

func convertResponsesTools(rawTools []json.RawMessage) ([]anthropic.Tool, bool, error) {
	var tools []anthropic.Tool
	usesSearch := false
	for _, raw := range rawTools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}
		var typ string
		_ = json.Unmarshal(tool["type"], &typ)
		if isResponsesSearchTool(typ) {
			usesSearch = true
			tools = append(tools, anthropic.Tool{
				Name:        proxyWebSearchToolName,
				Description: "Search the web for current information.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`),
			})
			continue
		}
		if typ != "function" {
			continue
		}
		var name, description string
		_ = json.Unmarshal(tool["name"], &name)
		_ = json.Unmarshal(tool["description"], &description)
		schema := tool["parameters"]
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if name == "" {
			continue
		}
		tools = append(tools, anthropic.Tool{Name: name, Description: description, InputSchema: schema})
	}
	return tools, usesSearch, nil
}

func convertResponsesToolChoice(raw json.RawMessage, hasTools bool) any {
	if !hasTools || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto", "any", "none":
			return map[string]any{"type": s}
		}
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		if typ, _ := m["type"].(string); typ == "function" {
			if name, _ := m["name"].(string); name != "" {
				return map[string]any{"type": "tool", "name": name}
			}
		}
	}
	return nil
}

func convertAnthropicToResponses(resp anthropic.Response, model string) openairesp.Response {
	output := make([]openairesp.OutputItem, 0, len(resp.Content))
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			output = append(output, openairesp.OutputItem{
				ID:      randomID("msg_"),
				Type:    "message",
				Status:  "completed",
				Role:    "assistant",
				Content: []openairesp.ContentPart{{Type: "output_text", Text: block.Text, Annotations: []any{}}},
			})
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 && string(block.Input) != "null" {
				args = string(block.Input)
			}
			output = append(output, openairesp.OutputItem{
				ID:        randomID("fc_"),
				Type:      "function_call",
				Status:    "completed",
				CallID:    firstNonEmpty(block.ID, randomID("call_")),
				Name:      block.Name,
				Arguments: args,
			})
		}
	}
	usage := (*openairesp.Usage)(nil)
	if resp.Usage != nil {
		input := resp.Usage.TotalInput()
		outputTokens := resp.Usage.OutputTokens
		usage = &openairesp.Usage{InputTokens: input, OutputTokens: outputTokens, TotalTokens: input + outputTokens}
	}
	return openairesp.NewResponse(firstNonEmpty(resp.ID, randomID("resp_")), firstNonEmpty(model, resp.Model), output, usage)
}

func isResponsesSearchTool(typ string) bool {
	switch typ {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11":
		return true
	default:
		return false
	}
}

func textFromBlocks(blocks []anthropic.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if content, ok := obj["content"].(string); ok {
			return content
		}
	}
	return string(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
