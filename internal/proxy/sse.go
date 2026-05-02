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

func readAnthropicStream(r io.Reader) (anthropic.Response, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out anthropic.Response
	var blocks []anthropic.ContentBlock
	var current *anthropic.ContentBlock
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		var typ string
		_ = json.Unmarshal(event["type"], &typ)
		switch typ {
		case "message_start":
			var payload struct {
				Message anthropic.Response `json:"message"`
			}
			if json.Unmarshal([]byte(data), &payload) == nil {
				out.ID = payload.Message.ID
				out.Type = payload.Message.Type
				out.Role = payload.Message.Role
				out.Model = payload.Message.Model
				out.Usage = payload.Message.Usage
			}
		case "content_block_start":
			var payload struct {
				ContentBlock anthropic.ContentBlock `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &payload) == nil {
				block := payload.ContentBlock
				blocks = append(blocks, block)
				current = &blocks[len(blocks)-1]
			}
		case "content_block_delta":
			if current == nil {
				continue
			}
			var payload struct {
				Delta struct {
					Type        string          `json:"type"`
					Text        string          `json:"text"`
					PartialJSON string          `json:"partial_json"`
					Thinking    string          `json:"thinking"`
					Input       json.RawMessage `json:"input"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &payload) != nil {
				continue
			}
			switch payload.Delta.Type {
			case "text_delta":
				current.Text += payload.Delta.Text
			case "input_json_delta":
				if string(current.Input) == "{}" {
					current.Input = nil
				}
				current.Input = append(current.Input, []byte(payload.Delta.PartialJSON)...)
			}
		case "message_delta":
			var payload struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage *anthropic.Usage `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &payload) == nil {
				out.StopReason = payload.Delta.StopReason
				if payload.Usage != nil {
					if out.Usage == nil {
						out.Usage = payload.Usage
					} else {
						out.Usage.OutputTokens = payload.Usage.OutputTokens
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return anthropic.Response{}, err
	}
	out.Content = blocks
	return out, nil
}

func (h *Handler) streamAnthropicToResponses(w http.ResponseWriter, model string, resp anthropic.Response) {
	streamFinalResponse(w, convertAnthropicToResponses(resp, model))
}

func streamFinalResponse(w http.ResponseWriter, resp openairesp.Response) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	seq := 0
	send := func(event string, payload any) {
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		seq++
		if flusher != nil {
			flusher.Flush()
		}
	}
	responseSkeleton := resp
	responseSkeleton.Output = nil
	send("response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": seq,
		"response":        responseSkeleton,
	})
	for outputIndex, item := range resp.Output {
		send("response.output_item.added", map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": seq,
			"output_index":    outputIndex,
			"item":            item,
		})
		if item.Type == "message" {
			for contentIndex, part := range item.Content {
				send("response.content_part.added", map[string]any{
					"type":            "response.content_part.added",
					"sequence_number": seq,
					"output_index":    outputIndex,
					"content_index":   contentIndex,
					"part":            map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
				})
				if part.Text != "" {
					send("response.output_text.delta", map[string]any{
						"type":            "response.output_text.delta",
						"sequence_number": seq,
						"output_index":    outputIndex,
						"content_index":   contentIndex,
						"delta":           part.Text,
					})
				}
				send("response.output_text.done", map[string]any{
					"type":            "response.output_text.done",
					"sequence_number": seq,
					"output_index":    outputIndex,
					"content_index":   contentIndex,
					"text":            part.Text,
				})
				send("response.content_part.done", map[string]any{
					"type":            "response.content_part.done",
					"sequence_number": seq,
					"output_index":    outputIndex,
					"content_index":   contentIndex,
					"part":            part,
				})
			}
		}
		if item.Type == "function_call" {
			if item.Arguments != "" {
				send("response.function_call_arguments.delta", map[string]any{
					"type":            "response.function_call_arguments.delta",
					"sequence_number": seq,
					"output_index":    outputIndex,
					"delta":           item.Arguments,
				})
			}
			send("response.function_call_arguments.done", map[string]any{
				"type":            "response.function_call_arguments.done",
				"sequence_number": seq,
				"output_index":    outputIndex,
				"arguments":       item.Arguments,
			})
		}
		send("response.output_item.done", map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": seq,
			"output_index":    outputIndex,
			"item":            item,
		})
	}
	resp.CreatedAt = time.Now().Unix()
	send("response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": seq,
		"response":        resp,
	})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
