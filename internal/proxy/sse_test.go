package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamAnthropicSSEToResponsesStreamsIncrementalEvents(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","model":"claude","usage":{"input_tokens":3}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	streamAnthropicSSEToResponses(rec, strings.NewReader(stream), "codex-model", nil)
	body := rec.Body.String()

	for _, want := range []string{
		`event: response.created`,
		`event: response.in_progress`,
		`event: response.output_text.delta`,
		`"delta":"Hel"`,
		`"delta":"lo"`,
		`event: response.function_call_arguments.delta`,
		`"item_id":"fc_msg_stream_1"`,
		`"delta":"{\"city\":\"Paris\"}"`,
		`event: response.function_call_arguments.done`,
		`"arguments":"{\"city\":\"Paris\"}"`,
		`"call_id":"toolu_1"`,
		`event: response.completed`,
		`"input_tokens":3`,
		`"output_tokens":7`,
		`"total_tokens":10`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream body missing %s:\n%s", want, body)
		}
	}

	if strings.Index(body, `"delta":"Hel"`) > strings.Index(body, `event: response.completed`) {
		t.Fatalf("text delta was not streamed before completion:\n%s", body)
	}
	if strings.Index(body, `event: response.function_call_arguments.delta`) > strings.Index(body, `event: response.completed`) {
		t.Fatalf("tool arguments were not streamed before completion:\n%s", body)
	}
}
