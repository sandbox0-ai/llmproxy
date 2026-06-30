package openairesp

import (
	"encoding/json"
	"time"
)

type Request struct {
	Model             string            `json:"model"`
	Input             json.RawMessage   `json:"input"`
	Instructions      string            `json:"instructions,omitempty"`
	Tools             []json.RawMessage `json:"tools,omitempty"`
	ToolChoice        json.RawMessage   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens   *int              `json:"max_output_tokens,omitempty"`
	MaxTokens         *int              `json:"max_tokens,omitempty"`
	Temperature       *float64          `json:"temperature,omitempty"`
	TopP              *float64          `json:"top_p,omitempty"`
	Stop              json.RawMessage   `json:"stop,omitempty"`
	Stream            bool              `json:"stream,omitempty"`
	Reasoning         json.RawMessage   `json:"reasoning,omitempty"`
	Text              json.RawMessage   `json:"text,omitempty"`
	User              string            `json:"user,omitempty"`
	Metadata          json.RawMessage   `json:"metadata,omitempty"`
}

type Response struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	CreatedAt         int64              `json:"created_at"`
	Status            string             `json:"status"`
	Model             string             `json:"model"`
	Output            []OutputItem       `json:"output"`
	Usage             *Usage             `json:"usage,omitempty"`
	Error             any                `json:"error,omitempty"`
	IncompleteDetails *IncompleteDetails `json:"incomplete_details,omitempty"`
}

type Usage struct {
	InputTokens              int                  `json:"input_tokens"`
	OutputTokens             int                  `json:"output_tokens"`
	TotalTokens              int                  `json:"total_tokens"`
	InputTokensDetails       *InputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails      *OutputTokensDetails `json:"output_tokens_details,omitempty"`
	CacheReadInputTokens     int                  `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int                  `json:"cache_creation_input_tokens,omitempty"`
}

type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type IncompleteDetails struct {
	Reason string `json:"reason"`
}

type OutputItem struct {
	ID        string         `json:"id,omitempty"`
	Type      string         `json:"type"`
	Status    string         `json:"status,omitempty"`
	Role      string         `json:"role,omitempty"`
	Content   []ContentPart  `json:"content,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
	Input     string         `json:"input,omitempty"`
	Action    any            `json:"action,omitempty"`
	Summary   []SummaryPart  `json:"summary,omitempty"`
	Error     map[string]any `json:"error,omitempty"`
}

type ContentPart struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Refusal     string `json:"refusal,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

type SummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewResponse(id, model string, output []OutputItem, usage *Usage) Response {
	return Response{
		ID:        id,
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Status:    "completed",
		Model:     model,
		Output:    output,
		Usage:     usage,
	}
}
