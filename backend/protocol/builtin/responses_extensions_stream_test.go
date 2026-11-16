package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func buildResponseRetentionFrames(t *testing.T) []string {
	t.Helper()
	frames := responsesPaddedTextFrames()
	frames[0] = replaceResponseFrameFields(t, frames[0], responseRetentionUsage)
	progress := strings.Replace(frames[0], "response.created", "response.in_progress", 1)
	frames = append(frames[:1], append([]string{progress, progress}, frames[1:]...)...)
	last := len(frames) - 1
	frames[last] = replaceResponseFrameFields(t, frames[last], responseZeroRetentionUsage)
	return frames
}

func TestResponsesRetentionStreamSnapshots(t *testing.T) {
	frames := buildResponseRetentionFrames(t)
	from := shippedProjectionProtocol(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	validation, err := from.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	var usage *p.Usage
	for index, body := range frames {
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(index, err)
		}
		for _, event := range frame.Events {
			usage = p.MergeUsage(usage, event.Usage)
			if event.Response != nil {
				usage = p.MergeUsage(usage, event.Response.Usage)
			}
		}
		if index < 3 && (usage.CacheCreation.Count != 7000 || usage.Details["cache_creation.30m_tokens"].Count != 3000 || usage.Details["cache_creation.12h_tokens"].Count != 4000) {
			t.Fatal("repeated usage snapshot accumulated retention counts", usage)
		}
	}
	if usage.Input.Count != 3 || usage.Output.Count != 1 || usage.Total.Count != 4 || usage.CacheCreation.Count != 0 || usage.Details["cache_creation.30m_tokens"].Count != 0 || usage.Details["cache_creation.12h_tokens"].Count != 0 {
		t.Fatal("final observed zero did not replace earlier usage", usage)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) { auditConvertFrames(t, Responses, target, frames) })
	}
}

func TestResponsesRetentionDisclosureBoundary(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct {
		name, fields string
		isValid      bool
	}{
		{"no-usage-update", `"id":"r"`, true},
		{"same-pair", responseRetentionUsage, true},
		{"new-zero-pair", responseZeroRetentionUsage, true},
		{"null-usage", `"usage":null`, false},
		{"empty-usage", `"usage":{}`, false},
		{"new-total-without-pair", `"usage":{"input_tokens":13000,"output_tokens":6,"total_tokens":13006}`, false},
		{"null-pair", strings.Replace(responseRetentionUsage, `{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}`, `null`, 1), false},
		{"partial-pair", strings.Replace(responseRetentionUsage, `,"ttl_12h_tokens":4000`, ``, 1), false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			defer validation.Release()
			initial := buildResponseRetentionFrames(t)[0]
			if err := validation.Consume(t.Context(), testValue(t, initial)); err != nil {
				t.Fatal(err)
			}
			progress := fmt.Sprintf(`{"type":"response.in_progress","response":{%s}}`, fixture.fields)
			err = validation.Consume(t.Context(), testValue(t, progress))
			if fixture.isValid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireDraftRejection(t, err, "/response/usage")
			}
		})
	}
	for _, fields := range []string{`"usage":null`, `"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}`} {
		validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		frames := buildResponseRetentionFrames(t)
		for _, body := range frames[:len(frames)-1] {
			if err := validation.Consume(t.Context(), testValue(t, body)); err != nil {
				t.Fatal(err)
			}
		}
		err = validation.Consume(t.Context(), testValue(t, replaceResponseFrameFields(t, frames[len(frames)-1], fields)))
		validation.Release()
		requireDraftRejection(t, err, "/response/usage")
	}
}

func setResponseObservationValue(metadata []p.ResponseMetadata, name string, value p.Value) {
	for index := range metadata {
		if metadata[index].Name == name {
			metadata[index].Value = value
		}
	}
}

func rebuildResponseExtensionStream(t *testing.T, frames []string, edit func(*p.Event)) []p.Value {
	t.Helper()
	compiled := testCompiled(t, Responses)
	sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer sourceOptions.State.Release()
	targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer targetOptions.State.Release()
	targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
	var output []p.Value
	for _, body := range frames {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			event.Native = nil
			if event.Item != nil {
				event.Item.Native = nil
				clearMediaScopeNative(event.Item.Children)
				clearMediaScopeNative(event.Item.ReasoningContent)
			}
			if event.Response != nil {
				event.Response.Native = nil
				clearMediaScopeNative(event.Response.Content)
			}
			if edit != nil {
				edit(&event)
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
	return output
}

func TestResponsesExtensionStreamRebuild(t *testing.T) {
	frames := buildResponseMetadataFrames(`"image":{"context_compression":"disabled"},"input_transformations":` + responseTransformation)
	output := rebuildResponseExtensionStream(t, frames, func(event *p.Event) {
		setResponseObservationValue(event.Metadata, "image", testValue(t, "true"))
		if event.Response != nil {
			setResponseObservationValue(event.Response.Metadata, "image", testValue(t, "true"))
		}
	})
	hasCompleted := false
	for _, frame := range output {
		fields, _ := frame.ReadObject()
		if !fields["type"].IsString("response.completed") {
			continue
		}
		hasCompleted = true
		response, _ := fields["response"].ReadObject()
		sameJSON(t, response["image"].Bytes(), `{"context_compression":"enabled"}`)
		sameJSON(t, response["input_transformations"].Bytes(), responseTransformation)
	}
	if !hasCompleted {
		t.Fatal("native-free observation stream did not finish")
	}
}

func TestResponsesRetentionNullStreamRebuild(t *testing.T) {
	frames := buildResponseRetentionFrames(t)
	fields := strings.Replace(responseZeroRetentionUsage, `{"ttl_30m_tokens":0,"ttl_12h_tokens":0}`, `null`, 1)
	for _, index := range []int{0, 1, 2, len(frames) - 1} {
		frames[index] = replaceResponseFrameFields(t, frames[index], fields)
	}
	output := rebuildResponseExtensionStream(t, frames, nil)
	hasCompleted := false
	for _, frame := range output {
		members, _ := frame.ReadObject()
		if !members["type"].IsString("response.completed") {
			continue
		}
		hasCompleted = true
		response, _ := members["response"].ReadObject()
		sameJSON(t, response["usage"].Bytes(), strings.TrimPrefix(fields, `"usage":`))
	}
	if !hasCompleted {
		t.Fatal("null retention stream did not finish")
	}
}

func TestResponsesExtensionStreamPolicies(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, fixture := range []struct {
			name, fields, rule  string
			canProject, isEmpty bool
		}{
			{"image", `"image":{"context_compression":"disabled"}`, "metadata", true, false},
			{"unreported-image", `"image":null`, "metadata", false, false},
			{"transformations", `"input_transformations":` + responseTransformation, "metadata", false, false},
			{"empty-transformations", `"input_transformations":[]`, "metadata", true, true},
			{"retention", responseRetentionUsage, "usage-projection", true, false},
		} {
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				t.Run(target+"/"+fixture.name+"/"+mode, func(t *testing.T) {
					options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
					defer options.State.Release()
					body := replaceResponseFrameFields(t, responsesPaddedTextFrames()[0], fixture.fields)
					frame, err := from.DecodeFrame(t.Context(), testValue(t, body), options)
					if err != nil {
						t.Fatal(err)
					}
					conversion := resolveResponseExtensionConversion(t, from, to, p.ConversionEvent, mode, fixture.rule)
					route, err := conversion.WithFrameUsage(conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE).WithOperation(options), frame.Events)
					if err != nil {
						t.Fatal(err)
					}
					targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}).WithConversion(conversion, route)
					defer targetOptions.State.Release()
					sink := &p.DiagnosticSink{}
					for _, event := range frame.Events {
						// Isolate observation fidelity from unrelated creation-time loss.
						event.Native = nil
						if event.Response != nil {
							event.Response.Native = nil
							delete(event.Response.Attributes, "created_at")
						}
						var projected p.Event
						projected, err = conversion.Event(t.Context(), event, route, sink)
						if err == nil {
							_, err = to.EncodeFrames(t.Context(), projected, targetOptions)
						}
						if err != nil {
							break
						}
					}
					if !fixture.canProject || mode == "disabled" || mode == "strict" && !fixture.isEmpty {
						requireDraftRejection(t, err, "")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					hasDiagnostic := false
					for _, issue := range sink.Issues() {
						isPath := issue.Path == "/image" || issue.Path == "/input_transformations" || strings.HasSuffix(issue.Path, "/details/cache_creation.30m_tokens")
						code := p.ConversionDegraded
						if fixture.isEmpty {
							code = p.ConversionNormalized
						}
						if isPath && issue.Code == code && issue.RuleID == "event-"+fixture.rule && issue.PolicyHash == conversion.Hash {
							hasDiagnostic = true
						}
					}
					if !hasDiagnostic {
						t.Fatal("stream observation projection lost its diagnostic identity", sink.Issues())
					}
				})
			}
		}
	}
}
