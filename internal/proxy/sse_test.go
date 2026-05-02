package proxy

import (
	"strings"
	"testing"
)

func TestReadAnthropicStreamTextAndToolUse(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","usage":{"input_tokens":3}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"web_search","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"sandbox0\"}"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	got, err := readAnthropicStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if got.ID != "msg_1" || got.Model != "claude" {
		t.Fatalf("message = %#v", got)
	}
	if len(got.Content) != 2 {
		t.Fatalf("content len = %d", len(got.Content))
	}
	if got.Content[0].Text != "Hello" {
		t.Fatalf("text = %q", got.Content[0].Text)
	}
	if got.Content[1].Name != "web_search" || string(got.Content[1].Input) != `{"query":"sandbox0"}` {
		t.Fatalf("tool block = %#v", got.Content[1])
	}
}
