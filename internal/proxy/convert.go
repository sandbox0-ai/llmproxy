package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sandbox0-ai/llmproxy/internal/anthropic"
	"github.com/sandbox0-ai/llmproxy/internal/openairesp"
)

const (
	proxyWebSearchToolName      = "web_search"
	proxyWebFetchToolName       = "web_fetch"
	anthropicWebSearchToolType  = "web_search_20260318"
	anthropicWebFetchToolType   = "web_fetch_20260318"
	defaultHostedWebToolMaxUses = 5
	responseToolKindNamespace   = "namespace"
	responseToolKindCustom      = "custom"
	responseToolKindToolSearch  = "tool_search"
	customToolInputField        = "input"
)

type convertedRequest struct {
	Request anthropic.Request
	Tools   responseToolNameMap
}

type inputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Namespace string          `json:"namespace"`
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
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		out.MaxTokens = *req.MaxTokens
	}
	out.StopSequences = stopSequencesFromRaw(req.Stop)
	out.Metadata = rawJSONOption(req.Metadata)

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

	tools, err := convertResponsesTools(req.Tools)
	if err != nil {
		return convertedRequest{}, err
	}
	out.Tools = tools.Tools
	out.ToolChoice = convertResponsesToolChoice(req.ToolChoice, tools.Len() > 0)
	return convertedRequest{Request: out, Tools: tools.Names}, nil
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
			if item.Type == "custom_tool_call" {
				args = string(mustRawJSON(map[string]any{customToolInputField: args}))
			} else if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			name := anthropicToolName(item.Namespace, item.Name)
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
			item.Type == "web_fetch_call" ||
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
		case "refusal":
			var text string
			_ = json.Unmarshal(part["refusal"], &text)
			if text != "" {
				blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: text})
			}
		case "input_image":
			if source := anthropicSourceFromInputImage(part); len(source) > 0 {
				blocks = append(blocks, anthropic.ContentBlock{Type: "image", Source: source})
			} else if role != "assistant" {
				blocks = append(blocks, unsupportedContentBlock(part))
			}
		case "input_file":
			if source := anthropicSourceFromInputFile(part); len(source) > 0 {
				blocks = append(blocks, anthropic.ContentBlock{Type: "document", Source: source})
			} else if role != "assistant" {
				blocks = append(blocks, unsupportedContentBlock(part))
			}
		case "input_audio":
			if role != "assistant" {
				blocks = append(blocks, unsupportedContentBlock(part))
			}
		default:
			// Keep unsupported structured content visible rather than silently losing it.
			if role != "assistant" {
				blocks = append(blocks, unsupportedContentBlock(part))
			}
		}
	}
	return blocks
}

type convertedTools struct {
	Tools []anthropic.Tool
	Names responseToolNameMap
}

func (tools convertedTools) Len() int {
	return len(tools.Tools)
}

type responseToolNameMap map[string]responseToolName

type responseToolName struct {
	Namespace string
	Name      string
	Kind      string
}

func convertResponsesTools(rawTools []json.RawMessage) (convertedTools, error) {
	var out convertedTools
	out.Names = make(responseToolNameMap)
	hostedToolIndexes := make(map[string]int)
	appendHostedTool := func(tool anthropic.Tool, replace bool) {
		if idx, ok := hostedToolIndexes[tool.Name]; ok {
			if replace {
				out.Tools[idx] = tool
			}
			return
		}
		hostedToolIndexes[tool.Name] = len(out.Tools)
		out.Tools = append(out.Tools, tool)
	}
	for _, raw := range rawTools {
		var toolName string
		if json.Unmarshal(raw, &toolName) == nil && strings.TrimSpace(toolName) != "" {
			customTool := convertResponsesCustomTool(toolName, "", nil)
			out.Tools = append(out.Tools, customTool)
			out.Names[customTool.Name] = responseToolName{Name: toolName, Kind: responseToolKindCustom}
			continue
		}
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}
		var typ string
		_ = json.Unmarshal(tool["type"], &typ)
		if isResponsesSearchTool(typ) {
			appendHostedTool(convertResponsesWebSearchTool(tool), true)
			continue
		}
		if isResponsesFetchTool(typ) {
			appendHostedTool(convertResponsesWebFetchTool(tool), true)
			continue
		}
		if typ == "namespace" {
			var namespaceName, namespaceDescription string
			_ = json.Unmarshal(tool["name"], &namespaceName)
			_ = json.Unmarshal(tool["description"], &namespaceDescription)
			if namespaceName == "" {
				continue
			}

			var childTools []map[string]json.RawMessage
			if err := json.Unmarshal(tool["tools"], &childTools); err != nil {
				continue
			}
			for _, child := range childTools {
				childTool, ok := convertResponsesFunctionTool(child, namespaceDescription)
				if !ok {
					continue
				}
				childTool.Name = anthropicToolName(namespaceName, childTool.Name)
				if childTool.Name == "" {
					continue
				}
				out.Tools = append(out.Tools, childTool)
				out.Names[childTool.Name] = responseToolName{
					Namespace: namespaceName,
					Name:      childToolName(childTool.Name, namespaceName),
					Kind:      responseToolKindNamespace,
				}
			}
			continue
		}
		if typ == "custom" {
			var name, description string
			_ = json.Unmarshal(tool["name"], &name)
			_ = json.Unmarshal(tool["description"], &description)
			if name == "" {
				continue
			}
			customTool := convertResponsesCustomTool(name, description, tool)
			out.Tools = append(out.Tools, customTool)
			out.Names[customTool.Name] = responseToolName{Name: name, Kind: responseToolKindCustom}
			continue
		}
		if typ == "tool_search" {
			tool := anthropic.Tool{
				Name:        proxyToolSearchToolName(),
				Description: "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`),
			}
			out.Tools = append(out.Tools, tool)
			out.Names[tool.Name] = responseToolName{Name: tool.Name, Kind: responseToolKindToolSearch}
			continue
		}
		if typ != "function" {
			continue
		}
		functionTool, ok := convertResponsesFunctionTool(tool, "")
		if !ok {
			continue
		}
		out.Tools = append(out.Tools, functionTool)
	}
	return out, nil
}

func convertResponsesWebSearchTool(tool map[string]json.RawMessage) anthropic.Tool {
	out := anthropic.Tool{
		Type:    anthropicWebSearchToolType,
		Name:    proxyWebSearchToolName,
		MaxUses: hostedWebToolMaxUses(tool),
	}
	applyHostedWebDomains(&out, tool)
	out.UserLocation = rawJSONOption(tool["user_location"])
	out.ResponseInclusion = stringFromRaw(tool["response_inclusion"])
	return out
}

func convertResponsesWebFetchTool(tool map[string]json.RawMessage) anthropic.Tool {
	out := anthropic.Tool{
		Type:    anthropicWebFetchToolType,
		Name:    proxyWebFetchToolName,
		MaxUses: hostedWebToolMaxUses(tool),
	}
	applyHostedWebDomains(&out, tool)
	out.Citations = rawJSONOption(tool["citations"])
	out.MaxContentTokens = intFromRaw(tool["max_content_tokens"])
	out.ResponseInclusion = stringFromRaw(tool["response_inclusion"])
	out.UseCache = boolPtrFromRaw(tool["use_cache"])
	return out
}

func applyHostedWebDomains(out *anthropic.Tool, tool map[string]json.RawMessage) {
	allowed := stringSliceFromRaw(tool["allowed_domains"])
	blocked := stringSliceFromRaw(tool["blocked_domains"])
	var filters struct {
		AllowedDomains []string `json:"allowed_domains"`
		BlockedDomains []string `json:"blocked_domains"`
	}
	if raw := rawJSONOption(tool["filters"]); len(raw) > 0 && json.Unmarshal(raw, &filters) == nil {
		if len(filters.AllowedDomains) > 0 {
			allowed = cleanStringSlice(filters.AllowedDomains)
		}
		if len(filters.BlockedDomains) > 0 {
			blocked = cleanStringSlice(filters.BlockedDomains)
		}
	}
	out.AllowedDomains = allowed
	out.BlockedDomains = blocked
}

func convertResponsesFunctionTool(tool map[string]json.RawMessage, namespaceDescription string) (anthropic.Tool, bool) {
	var typ string
	_ = json.Unmarshal(tool["type"], &typ)
	if typ != "" && typ != "function" {
		return anthropic.Tool{}, false
	}
	var name, description string
	_ = json.Unmarshal(tool["name"], &name)
	_ = json.Unmarshal(tool["description"], &description)
	if name == "" {
		return anthropic.Tool{}, false
	}
	schema := tool["parameters"]
	if len(schema) == 0 || string(schema) == "null" {
		schema = tool["input_schema"]
	}
	if len(schema) == 0 || string(schema) == "null" {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if namespaceDescription != "" {
		description = strings.TrimSpace(strings.Join([]string{namespaceDescription, description}, "\n\n"))
	}
	return anthropic.Tool{Name: name, Description: description, InputSchema: schema}, true
}

func convertResponsesCustomTool(name, description string, raw map[string]json.RawMessage) anthropic.Tool {
	if strings.TrimSpace(description) == "" && raw != nil {
		description = stringFromRaw(raw["description"])
	}
	return anthropic.Tool{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"Raw string input for the original custom tool."}},"required":["input"]}`),
	}
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
		case "required":
			return map[string]any{"type": "any"}
		}
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		if typ, _ := m["type"].(string); typ == "function" || typ == "custom" {
			if name, _ := m["name"].(string); name != "" {
				return map[string]any{"type": "tool", "name": name}
			}
		} else if isResponsesSearchTool(typ) {
			return map[string]any{"type": "tool", "name": proxyWebSearchToolName}
		} else if isResponsesFetchTool(typ) {
			return map[string]any{"type": "tool", "name": proxyWebFetchToolName}
		} else if typ == "tool_search" {
			return map[string]any{"type": "tool", "name": proxyToolSearchToolName()}
		}
	}
	return nil
}

func convertAnthropicToResponses(resp anthropic.Response, model string, toolNames responseToolNameMap) openairesp.Response {
	output := make([]openairesp.OutputItem, 0, len(resp.Content))
	fetchDocuments := make([]fetchedDocument, 0)
	for _, block := range resp.Content {
		switch block.Type {
		case "thinking", "redacted_thinking":
			thinking := strings.TrimSpace(block.Thinking)
			if thinking == "" {
				thinking = strings.TrimSpace(block.Text)
			}
			if thinking == "" {
				continue
			}
			output = append(output, openairesp.OutputItem{
				ID:     randomID("rs_"),
				Type:   "reasoning",
				Status: "completed",
				Summary: []openairesp.SummaryPart{{
					Type: "summary_text",
					Text: thinking,
				}},
			})
		case "text":
			if block.Text == "" {
				continue
			}
			output = append(output, openairesp.OutputItem{
				ID:     randomID("msg_"),
				Type:   "message",
				Status: "completed",
				Role:   "assistant",
				Content: []openairesp.ContentPart{{
					Type:        "output_text",
					Text:        block.Text,
					Annotations: citationsToOpenAIAnnotations(block.Text, block.Citations, fetchDocuments),
				}},
			})
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 && string(block.Input) != "null" {
				args = string(block.Input)
			}
			output = append(output, responseToolUseToOutputItem(block, args, toolNames))
		case "server_tool_use":
			item, ok := convertServerToolUseToResponses(block)
			if ok {
				output = append(output, item)
			}
		case "web_fetch_tool_result":
			fetchDocuments = appendFetchDocuments(fetchDocuments, block.Content)
			continue
		case "web_search_tool_result":
			continue
		}
	}
	out := openairesp.NewResponse(firstNonEmpty(resp.ID, randomID("resp_")), firstNonEmpty(model, resp.Model), output, usageFromAnthropic(resp.Usage))
	out.Status = responsesStatusFromAnthropicStop(resp.StopReason)
	if resp.StopReason == "max_tokens" {
		out.IncompleteDetails = &openairesp.IncompleteDetails{Reason: "max_output_tokens"}
	}
	return out
}

func responseToolUseToOutputItem(block anthropic.ContentBlock, args string, toolNames responseToolNameMap) openairesp.OutputItem {
	callID := firstNonEmpty(block.ID, randomID("call_"))
	tool := responseToolInfo(block.Name, toolNames)
	switch tool.Kind {
	case responseToolKindCustom:
		return openairesp.OutputItem{
			ID:     randomID("ctc_"),
			Type:   "custom_tool_call",
			Status: "completed",
			CallID: callID,
			Name:   tool.Name,
			Input:  customToolInputFromArguments(args),
		}
	case responseToolKindToolSearch:
		return openairesp.OutputItem{
			ID:        randomID("ts_"),
			Type:      "tool_search_call",
			Status:    "completed",
			CallID:    callID,
			Arguments: args,
		}
	default:
		return openairesp.OutputItem{
			ID:        randomID("fc_"),
			Type:      "function_call",
			Status:    "completed",
			CallID:    callID,
			Namespace: tool.Namespace,
			Name:      tool.Name,
			Arguments: args,
		}
	}
}

func responseToolInfo(name string, toolNames responseToolNameMap) responseToolName {
	if mapped, ok := toolNames[name]; ok {
		if mapped.Name == "" {
			mapped.Name = name
		}
		return mapped
	}
	return responseToolName{Name: name}
}

func customToolInputFromArguments(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(args), &payload) == nil {
		if input, ok := payload[customToolInputField].(string); ok {
			return input
		}
	}
	return args
}

func usageFromAnthropic(usage *anthropic.Usage) *openairesp.Usage {
	if usage == nil {
		return &openairesp.Usage{
			OutputTokensDetails: &openairesp.OutputTokensDetails{ReasoningTokens: 0},
		}
	}
	input := usage.TotalInput()
	out := &openairesp.Usage{
		InputTokens:              input,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              input + usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		OutputTokensDetails:      &openairesp.OutputTokensDetails{ReasoningTokens: 0},
	}
	if usage.CacheReadInputTokens > 0 {
		out.InputTokensDetails = &openairesp.InputTokensDetails{
			CachedTokens: usage.CacheReadInputTokens,
		}
	}
	return out
}

func responsesStatusFromAnthropicStop(stopReason string) string {
	switch stopReason {
	case "max_tokens":
		return "incomplete"
	default:
		return "completed"
	}
}

func convertServerToolUseToResponses(block anthropic.ContentBlock) (openairesp.OutputItem, bool) {
	switch block.Name {
	case proxyWebSearchToolName:
		item := openairesp.OutputItem{
			ID:     firstNonEmpty(block.ID, randomID("ws_")),
			Type:   "web_search_call",
			Status: "completed",
		}
		if query := stringFieldFromJSON(block.Input, "query"); query != "" {
			item.Action = map[string]any{"type": "search", "query": query}
		}
		return item, true
	case proxyWebFetchToolName:
		item := openairesp.OutputItem{
			ID:     firstNonEmpty(block.ID, randomID("ws_")),
			Type:   "web_search_call",
			Status: "completed",
		}
		if url := stringFieldFromJSON(block.Input, "url"); url != "" {
			item.Action = map[string]any{"type": "open_page", "url": url}
		}
		return item, true
	default:
		return openairesp.OutputItem{}, false
	}
}

func anthropicToolName(namespace, name string) string {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "" {
		return name
	}
	if name == "" {
		return ""
	}
	return strings.TrimRight(namespace, "_") + "__" + strings.TrimLeft(name, "_")
}

func childToolName(flatName, namespace string) string {
	prefix := strings.TrimRight(namespace, "_") + "__"
	return strings.TrimPrefix(flatName, prefix)
}

func stringFieldFromJSON(raw json.RawMessage, field string) string {
	var payload struct {
		Query string `json:"query"`
		URL   string `json:"url"`
	}
	_ = json.Unmarshal(raw, &payload)
	switch field {
	case "query":
		return payload.Query
	case "url":
		return payload.URL
	default:
		return ""
	}
}

func isResponsesSearchTool(typ string) bool {
	switch typ {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11":
		return true
	default:
		return false
	}
}

func isResponsesFetchTool(typ string) bool {
	switch typ {
	case "web_fetch", "web_fetch_preview":
		return true
	default:
		return false
	}
}

type fetchedDocument struct {
	URL   string
	Title string
}

func appendFetchDocuments(docs []fetchedDocument, content any) []fetchedDocument {
	switch value := content.(type) {
	case []any:
		for _, item := range value {
			if doc, ok := fetchedDocumentFromAny(item); ok {
				docs = append(docs, doc)
			}
		}
	case map[string]any:
		if doc, ok := fetchedDocumentFromMap(value); ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

func fetchedDocumentFromAny(value any) (fetchedDocument, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return fetchedDocument{}, false
	}
	return fetchedDocumentFromMap(m)
}

func fetchedDocumentFromMap(value map[string]any) (fetchedDocument, bool) {
	url := stringFromAny(value["url"])
	title := stringFromAny(value["title"])
	if content, ok := value["content"].(map[string]any); ok && title == "" {
		title = stringFromAny(content["title"])
	}
	if url == "" {
		return fetchedDocument{}, false
	}
	return fetchedDocument{URL: url, Title: title}, true
}

func citationsToOpenAIAnnotations(text string, citations []anthropic.Citation, fetchDocuments []fetchedDocument) []any {
	annotations := make([]any, 0, len(citations))
	for _, citation := range citations {
		url, title := citationURLAndTitle(citation, fetchDocuments)
		if strings.TrimSpace(url) == "" {
			continue
		}
		start, end := citationSpan(text, citation.CitedText)
		annotation := map[string]any{
			"type":        "url_citation",
			"start_index": start,
			"end_index":   end,
			"url":         url,
			"title":       title,
		}
		annotations = append(annotations, annotation)
	}
	if annotations == nil {
		return []any{}
	}
	return annotations
}

func citationURLAndTitle(citation anthropic.Citation, fetchDocuments []fetchedDocument) (string, string) {
	url := citation.URL
	title := firstNonEmpty(citation.Title, citation.DocumentTitle)
	if url == "" && citation.DocumentIndex != nil {
		idx := *citation.DocumentIndex
		if idx >= 0 && idx < len(fetchDocuments) {
			doc := fetchDocuments[idx]
			url = doc.URL
			title = firstNonEmpty(title, doc.Title)
		}
	}
	return url, title
}

func citationSpan(text, citedText string) (int, int) {
	textLen := utf8.RuneCountInString(text)
	if textLen == 0 {
		return 0, 0
	}
	if strings.TrimSpace(citedText) == "" {
		return 0, textLen
	}
	idx := strings.Index(text, citedText)
	if idx < 0 {
		return 0, textLen
	}
	start := utf8.RuneCountInString(text[:idx])
	return start, start + utf8.RuneCountInString(citedText)
}

func hostedWebToolMaxUses(tool map[string]json.RawMessage) int {
	for _, key := range []string{"max_uses", "max_tool_calls"} {
		if value := intFromRaw(tool[key]); value > 0 {
			return value
		}
	}
	return defaultHostedWebToolMaxUses
}

func intFromRaw(raw json.RawMessage) int {
	var value int
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return value
}

func stringFromRaw(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func boolPtrFromRaw(raw json.RawMessage) *bool {
	var value bool
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

func stringFromAny(value any) string {
	s, _ := value.(string)
	return s
}

func stringSliceFromRaw(raw json.RawMessage) []string {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return nil
	}
	return cleanStringSlice(values)
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func rawJSONOption(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func stopSequencesFromRaw(raw json.RawMessage) []string {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if strings.TrimSpace(single) == "" {
			return nil
		}
		return []string{single}
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	return cleanStringSlice(many)
}

func unsupportedContentBlock(part map[string]json.RawMessage) anthropic.ContentBlock {
	if b, err := json.Marshal(part); err == nil {
		return anthropic.ContentBlock{Type: "text", Text: string(b)}
	}
	return anthropic.ContentBlock{Type: "text", Text: "[Unsupported content]"}
}

func anthropicSourceFromInputImage(part map[string]json.RawMessage) json.RawMessage {
	raw := part["image_url"]
	if len(raw) == 0 || string(raw) == "null" {
		raw = part["url"]
	}
	urlValue := imageURLFromRaw(raw)
	if urlValue == "" {
		return nil
	}
	return anthropicSourceFromURL(urlValue, "image/png")
}

func anthropicSourceFromInputFile(part map[string]json.RawMessage) json.RawMessage {
	for _, key := range []string{"file_data", "file_url", "url"} {
		if value := imageURLFromRaw(part[key]); value != "" {
			mediaType := firstNonEmpty(
				stringFromRaw(part["media_type"]),
				stringFromRaw(part["mime_type"]),
				"application/octet-stream",
			)
			return anthropicSourceFromURL(value, mediaType)
		}
	}
	return nil
}

func imageURLFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var urlString string
	if json.Unmarshal(raw, &urlString) == nil {
		return strings.TrimSpace(urlString)
	}
	var payload struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		return strings.TrimSpace(payload.URL)
	}
	return ""
}

func anthropicSourceFromURL(value, defaultMediaType string) json.RawMessage {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if mediaType, data, ok := parseDataURL(value); ok {
		return mustRawJSON(map[string]any{
			"type":       "base64",
			"media_type": firstNonEmpty(mediaType, defaultMediaType),
			"data":       data,
		})
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return mustRawJSON(map[string]any{
			"type": "url",
			"url":  value,
		})
	}
	return nil
}

func parseDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	meta, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok || !strings.Contains(meta, ";base64") {
		return "", "", false
	}
	mediaType := strings.TrimSuffix(meta, ";base64")
	return mediaType, data, true
}

func mustRawJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
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

func proxyToolSearchToolName() string {
	return "tool_search"
}
