package relay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func wireAdapterConfig(name string) CustomProtocolConfig {
	return CustomProtocolConfig{ID: "arbitrary-id-" + name, Request: CustomProtocolRequest{PathTemplate: "/generate", BodyTemplate: `{}`},
		Response: CustomProtocolResponse{Adapter: name, Stream: &CustomProtocolStreamMapping{Adapter: name}}}
}

func TestWireAdaptersPreserveNativeResponseExtensions(t *testing.T) {
	fixtures := map[string]string{
		"openai-chat": `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello","native_message":null},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15},"native_number":900719925474099312345}`,
		"anthropic":   `{"id":"r","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"hello","native_block":false}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":3},"native_number":900719925474099312345}`,
		"gemini":      `{"modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello","native_part":[]}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3,"totalTokenCount":15},"native_number":900719925474099312345}`,
		"responses":   `{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","native_part":{}}]}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15},"native_number":900719925474099312345}`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			adapter, err := findWireAdapter(name)
			if err != nil {
				t.Fatal(err)
			}
			response, err := CustomProtocolResponseToMaheshvara([]byte(fixture), wireAdapterConfig(name))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeMaheshvaraResponse(response, adapter.format)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != fixture {
				t.Fatalf("native response changed: %s", encoded)
			}
			for _, target := range builtinWireAdapters {
				body, err := EncodeMaheshvaraResponse(response, target.format)
				if err != nil {
					t.Fatalf("%s: %v", target.name, err)
				}
				if !strings.Contains(string(body), "hello") {
					t.Fatalf("%s text lost: %s", target.name, body)
				}
			}
		})
	}
}

func TestWireAdapterUsageOverridePreservesNativeExtensions(t *testing.T) {
	config := wireAdapterConfig("responses")
	config.Aliases = &CustomProtocolAliases{Usage: map[string][]string{"cached": {"vendor_read"}}}
	body := []byte(`{"id":"r","model":"m","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":3,"total_tokens":103,"vendor_read":25,"input_tokens_details":{"cached_tokens":0,"other_detail":false}},"extension":9007199254740993123}`)
	response, err := CustomProtocolResponseToMaheshvara(body, config)
	if err != nil {
		t.Fatal(err)
	}
	response.Model = "visible-model"
	encoded, err := EncodeMaheshvaraResponse(response, FormatResponses)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"cached_tokens":25`, `"other_detail":false`, `9007199254740993123`, `"model":"visible-model"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("missing %s: %s", expected, encoded)
		}
	}
	decoder, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatal(err)
	}
	events, _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.completed","response":` + string(body) + `}`})
	if err != nil {
		t.Fatal(err)
	}
	writer := &captureStreamWriter{}
	renderer := NewMaheshvaraStreamRenderer(FormatResponses, writer, "m")
	for _, event := range events {
		if err := renderer.Write(&event); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(writer.String(), `"cached_tokens":25`) || !strings.Contains(writer.String(), `"other_detail":false`) {
		t.Fatalf("stream alias/native extension lost: %s", writer.String())
	}
}

func TestWireAdapterCustomToolStreamIndependentOfID(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(wireAdapterConfig("responses"))
	if err != nil {
		t.Fatal(err)
	}
	writer := &captureStreamWriter{}
	renderer := NewMaheshvaraStreamRenderer(FormatResponses, writer, "m")
	for _, raw := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"i1","call_id":"c1","name":"patch","input":""}}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"i1","delta":"not JSON"}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[{"type":"custom_tool_call","id":"i1","call_id":"c1","name":"patch","input":"not JSON"}]}}`,
	} {
		events, _, err := decoder.Decode(SSEEvent{Data: raw})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if err := renderer.Write(&event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !decoder.TerminalReceived() {
		t.Fatal("native terminal not recorded")
	}
	if err := renderer.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writer.String(), `"delta":"not JSON"`) || !strings.Contains(writer.String(), `"input":"not JSON"`) {
		t.Fatalf("free text stream lost: %s", writer.String())
	}
}

func TestWireAdapterRejectsIgnoredMappings(t *testing.T) {
	for _, response := range []CustomProtocolResponse{
		{Adapter: "missing"}, {Adapter: "responses", TextPath: "answer"},
		{Stream: &CustomProtocolStreamMapping{Adapter: "responses", Mode: "cumulative"}},
	} {
		config := wireAdapterConfig("responses")
		config.Response = response
		if err := ValidateCustomProtocol(config); err == nil {
			encoded, _ := json.Marshal(response)
			t.Fatalf("ignored configuration accepted: %s", encoded)
		}
	}
}

func TestWireAdaptersPreserveToolSchemaIntegers(t *testing.T) {
	request, _, err := ConvertRequestToMaheshvara([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"type":"integer","maximum":9007199254740993123}}}}}]}`), FormatOpenAIChat, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, adapter := range builtinWireAdapters {
		body, err := adapter.EncodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `9007199254740993123`) {
			t.Fatalf("%s coerced schema integer: %s", adapter.name, body)
		}
		context := maheshvaraTemplateContext(request)
		if err := applyCustomProtocolShape(adapter.name, request, context); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(context["maheshvara"].(map[string]any)["tools"])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `9007199254740993123`) {
			t.Fatalf("%s custom coerced schema integer: %s", adapter.name, encoded)
		}
	}
}

func TestWireAdaptersDetectErrorEnvelopes(t *testing.T) {
	for _, adapter := range builtinWireAdapters {
		response, err := adapter.DecodeResponse([]byte(`{"error":{"message":"upstream rejected request","type":"invalid_request_error","code":400,"status":"INVALID_ARGUMENT"}}`))
		if err != nil {
			t.Fatal(err)
		}
		if response.Error == nil {
			t.Fatalf("%s accepted error as success", adapter.name)
		}
		for _, target := range builtinWireAdapters {
			encoded, err := target.EncodeResponse(response)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), "upstream rejected request") {
				t.Fatalf("%s error lost: %s", target.name, encoded)
			}
		}
	}
}
