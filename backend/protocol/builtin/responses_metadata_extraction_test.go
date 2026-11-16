package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const responsesMetadataBase = `{"id":"r","object":"response","model":"m","created_at":1,"status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}]}`

func buildResponseMetadataBody(fields string) string {
	return strings.TrimSuffix(responsesMetadataBase, "}") + "," + fields + "}"
}

func replaceResponseMetadata(metadata []p.ResponseMetadata, name string, value p.Value) {
	for index := range metadata {
		if metadata[index].Name == name {
			metadata[index].Value = value
		}
	}
}

func resolveMetadataExtractionConversion(t *testing.T, from, to *p.Compiled, phase p.ConversionPhase, mode string) *p.CompiledConversion {
	t.Helper()
	policy := p.DefaultConversionPolicy(to, from)
	if mode == "strict" {
		policy.Mode = mode
	}
	if mode == "disabled" {
		for index := range policy.Rules {
			if policy.Rules[index].ID == string(phase)+"-metadata" {
				policy.Rules[index].Enabled = false
			}
		}
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	return conversion
}

func requireResponseMetadata(t *testing.T, metadata []p.ResponseMetadata, name, value, meaning, prefix string) {
	t.Helper()
	for _, field := range metadata {
		if field.Name != name {
			continue
		}
		if field.Codec != Responses || field.SourceCodec != Responses || field.Location != "response" || field.Meaning != meaning || field.Path != prefix+"/"+name {
			t.Fatal("response metadata lost its source ownership or meaning", field)
		}
		sameJSON(t, field.Value.Bytes(), value)
		return
	}
	t.Fatal("response field remained outside declared metadata", name, metadata)
}

func TestResponsesMetadataExtractionRebuildsWithoutNative(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, value, meaning string }{
		{"instructions", "null", "empty"},
		{"instructions", `"Be concise"`, "information"},
		{"instructions", `[{"role":"developer","content":"Be concise"}]`, "information"},
		{"instructions", `[{"type":"function_call_output","call_id":"independent","output":"OK"}]`, "information"},
		{"previous_response_id", "null", "empty"},
		{"previous_response_id", `"previous-r"`, "state"},
		{"tool_choice", "null", "empty"},
		{"tool_choice", `"auto"`, "information"},
		{"tool_choice", `{"type":"function","name":"lookup"}`, "information"},
		{"tool_choice", `{"type":"mcp","server_label":"lookup","name":null}`, "information"},
	} {
		t.Run(fixture.name+"/"+fixture.value, func(t *testing.T) {
			body := buildResponseMetadataBody(`"` + fixture.name + `":` + fixture.value + `,"future_neighbor":{"n":9007199254740993}`)
			response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			requireResponseMetadata(t, response.Metadata, fixture.name, fixture.value, fixture.meaning, "")
			remainder, err := response.Attributes["wire:openai_responses"].ReadObject()
			if err != nil || !remainder[fixture.name].IsZero() || remainder["future_neighbor"].IsZero() {
				t.Fatal("metadata and unknown remainder have overlapping or missing ownership", response.Attributes, err)
			}
			response.Native = nil
			wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			edited := "edited"
			if fixture.name == "tool_choice" {
				edited = "required"
			}
			replaceResponseMetadata(response.Metadata, fixture.name, p.StringValue(edited))
			wire, err = compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, buildResponseMetadataBody(fmt.Sprintf(`%q:%q,"future_neighbor":{"n":9007199254740993}`, fixture.name, edited)))
		})
	}
}

func TestResponsesMetadataExtractionPolicies(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, fixture := range []struct {
			name, fields       string
			isEmpty            bool
			shouldAlwaysReject bool
		}{
			{"null", `"instructions":null,"previous_response_id":null,"tool_choice":null`, true, false},
			{"live", `"instructions":null,"previous_response_id":null,"tool_choice":"auto"`, false, false},
			{"owned-container", `"instructions":[{"role":"developer","content":"policy"}],"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"future":true}]}`, false, false},
			{"reference", `"previous_response_id":"previous-r"`, false, true},
			{"unknown-neighbor", `"instructions":null,"future_neighbor":null`, false, true},
		} {
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				t.Run(target+"/"+fixture.name+"/"+mode, func(t *testing.T) {
					response, err := from.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fixture.fields)), fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					response.Native = nil
					// Isolate metadata fidelity from unrelated timestamp and item-layout projections.
					delete(response.Attributes, "created_at")
					response.Content[0].ID = p.Value{}
					response.Content[0].Status = p.Value{}
					response.Usage = &p.Usage{Input: &p.Counter{Count: 3, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}, Total: &p.Counter{Count: 4, Origin: p.ObservedCount}}
					conversion := resolveMetadataExtractionConversion(t, from, to, p.ConversionResponse, mode)
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), sink)
					if err == nil {
						_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
					}
					shouldReject := fixture.shouldAlwaysReject || mode == "disabled" || mode == "strict" && !fixture.isEmpty
					if shouldReject {
						requireDraftRejection(t, err, "")
						if fixture.name == "reference" && mode != "disabled" && !strings.Contains(err.Error(), "/previous_response_id") {
							t.Fatal("state metadata lost its source path", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, field := range response.Metadata {
						hasDiagnostic := false
						for _, issue := range sink.Issues() {
							if issue.Path == field.Path && issue.RuleID == "response-metadata" && issue.PolicyHash == conversion.Hash {
								hasDiagnostic = true
								if field.Meaning == "empty" && issue.Code != p.ConversionNormalized || field.Meaning == "information" && issue.Code != p.ConversionDegraded {
									t.Fatal("metadata projection lost its fidelity classification", field, issue)
								}
							}
						}
						if !hasDiagnostic {
							t.Fatal("metadata projection lost its declared diagnostic", field.Name, sink.Issues())
						}
					}
				})
			}
		}
	}
}

func buildResponseMetadataFrames(fields string) []string {
	frames := responsesPaddedTextFrames()
	frames[0] = strings.Replace(frames[0], `"response":{`, `"response":{`+fields+`,`, 1)
	progress := strings.Replace(frames[0], `"response.created"`, `"response.in_progress"`, 1)
	frames = append(frames[:1], append([]string{progress}, frames[1:]...)...)
	last := len(frames) - 1
	frames[last] = strings.Replace(frames[last], `"response":{`, `"response":{`+fields+`,`, 1)
	return frames
}

func TestResponsesMetadataExtractionStreams(t *testing.T) {
	const fields = `"instructions":null,"previous_response_id":null,"tool_choice":"auto"`
	frames := buildResponseMetadataFrames(fields)
	compiled := testCompiled(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	for index, body := range frames {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(index, err)
		}
		if index != 0 && index != 1 && index != len(frames)-1 {
			continue
		}
		var metadata []p.ResponseMetadata
		for _, event := range frame.Events {
			metadata = append(metadata, event.Metadata...)
			if event.Response != nil {
				metadata = append(metadata, event.Response.Metadata...)
			}
		}
		prefix := ""
		if index == 1 {
			prefix = "/response"
		}
		requireResponseMetadata(t, metadata, "instructions", "null", "empty", prefix)
		requireResponseMetadata(t, metadata, "previous_response_id", "null", "empty", prefix)
		requireResponseMetadata(t, metadata, "tool_choice", `"auto"`, "information", prefix)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			output := auditConvertFrames(t, Responses, target, frames)
			if len(output) == 0 {
				t.Fatal("stream had no target frames")
			}
		})
	}
}

func TestResponsesMetadataExtractionStreamRebuild(t *testing.T) {
	compiled := testCompiled(t, Responses)
	sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer sourceOptions.State.Release()
	targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer targetOptions.State.Release()
	targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
	var output []p.Value
	for _, body := range buildResponseMetadataFrames(`"instructions":null,"previous_response_id":null,"tool_choice":"auto"`) {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			event.Native = nil
			if event.Response != nil {
				event.Response.Native = nil
			}
			replaceResponseMetadata(event.Metadata, "tool_choice", p.StringValue("required"))
			if event.Response != nil {
				replaceResponseMetadata(event.Response.Metadata, "tool_choice", p.StringValue("required"))
			}
			encoded, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
			if err != nil {
				t.Fatal(err)
			}
			consume(encoded)
			output = append(output, encoded...)
		}
	}
	encoded, err := compiled.FinishEvents(t.Context(), targetOptions)
	if err != nil {
		t.Fatal(err)
	}
	consume(encoded)
	output = append(output, encoded...)
	hasCompleted := false
	for _, frame := range output {
		fields, _ := frame.ReadObject()
		if fields["type"] != p.StringValue("response.completed") {
			continue
		}
		hasCompleted = true
		response, _ := fields["response"].ReadObject()
		if response["tool_choice"] != p.StringValue("required") || !response["instructions"].IsNull() || !response["previous_response_id"].IsNull() {
			t.Fatal("native-free stream rebuilt stale or missing metadata", frame)
		}
	}
	if !hasCompleted {
		t.Fatal("native-free stream did not finish")
	}
}

func TestResponsesMetadataExtractionInvalidTypes(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, value string }{
		{"instructions", "false"}, {"instructions", "12"}, {"instructions", `{"future":true}`},
		{"tool_choice", "false"}, {"tool_choice", "12"}, {"tool_choice", `[{"type":"future"}]`},
		{"previous_response_id", `""`}, {"previous_response_id", "12"},
		{"previous_response_id", `{}`}, {"previous_response_id", `[]`},
	} {
		t.Run(fixture.name+"/"+fixture.value, func(t *testing.T) {
			fields := fmt.Sprintf("%q:%s", fixture.name, fixture.value)
			_, err := compiled.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fields)), fixtureOptions(p.EvaluationContext{}))
			requireDraftRejection(t, err, "/"+fixture.name)
			valid := buildResponseMetadataFrames(`"instructions":null,"previous_response_id":null,"tool_choice":null`)
			invalid := buildResponseMetadataFrames(fields)
			for _, position := range []int{0, 1, len(valid) - 1} {
				t.Run(fmt.Sprint(position), func(t *testing.T) {
					validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
					if err != nil {
						t.Fatal(err)
					}
					defer validation.Release()
					for _, body := range valid[:position] {
						if err := validation.Consume(t.Context(), testValue(t, body)); err != nil {
							t.Fatal(err)
						}
					}
					requireDraftRejection(t, validation.Consume(t.Context(), testValue(t, invalid[position])), "/response/"+fixture.name)
				})
			}
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			_, err = compiled.DecodeFrame(t.Context(), testValue(t, buildResponseMetadataFrames(fields)[0]), options)
			requireDraftRejection(t, err, "/"+fixture.name)
		})
	}
}

func TestResponsesMetadataExtractionStreamPolicies(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"strict", "disabled"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				defer sourceOptions.State.Release()
				targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				defer targetOptions.State.Release()
				frame, err := from.DecodeFrame(t.Context(), testValue(t, buildResponseMetadataFrames(`"instructions":null,"previous_response_id":null,"tool_choice":"auto"`)[0]), sourceOptions)
				if err != nil {
					t.Fatal(err)
				}
				conversion := resolveMetadataExtractionConversion(t, from, to, p.ConversionEvent, mode)
				route, err := conversion.WithFrameUsage(conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE), frame.Events)
				if err != nil {
					t.Fatal(err)
				}
				targetOptions = targetOptions.WithConversion(conversion, route)
				for _, event := range frame.Events {
					var projected p.Event
					projected, err = conversion.Event(t.Context(), event, route, nil)
					if err == nil {
						_, err = to.EncodeFrames(t.Context(), projected, targetOptions)
					}
					if err != nil {
						break
					}
				}
				requireDraftRejection(t, err, "metadata")
			})
		}
	}
}

func TestResponsesMetadataExtractionFailureBoundary(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	const failure = `{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"message":"failed"},"instructions":null,"previous_response_id":null,"tool_choice":"auto"}}`
	frame, err := validation.DecodeFrame(t.Context(), testValue(t, failure), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Events) != 1 || frame.Events[0].Type != p.OperationFailed {
		t.Fatal("metadata changed the provider failure boundary", frame.Events)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	progress := `{"type":"response.in_progress","sequence_number":2,"response":{"instructions":null,"previous_response_id":null,"tool_choice":"auto"}}`
	requireDraftRejection(t, validation.Consume(t.Context(), testValue(t, progress)), "/type")
}
