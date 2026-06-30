package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sandbox0-ai/llmproxy/internal/anthropic"
	"github.com/sandbox0-ai/llmproxy/internal/openairesp"
)

type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	Message      anthropic.Response     `json:"message"`
	ContentBlock anthropic.ContentBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthropic.Usage `json:"usage"`
}

type responsesStreamState struct {
	w           http.ResponseWriter
	flusher     http.Flusher
	model       string
	toolNames   responseToolNameMap
	responseID  string
	createdAt   int64
	created     bool
	completed   bool
	stopReason  string
	usage       *openairesp.Usage
	blocks      map[int]*responsesStreamBlock
	outputItems []openairesp.OutputItem
	nextIndex   int
	sequence    int
}

type responsesStreamBlock struct {
	kind         string
	outputIndex  int
	itemID       string
	contentIndex int
	block        anthropic.ContentBlock
	text         strings.Builder
	thinking     strings.Builder
	arguments    strings.Builder
}

func streamAnthropicSSEToResponses(w http.ResponseWriter, r io.Reader, model string, toolNames responseToolNameMap) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	state := &responsesStreamState{
		w:          w,
		flusher:    flusher,
		model:      model,
		toolNames:  toolNames,
		responseID: randomID("resp_"),
		createdAt:  time.Now().Unix(),
		usage:      usageFromAnthropic(nil),
		blocks:     make(map[int]*responsesStreamBlock),
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event anthropicStreamEvent
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		state.handle(event)
		if state.completed {
			break
		}
	}
	if !state.completed {
		state.complete()
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *responsesStreamState) handle(event anthropicStreamEvent) {
	switch event.Type {
	case "message_start":
		if event.Message.ID != "" {
			s.responseID = event.Message.ID
		}
		if event.Message.Model != "" && strings.TrimSpace(s.model) == "" {
			s.model = event.Message.Model
		}
		s.mergeUsage(event.Message.Usage)
		s.ensureStarted()
	case "content_block_start":
		s.startBlock(event.Index, event.ContentBlock)
	case "content_block_delta":
		s.deltaBlock(event.Index, event.Delta.Type, event.Delta.Text, event.Delta.PartialJSON, event.Delta.Thinking)
	case "content_block_stop":
		s.finishBlock(event.Index)
	case "message_delta":
		s.mergeUsage(event.Usage)
		if event.Delta.StopReason != "" {
			s.stopReason = event.Delta.StopReason
		}
	case "message_stop":
		s.complete()
	}
}

func (s *responsesStreamState) ensureStarted() {
	if s.created {
		return
	}
	s.created = true
	s.send("response.created", map[string]any{
		"type":     "response.created",
		"response": s.baseResponse("in_progress", nil),
	})
	s.send("response.in_progress", map[string]any{
		"type":     "response.in_progress",
		"response": s.baseResponse("in_progress", nil),
	})
}

func (s *responsesStreamState) startBlock(index int, block anthropic.ContentBlock) {
	s.ensureStarted()
	if index < 0 {
		index = len(s.blocks)
	}
	kind := block.Type
	if kind == "" {
		kind = "text"
	}
	outputIndex := s.nextOutputIndex()
	itemID := responseItemIDForBlock(kind, s.responseID, outputIndex)
	if kind == "server_tool_use" {
		itemID = firstNonEmpty(block.ID, itemID)
	}
	streamBlock := &responsesStreamBlock{
		kind:        kind,
		outputIndex: outputIndex,
		itemID:      itemID,
		block:       block,
	}
	s.blocks[index] = streamBlock
	switch kind {
	case "text":
		s.startTextBlock(streamBlock)
	case "thinking", "redacted_thinking":
		s.startReasoningBlock(streamBlock)
	case "tool_use":
		s.startToolBlock(streamBlock)
	case "server_tool_use":
		s.startServerToolBlock(streamBlock)
	}
}

func (s *responsesStreamState) deltaBlock(index int, deltaType, text, partialJSON, thinking string) {
	block := s.blocks[index]
	if block == nil {
		if deltaType == "thinking_delta" {
			s.startBlock(index, anthropic.ContentBlock{Type: "thinking"})
		} else {
			s.startBlock(index, anthropic.ContentBlock{Type: "text"})
		}
		block = s.blocks[index]
	}
	switch deltaType {
	case "text_delta":
		block.text.WriteString(text)
		s.send("response.output_text.delta", map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       block.itemID,
			"output_index":  block.outputIndex,
			"content_index": block.contentIndex,
			"delta":         text,
		})
	case "thinking_delta":
		block.thinking.WriteString(thinking)
		s.send("response.reasoning_summary_text.delta", map[string]any{
			"type":          "response.reasoning_summary_text.delta",
			"item_id":       block.itemID,
			"output_index":  block.outputIndex,
			"summary_index": 0,
			"delta":         thinking,
		})
	case "input_json_delta":
		block.arguments.WriteString(partialJSON)
		if shouldStreamFunctionArguments(block.block.Name, s.toolNames) {
			s.send("response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      block.itemID,
				"output_index": block.outputIndex,
				"delta":        partialJSON,
			})
		}
	}
}

func (s *responsesStreamState) finishBlock(index int) {
	block := s.blocks[index]
	if block == nil {
		return
	}
	switch block.kind {
	case "text":
		s.finishTextBlock(block)
	case "thinking", "redacted_thinking":
		s.finishReasoningBlock(block)
	case "tool_use":
		s.finishToolBlock(block)
	case "server_tool_use":
		s.finishServerToolBlock(block)
	}
	delete(s.blocks, index)
}

func (s *responsesStreamState) startTextBlock(block *responsesStreamBlock) {
	s.send("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": block.outputIndex,
		"item": map[string]any{
			"id":      block.itemID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	})
	s.send("response.content_part.added", map[string]any{
		"type":          "response.content_part.added",
		"item_id":       block.itemID,
		"output_index":  block.outputIndex,
		"content_index": block.contentIndex,
		"part": map[string]any{
			"type":        "output_text",
			"text":        "",
			"annotations": []any{},
		},
	})
}

func (s *responsesStreamState) finishTextBlock(block *responsesStreamBlock) {
	text := block.text.String()
	part := openairesp.ContentPart{Type: "output_text", Text: text, Annotations: citationsToOpenAIAnnotations(text, block.block.Citations, nil)}
	item := openairesp.OutputItem{
		ID:      block.itemID,
		Type:    "message",
		Status:  "completed",
		Role:    "assistant",
		Content: []openairesp.ContentPart{part},
	}
	s.send("response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       block.itemID,
		"output_index":  block.outputIndex,
		"content_index": block.contentIndex,
		"text":          text,
	})
	s.send("response.content_part.done", map[string]any{
		"type":          "response.content_part.done",
		"item_id":       block.itemID,
		"output_index":  block.outputIndex,
		"content_index": block.contentIndex,
		"part":          part,
	})
	s.outputItems = append(s.outputItems, item)
	s.send("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": block.outputIndex,
		"item":         item,
	})
}

func (s *responsesStreamState) startReasoningBlock(block *responsesStreamBlock) {
	s.send("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": block.outputIndex,
		"item": map[string]any{
			"id":      block.itemID,
			"type":    "reasoning",
			"status":  "in_progress",
			"summary": []any{},
		},
	})
	s.send("response.reasoning_summary_part.added", map[string]any{
		"type":          "response.reasoning_summary_part.added",
		"item_id":       block.itemID,
		"output_index":  block.outputIndex,
		"summary_index": 0,
		"part": map[string]any{
			"type": "summary_text",
			"text": "",
		},
	})
}

func (s *responsesStreamState) finishReasoningBlock(block *responsesStreamBlock) {
	text := firstNonEmpty(block.thinking.String(), block.block.Thinking, block.block.Text)
	item := openairesp.OutputItem{
		ID:     block.itemID,
		Type:   "reasoning",
		Status: "completed",
		Summary: []openairesp.SummaryPart{{
			Type: "summary_text",
			Text: text,
		}},
	}
	s.send("response.reasoning_summary_text.done", map[string]any{
		"type":          "response.reasoning_summary_text.done",
		"item_id":       block.itemID,
		"output_index":  block.outputIndex,
		"summary_index": 0,
		"text":          text,
	})
	s.send("response.reasoning_summary_part.done", map[string]any{
		"type":          "response.reasoning_summary_part.done",
		"item_id":       block.itemID,
		"output_index":  block.outputIndex,
		"summary_index": 0,
		"part": openairesp.SummaryPart{
			Type: "summary_text",
			Text: text,
		},
	})
	s.outputItems = append(s.outputItems, item)
	s.send("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": block.outputIndex,
		"item":         item,
	})
}

func (s *responsesStreamState) startToolBlock(block *responsesStreamBlock) {
	item := responseToolUseToOutputItem(block.block, "", s.toolNames)
	item.ID = block.itemID
	item.Status = "in_progress"
	s.send("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": block.outputIndex,
		"item":         item,
	})
}

func (s *responsesStreamState) finishToolBlock(block *responsesStreamBlock) {
	args := block.arguments.String()
	if strings.TrimSpace(args) == "" && len(block.block.Input) > 0 && string(block.block.Input) != "null" {
		args = string(block.block.Input)
	}
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	item := responseToolUseToOutputItem(block.block, args, s.toolNames)
	item.ID = block.itemID
	if shouldStreamFunctionArguments(block.block.Name, s.toolNames) {
		s.send("response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      block.itemID,
			"output_index": block.outputIndex,
			"arguments":    args,
		})
	}
	s.outputItems = append(s.outputItems, item)
	s.send("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": block.outputIndex,
		"item":         item,
	})
}

func (s *responsesStreamState) startServerToolBlock(block *responsesStreamBlock) {
	item := openairesp.OutputItem{ID: block.itemID, Type: "web_search_call", Status: "in_progress"}
	s.send("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": block.outputIndex,
		"item":         item,
	})
}

func (s *responsesStreamState) finishServerToolBlock(block *responsesStreamBlock) {
	args := block.arguments.String()
	if strings.TrimSpace(args) != "" {
		block.block.Input = json.RawMessage(args)
	}
	item, ok := convertServerToolUseToResponses(block.block)
	if !ok {
		return
	}
	item.ID = block.itemID
	s.outputItems = append(s.outputItems, item)
	s.send("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": block.outputIndex,
		"item":         item,
	})
}

func (s *responsesStreamState) complete() {
	if s.completed {
		return
	}
	s.ensureStarted()
	for index := range s.blocks {
		s.finishBlock(index)
	}
	status := responsesStatusFromAnthropicStop(s.stopReason)
	response := s.baseResponse(status, s.outputItems)
	if status == "incomplete" {
		response["incomplete_details"] = openairesp.IncompleteDetails{Reason: "max_output_tokens"}
	}
	s.send("response.completed", map[string]any{
		"type":     "response.completed",
		"response": response,
	})
	s.completed = true
}

func (s *responsesStreamState) baseResponse(status string, output []openairesp.OutputItem) map[string]any {
	if strings.TrimSpace(s.model) == "" {
		s.model = "unknown"
	}
	return map[string]any{
		"id":         s.responseID,
		"object":     "response",
		"created_at": s.createdAt,
		"status":     status,
		"model":      s.model,
		"output":     output,
		"usage":      s.usage,
	}
}

func (s *responsesStreamState) nextOutputIndex() int {
	index := s.nextIndex
	s.nextIndex++
	return index
}

func (s *responsesStreamState) send(event string, payload map[string]any) {
	payload["sequence_number"] = s.sequence
	raw, _ := json.Marshal(payload)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, raw)
	s.sequence++
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *responsesStreamState) mergeUsage(usage *anthropic.Usage) {
	if usage == nil {
		return
	}
	if s.usage == nil {
		s.usage = usageFromAnthropic(usage)
		return
	}
	input := usage.TotalInput()
	if input > 0 {
		s.usage.InputTokens = input
		s.usage.CacheReadInputTokens = usage.CacheReadInputTokens
		s.usage.CacheCreationInputTokens = usage.CacheCreationInputTokens
		if usage.CacheReadInputTokens > 0 {
			s.usage.InputTokensDetails = &openairesp.InputTokensDetails{CachedTokens: usage.CacheReadInputTokens}
		}
	}
	if usage.OutputTokens > 0 {
		s.usage.OutputTokens = usage.OutputTokens
	}
	s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
	if s.usage.OutputTokensDetails == nil {
		s.usage.OutputTokensDetails = &openairesp.OutputTokensDetails{ReasoningTokens: 0}
	}
}

func responseItemIDForBlock(kind, responseID string, outputIndex int) string {
	switch kind {
	case "thinking", "redacted_thinking":
		return fmt.Sprintf("rs_%s_%d", responseID, outputIndex)
	case "tool_use":
		return fmt.Sprintf("fc_%s_%d", responseID, outputIndex)
	default:
		return fmt.Sprintf("msg_%s_%d", responseID, outputIndex)
	}
}

func shouldStreamFunctionArguments(name string, toolNames responseToolNameMap) bool {
	tool := responseToolInfo(name, toolNames)
	return tool.Kind != responseToolKindCustom && tool.Kind != responseToolKindToolSearch
}
