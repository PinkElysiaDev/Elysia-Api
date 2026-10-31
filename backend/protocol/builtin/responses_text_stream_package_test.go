package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageSourceCapacity(t *testing.T) {
	const messageCount = 48
	limits := p.DefaultLimits()
	limits.StateItems = 64
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	legacyCompiler, err := p.NewCompiler(limits, Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy, issues := legacyCompiler.Compile(read("definitions/openai-responses.json"))
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(limits, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, issues := compiler.CompileSources(read("packages/openai-responses/spec.json"), read("packages/openai-responses/mapping.json"))
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	frames := []p.Value{testValue(t, `{"type":"response.created","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`)}
	var output []string
	for index := range messageCount {
		message := fmt.Sprintf(`{"type":"message","id":"m_%d","role":"assistant","status":"completed","content":[]}`, index)
		frames = append(frames,
			testValue(t, fmt.Sprintf(`{"type":"response.output_item.added","output_index":%d,"item":%s}`, index, strings.Replace(message, "completed", "in_progress", 1))),
			testValue(t, fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, message)))
		output = append(output, message)
	}
	frames = append(frames, testValue(t, `{"type":"response.completed","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[`+strings.Join(output, ",")+`]}}`))
	for name, compiled := range map[string]*p.Compiled{"legacy": legacy, "package": candidate} {
		t.Run(name, func(t *testing.T) {
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			defer options.State.Release()
			var wire *p.WireStreamValidation
			if compiled == candidate {
				wire, err = compiled.NewWireInputStreamValidation(p.Scope{})
				if err != nil {
					t.Fatal(err)
				}
			}
			finishedMessages, finishedResponses := 0, 0
			for index, frame := range frames {
				if wire != nil {
					if err := wire.Consume(t.Context(), frame); err != nil {
						t.Fatal("wire capacity decreased", index, err)
					}
				}
				decoded, err := compiled.DecodeFrame(t.Context(), frame, options)
				if err != nil {
					t.Fatal("source capacity decreased", index, err)
				}
				for _, event := range decoded.Events {
					if event.Type == p.ItemFinished {
						finishedMessages++
					}
					if event.Type == p.ResponseFinished {
						finishedResponses++
						if event.Response == nil || len(event.Response.Content) != messageCount {
							t.Fatal("terminal output lost registered messages")
						}
					}
				}
			}
			if finishedMessages != messageCount || finishedResponses != 1 {
				t.Fatal("message or response completed more than once", finishedMessages, finishedResponses)
			}
			if wire != nil {
				if err := wire.Finish(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestResponsesPackageDuplicateSourceStarts(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, item := range []string{
		`{"type":"message","id":"m","role":"assistant","status":"in_progress","content":[]}`,
		`{"type":"function_call","id":"f","call_id":"c","name":"run","arguments":""}`,
	} {
		start := testValue(t, `{"type":"response.created","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`)
		frame := testValue(t, `{"type":"response.output_item.added","output_index":0,"item":`+item+`}`)
		var failures []*p.ConversionError
		for _, compiled := range []*p.Compiled{legacy, candidate} {
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			for _, initial := range []p.Value{start, frame} {
				if _, err := compiled.DecodeFrame(t.Context(), initial, options); err != nil {
					t.Fatal(err)
				}
			}
			_, err := compiled.DecodeFrame(t.Context(), frame, options)
			options.State.Release()
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) == 0 {
				t.Fatal("direct source decoding accepted a duplicate start", err)
			}
			failures = append(failures, failure)
		}
		before, after := failures[0].Issues[0], failures[1].Issues[0]
		if before.Code != after.Code || before.Stage != after.Stage || before.Path != after.Path {
			t.Fatal("duplicate start classification changed", before, after)
		}
	}
}

func TestResponsesPackageTextSourceStream(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, timing := range []string{"start", "done", "terminal"} {
		for _, refusal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/refusal=%t", timing, refusal), func(t *testing.T) {
				frames := phaseStreamFrames(t, timing, []string{`"commentary"`, `"final_answer"`, `null`})
				if refusal {
					for index, frame := range frames {
						raw := strings.ReplaceAll(frame.JSONString(), "response.output_text", "response.refusal")
						raw = strings.ReplaceAll(raw, `"type":"output_text"`, `"type":"refusal"`)
						raw = strings.ReplaceAll(raw, `"text":`, `"refusal":`)
						frames[index] = testValue(t, raw)
					}
				}
				oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
				newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
				defer oldOptions.State.Release()
				defer newOptions.State.Release()
				wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
				if err != nil {
					t.Fatal(err)
				}
				for number, frame := range frames {
					before, err := legacy.DecodeFrame(t.Context(), frame, oldOptions)
					if err != nil {
						t.Fatal("existing source", number, err)
					}
					if err := wire.Consume(t.Context(), frame); err != nil {
						t.Fatal("independent source contract", number, err)
					}
					after, err := candidate.DecodeFrame(t.Context(), frame, newOptions)
					if err != nil {
						t.Fatal("declared source", number, err)
					}
					want, err := p.EncodeValue(responsesTextEventsAtOriginalLocations(t, before.Events, frame))
					if err != nil {
						t.Fatal(err)
					}
					got, err := p.EncodeValue(after.Events)
					if err != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
						t.Fatalf("text source events changed at frame %d: %v\nold: %s\nnew: %s", number, err, want.JSONString(), got.JSONString())
					}
				}
				if err := wire.Finish(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// Source locations now name the actual frame fragment. Native JSON keeps the
// shared ConfigStd container spelling; no field, payload or diagnostic is removed.
func responsesTextEventsAtOriginalLocations(t *testing.T, events []p.Event, frame p.Value) []p.Event {
	t.Helper()
	result := streamEventsAtVersion(events, p.ProtocolCoreVersion)
	fields, err := frame.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	isTerminal := fields["type"].IsString("response.completed")
	for index := range result {
		event := &result[index]
		if event.Item != nil && event.Item.Kind == p.MessageNode {
			metadataPath := "/item"
			if isTerminal {
				var output int
				var identity string
				_ = event.ItemID.Decode(&identity)
				if _, err := fmt.Sscanf(identity, "output:%d", &output); err != nil {
					t.Fatal(err)
				}
				metadataPath = fmt.Sprintf("/response/output/%d", output)
			}
			node := normalizeResponsesTextNode(t, *event.Item, "/item", metadataPath)
			event.Item = &node
		}
		if event.Type == p.ResponseFinished && event.Response != nil {
			event.Response.Content = append([]p.Node(nil), event.Response.Content...)
			for position, node := range event.Response.Content {
				path := fmt.Sprintf("/response/output/%d", position)
				event.Response.Content[position] = normalizeResponsesTextNode(t, node, path, path)
			}
		}
	}
	return result
}

func normalizeResponsesTextNode(t *testing.T, node p.Node, sourcePath, metadataPath string) p.Node {
	t.Helper()
	if node.Source != nil {
		source := *node.Source
		source.Path = sourcePath
		node.Source = &source
	}
	if node.Native != nil {
		native := *node.Native
		native.Source.Path = sourcePath
		encoded, err := sonic.ConfigStd.Marshal(native.Value)
		if err != nil {
			t.Fatal(err)
		}
		native.Value, err = p.ParseValue(encoded)
		if err != nil {
			t.Fatal(err)
		}
		node.Native = &native
	}
	node.Metadata = append([]p.ResponseMetadata(nil), node.Metadata...)
	for index := range node.Metadata {
		node.Metadata[index].Path = metadataPath + "/" + node.Metadata[index].Name
	}
	node.Children = append([]p.Node(nil), node.Children...)
	for index, child := range node.Children {
		path := fmt.Sprintf("%s/content/%d", sourcePath, index)
		node.Children[index] = normalizeResponsesTextNode(t, child, path, path)
	}
	return node
}
