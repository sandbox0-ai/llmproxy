package anthropic

import "encoding/json"

type Request struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	Messages      []Message       `json:"messages"`
	System        any             `json:"system,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    any             `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
}

type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Citations []Citation      `json:"citations,omitempty"`
}

type Citation struct {
	Type           string `json:"type,omitempty"`
	URL            string `json:"url,omitempty"`
	Title          string `json:"title,omitempty"`
	CitedText      string `json:"cited_text,omitempty"`
	EncryptedIndex string `json:"encrypted_index,omitempty"`
	DocumentIndex  *int   `json:"document_index,omitempty"`
	DocumentTitle  string `json:"document_title,omitempty"`
}

type Tool struct {
	Type              string          `json:"type,omitempty"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	InputSchema       json.RawMessage `json:"input_schema,omitempty"`
	MaxUses           int             `json:"max_uses,omitempty"`
	AllowedDomains    []string        `json:"allowed_domains,omitempty"`
	BlockedDomains    []string        `json:"blocked_domains,omitempty"`
	UserLocation      json.RawMessage `json:"user_location,omitempty"`
	ResponseInclusion string          `json:"response_inclusion,omitempty"`
	MaxContentTokens  int             `json:"max_content_tokens,omitempty"`
	Citations         json.RawMessage `json:"citations,omitempty"`
	UseCache          *bool           `json:"use_cache,omitempty"`
}

type Response struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence string         `json:"stop_sequence,omitempty"`
	Usage        *Usage         `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens              int            `json:"input_tokens,omitempty"`
	OutputTokens             int            `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int            `json:"cache_read_input_tokens,omitempty"`
	ServerToolUse            map[string]int `json:"server_tool_use,omitempty"`
}

func (u *Usage) TotalInput() int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}
