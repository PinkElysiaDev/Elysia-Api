package builtin

import "testing"

func TestAnthropicToolStreamPassesResponsesFinalValidation(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Responses)
	convertFixtureStream(t, from, to, []string{
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c","name":"echo","caller":{"type":"direct"},"input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{ \"value\": 7 }"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	})
}
