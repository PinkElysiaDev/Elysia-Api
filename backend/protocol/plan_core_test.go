package protocol

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestCoreOutputPublishedContracts(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("builtin", "definitions", "*.json"))
	if err != nil || len(paths) != 4 {
		t.Fatal("missing builtin contract corpus", err)
	}
	checked := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document Definition
		if err := sonic.ConfigStd.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		for _, sample := range document.Samples {
			if sample.Expected.IsZero() {
				continue
			}
			switch sample.Direction {
			case DecodeRequest, DecodeResponse, DecodeEvent:
			default:
				continue
			}
			checked++
			t.Run(document.ID+"/"+sample.ID, func(t *testing.T) {
				for _, output := range []JSONOutput{JSONLeaf(sample.Expected), expandCoreOutput(t, sample.Expected)} {
					switch sample.Direction {
					case DecodeRequest:
						request := assertCoreOutputParity(t, output, decodeCoreRequestOutput)
						assertCoreReaderParity(t, request, func(pointer []string) (JSONOutput, error) { return readCoreRequestOutput(request, pointer, nil) })
					case DecodeResponse:
						response := assertCoreOutputParity(t, output, decodeCoreResponseOutput)
						assertCoreReaderParity(t, response, func(pointer []string) (JSONOutput, error) { return readCoreResponseOutput(response, pointer, nil) })
					case DecodeEvent:
						if sample.Expected.IsArray() {
							events := assertCoreOutputParity(t, output, decodeCoreEventSlice)
							assertCoreReaderParity(t, events, func(pointer []string) (JSONOutput, error) { return readCoreEventsOutput(*events, pointer, nil) })
						} else {
							event := assertCoreOutputParity(t, output, decodeCoreEventOutput)
							assertCoreReaderParity(t, event, func(pointer []string) (JSONOutput, error) { return readCoreEventOutput(event, pointer, nil) })
						}
					}
				}
			})
		}
	}
	if checked < 40 {
		t.Fatal("core contract corpus unexpectedly small", checked)
	}
}

func TestCoreOutputRawContracts(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `[]`, `true`,
		`{"source":{"family":"first"},"source":{"revision":"second"},"source":null}`,
		`{"schemaVersion":1,"SchemaVersion":2,"SCHEMAVERSION":null,"SOURCE":{"FAMILY":"test"}}`,
		`{"ſource":{"family":"unicode-fold"}}`,
		`{"source":{"family":"a","Family":"b","family":null}}`,
		`{"parameters":{"a":1},"parameters":{"b":2,"a":null}}`,
		`{"parameters":{"a":1},"parameters":null,"parameters":{}}`,
		`{"reasoning":{"mode":"auto"},"reasoning":{"interleaved":true},"reasoning":null}`,
		`{"content":[{"kind":"reasoning","reasoningContent":[]},{"kind":"reasoning","reasoningContent":null},{"kind":"message","children":[]}]}`,
		`{"content":[{"kind":"tool_call","input":{"kind":"json","value":{"n":900719925474099312345}}}]}`,
		`{"native":{"source":{"protocol":{},"direction":"decode_request","path":"/","scope":{}},"value":{"n":1e+03},"requestContext":{"values":{"flag":false},"scope":{"model":"m"}}}}`,
		`{"schemaVersion":"wrong"}`, `{"content":{}}`, `{"reasoning":{"mode":123}}`,
		`{"future":true}`, `{"source":{"future":true}}`, `{"native":{"requestWorkspace":{}}}`,
		`{"content":[{"kind":"text","payload":"\ud800","future":true}]}`,
	} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		t.Run(raw, func(t *testing.T) { assertCoreOutputParity(t, JSONLeaf(value), decodeCoreRequestOutput) })
	}
	// Constructed object keys undergo JSON's invalid-UTF-8 replacement too.
	output := JSONObject(map[string]JSONOutput{"sour\xffce": JSONLeaf(Value{raw: "null"})})
	assertCoreOutputParity(t, output, decodeCoreRequestOutput)
	for _, raw := range []string{
		`{"usage":{"input":{"count":7}},"usage":{"input":{"origin":"observed"}}}`,
		`{"usage":{"details":{"x":{"count":7}}},"usage":{"details":{"x":{"origin":"observed"}}}}`,
		`{"usage":{"details":{"x":{"count":7,"origin":"observed"}}},"usage":{"details":{"x":null}}}`,
	} {
		value, _ := ParseValue([]byte(raw))
		assertCoreOutputParity(t, JSONLeaf(value), decodeCoreResponseOutput)
	}
}

func BenchmarkCoreOutputRequest(b *testing.B) {
	for _, size := range []int{64, 1 << 20} {
		payload := StringValue(strings.Repeat("p", size))
		output := JSONObject(map[string]JSONOutput{
			"schemaVersion": JSONLeaf(Value{raw: "1"}),
			"source":        JSONObject(map[string]JSONOutput{}),
			"content": JSONArray([]JSONOutput{JSONObject(map[string]JSONOutput{
				"kind": JSONLeaf(StringValue("text")), "payload": JSONLeaf(payload),
			})}),
		})
		for _, isTyped := range []bool{false, true} {
			name := "materialized"
			if isTyped {
				name = "typed"
			}
			b.Run(fmt.Sprintf("%d/%s", size, name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var request *Request
					var err error
					if isTyped {
						request, err = decodeCoreRequestOutput(output)
					} else {
						var wire Value
						wire, err = output.Encode(b.Context())
						if err == nil {
							request = &Request{}
							err = decodeContract(wire.Bytes(), request)
						}
					}
					if err != nil || len(request.Content) != 1 || request.Content[0].Payload != payload {
						b.Fatal("core content changed", err)
					}
				}
			})
		}
	}
}

func TestCoreOutputFailureNeverUsesSuccessfulDecoder(t *testing.T) {
	output := JSONObject(map[string]JSONOutput{"schemaVersion": JSONLeaf(Value{raw: "1"})})
	result, err := decodeCoreOutput(output, func(JSONOutput, *Request, *valueReader) error { return fmt.Errorf("synthetic writer rejection") })
	if result != nil || err == nil || !strings.Contains(err.Error(), "writer rejected a valid contract") {
		t.Fatal("diagnostic rendering became a successful fallback", result, err)
	}
}

func TestCoreOutputOwnership(t *testing.T) {
	input, err := ParseValue([]byte(`{"model":"small","content":[{"kind":"text","payload":"` + strings.Repeat("p", 1<<20) + `","attributes":{"label":"original"}}],"reasoning":{"mode":"auto"}}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := decodeCoreRequestOutput(JSONLeaf(input))
	if err != nil {
		t.Fatal(err)
	}
	second, err := decodeCoreRequestOutput(JSONLeaf(input))
	if err != nil {
		t.Fatal(err)
	}
	if sharesValueStorage(input, first.Model) || !sharesValueStorage(input, first.Content[0].Payload) {
		t.Fatal("core writer must detach small values and share large immutable payloads")
	}
	first.Content[0].Attributes["label"] = StringValue("changed")
	first.Content[0].Kind = ImageNode
	first.Reasoning.Mode = "off"
	if second.Content[0].Kind != TextNode || !second.Content[0].Attributes["label"].IsString("original") || second.Reasoning.Mode != "auto" {
		t.Fatal("core writer shared mutable containers between decoded requests")
	}
}

func TestCoreOutputRawSubtreeOwnership(t *testing.T) {
	const fragmentBytes = 90 << 10
	root, err := ParseValue([]byte(`{"content":[{"kind":"text","payload":"` + strings.Repeat("p", 31<<20) + `","children":[{"kind":"text","payload":"` + strings.Repeat("f", fragmentBytes) + `"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request, err := decodeCoreRequestOutput(JSONLeaf(root))
	if err != nil {
		t.Fatal(err)
	}
	if !sharesValueStorage(root, request.Content[0].Payload) || sharesValueStorage(root, request.Content[0].Children[0].Payload) {
		t.Fatal("raw subtree must share dominant payloads and detach relatively small descendants")
	}
	// This leaf owns its entire storage; its size alone does not require a copy.
	independent := StringValue(strings.Repeat("f", fragmentBytes))
	output := JSONObject(map[string]JSONOutput{"model": JSONLeaf(independent)})
	request, err = decodeCoreRequestOutput(output)
	if err != nil || !sharesValueStorage(independent, request.Model) {
		t.Fatal("independently owned large leaf must remain shared", err)
	}
}

func TestCoreReaderMapKeysAndPresence(t *testing.T) {
	request := &Request{SchemaVersion: 1, Content: []Node{{Kind: ReasoningNode, ReasoningContent: []Node{}}}, Parameters: Object{"bad\xff": StringValue("invalid"), "bad�": StringValue("valid"), "null": Value{raw: "null"}}, ClientOutput: &ClientOutput{IncludeUsage: new(bool)}}
	assertCoreReaderParity(t, request, func(pointer []string) (JSONOutput, error) { return readCoreRequestOutput(request, pointer, nil) })
	for _, pointer := range [][]string{{"content", "00"}, {"content", "-1"}, {"schemaVersion", "child"}} {
		encoded, _ := EncodeValue(request)
		_, want := readValuePointer(encoded, pointer)
		_, got := readCoreRequestOutput(request, pointer, nil)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatal("pointer error changed", pointer, got, want)
		}
	}
}

func TestLargeCoreRequestEncodingParityAndOwnership(t *testing.T) {
	payload := parseTestValue(t, ` { "body":"`+strings.Repeat("A", 2*plannedRequestEncodeMinBytes)+`<>&", "n":1e+03, "n":1.0 } `)
	request := &Request{
		SchemaVersion: 1,
		Content: []Node{{Kind: MessageNode, Children: []Node{
			{Kind: ImageNode, Payload: payload, Native: &Native{Value: payload}},
			{Kind: ReasoningNode, ReasoningContent: []Node{}},
		}}},
		Parameters: Object{"bad\xff": StringValue("invalid"), "bad\ufffd": StringValue("valid"), "null": Value{raw: "null"}},
		Native:     &Native{Value: payload},
	}
	assertCoreReaderParity(t, request, func(pointer []string) (JSONOutput, error) {
		return readCoreRequestOutput(request, pointer, nil)
	})
	want, err := EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []any{request, *request} {
		for _, encode := range []func(any) (Value, error){
			func(value any) (Value, error) { return EncodeSemantic(t.Context(), DecodeRequest, value) },
			func(value any) (Value, error) { return encodeSemanticWithin(value, semanticLimits(DefaultLimits())) },
		} {
			got, err := encode(input)
			if err != nil || got != want {
				t.Fatalf("large semantic encoding differs: %v", err)
			}
			if sharesValueStorage(payload, got) || sharesValueStorage(got, payload) {
				t.Fatal("encoded request retained the input payload storage")
			}
		}
	}
	if after, err := EncodeValue(request); err != nil || after != want {
		t.Fatal("encoding changed the input", err)
	}
}

func assertCoreOutputParity[T any](t *testing.T, output JSONOutput, decode func(JSONOutput) (*T, error)) *T {
	t.Helper()
	wire, err := output.Encode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var want T
	wantErr := decodeContract(wire.Bytes(), &want)
	got, gotErr := decode(output)
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) {
		t.Fatalf("contract error changed: got %T %v, want %T %v", gotErr, gotErr, wantErr, wantErr)
	}
	if wantErr == nil && (got == nil || !reflect.DeepEqual(*got, want)) {
		actual, _ := EncodeValue(got)
		expected, _ := EncodeValue(want)
		t.Fatalf("core value changed for %s: got %s, want %s", wire.raw, actual.raw, expected.raw)
	}
	return got
}

func assertCoreReaderParity(t *testing.T, value any, read func([]string) (JSONOutput, error)) {
	t.Helper()
	encoded, err := EncodeValue(value)
	if err != nil {
		t.Fatal(err)
	}
	var visit func(Value, []string)
	visit = func(current Value, pointer []string) {
		output, err := read(pointer)
		if err != nil {
			t.Fatal(pointer, err)
		}
		actual, err := output.Encode(context.Background())
		if err != nil || actual != current {
			t.Fatalf("core selection %v changed: got %s, want %s: %v", pointer, actual.raw, current.raw, err)
		}
		if current.IsObject() {
			fields, _ := current.ReadObject()
			for _, key := range sortedKeys(fields) {
				visit(fields[key], append(append([]string{}, pointer...), key))
			}
		} else if current.IsArray() {
			items, _ := current.readArray()
			for index, item := range items {
				visit(item, append(append([]string{}, pointer...), fmt.Sprint(index)))
			}
		}
	}
	visit(encoded, nil)
	missing, err := read([]string{"__absent"})
	_, expectedErr := readValuePointer(encoded, []string{"__absent"})
	if fmt.Sprint(err) != fmt.Sprint(expectedErr) || !missing.IsZero() {
		t.Fatal("missing field changed", missing, err)
	}
}

func decodeCoreEventSlice(output JSONOutput) (*[]Event, error) {
	value, err := decodeCoreEventsOutput(output)
	return &value, err
}

func expandCoreOutput(t *testing.T, value Value) JSONOutput {
	t.Helper()
	if value.IsObject() {
		fields, _ := value.ReadObject()
		output := make(map[string]JSONOutput, len(fields))
		for key, child := range fields {
			output[key] = expandCoreOutput(t, child)
		}
		return JSONObject(output)
	}
	if value.IsArray() {
		items, _ := value.readArray()
		output := make([]JSONOutput, len(items))
		for index, child := range items {
			output[index] = expandCoreOutput(t, child)
		}
		return JSONArray(output)
	}
	return JSONLeaf(value)
}

func TestCoreNormalizationPreservesWireAndOwnership(t *testing.T) {
	raw, err := ParseValue([]byte(` {"text":"<&>\u2028\u2029", "n":9007199254740993,"n":1e100} `))
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Parameters: Object{"value": raw, "same": raw, "large": StringValue(strings.Repeat("x", 4096))}}
	before := request.Clone()
	for _, bounded := range []bool{false, true} {
		limits := DefaultLimits()
		if bounded {
			limits.StateItems, limits.BufferBytes = 1, 1
		}
		cache := &semanticCache{limits: limits}
		for _, changed := range []bool{false, true} {
			if changed {
				request.Parameters["value"] = StringValue("replacement<&>")
			}
			want, err := EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				output, err := readCoreRequestOutput(request, nil, cache)
				if err != nil {
					t.Fatal(err)
				}
				got, err := output.encodeWithin(t.Context(), cache)
				if err != nil || got != want {
					t.Fatalf("cached core encoding changed wire: %v", err)
				}
			}
		}
		request.Parameters["value"] = raw
	}
	if !reflect.DeepEqual(request, before) {
		t.Fatal("reader modified its caller's core")
	}
}

func BenchmarkCoreNormalization(b *testing.B) {
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Parameters: Object{"payload": StringValue(strings.Repeat("A", 2<<20))}}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var cache *semanticCache
				if cached {
					cache = &semanticCache{limits: DefaultLimits()}
				}
				for range 4 {
					output, err := readCoreRequestOutput(request, nil, cache)
					if err != nil {
						b.Fatal(err)
					}
					if _, err = output.encodeWithin(b.Context(), cache); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
