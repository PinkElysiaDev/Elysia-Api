package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const progressSnapshotResponse = `{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[],"instructions":null,"tool_choice":"auto","usage":{"input_tokens":3,"output_tokens":0}}`

func buildProgressSnapshotFrame(response string, isBuffering bool) string {
	return fmt.Sprintf(`{"type":"response.in_progress","safety_buffering":%t,"response":%s}`, isBuffering, response)
}

func TestResponsesProgressSnapshotSource(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, isBuffering := range []bool{false, true} {
		t.Run(fmt.Sprint(isBuffering), func(t *testing.T) {
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			frame, err := compiled.DecodeFrame(t.Context(), testValue(t, buildProgressSnapshotFrame(progressSnapshotResponse, isBuffering)), options)
			if err != nil {
				t.Fatal(err)
			}
			if len(frame.Events) != 1 || frame.Events[0].Response == nil {
				t.Fatal("complete progress response lost its neutral snapshot", frame.Events)
			}
			event := frame.Events[0]
			if !event.Response.ID.IsString("r") || !event.Response.Model.IsString("m") || !event.Response.Status.IsString("in_progress") || event.Response.Attributes["created_at"] != testValue(t, "1") {
				t.Fatal("progress snapshot lost its observed envelope", event.Response)
			}
			if len(event.Response.Metadata) != 0 {
				t.Fatal("progress metadata has two owners", event.Response.Metadata)
			}
			requireResponseMetadata(t, event.Metadata, "instructions", "null", "empty", "/response")
			requireResponseMetadata(t, event.Metadata, "tool_choice", `"auto"`, "information", "/response")
		})
	}
}

func TestResponsesProgressSnapshotRebuild(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, isBuffering := range []bool{false, true} {
		t.Run(fmt.Sprint(isBuffering), func(t *testing.T) {
			sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer sourceOptions.State.Release()
			targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer targetOptions.State.Release()
			targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
			for index, response := range []string{progressSnapshotResponse, strings.ReplaceAll(progressSnapshotResponse, `"auto"`, `"required"`)} {
				frame, err := compiled.DecodeFrame(t.Context(), testValue(t, buildProgressSnapshotFrame(response, isBuffering)), sourceOptions)
				if err != nil {
					t.Fatal(err)
				}
				var output []p.Value
				for _, event := range frame.Events {
					event.Native = nil
					if event.Response != nil {
						event.Response.Native = nil
					}
					encoded, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
					if err != nil {
						t.Fatal(index, err)
					}
					output = append(output, encoded...)
				}
				consume(output)
				if len(output) != 1 {
					t.Fatal("progress emitted extra frames", output)
				}
				fields, err := output[0].ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				expected := strings.Replace(response, `"output_tokens":0`, `"output_tokens":0,"total_tokens":3`, 1)
				sameJSON(t, fields["response"].Bytes(), expected)
				if fields["safety_buffering"] != testValue(t, fmt.Sprint(isBuffering)) {
					t.Fatal("progress lost buffering metadata", fields)
				}
			}
		})
	}
}

func TestResponsesProgressSnapshotRetainsOutput(t *testing.T) {
	const message = `{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"snapshot","annotations":[]}]}`
	compiled := testCompiled(t, Responses)
	for _, hasNativeSnapshot := range []bool{false, true} {
		t.Run(fmt.Sprint(hasNativeSnapshot), func(t *testing.T) {
			sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer sourceOptions.State.Release()
			targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer targetOptions.State.Release()
			targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
			response := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[`+message+`],"future_neighbor":{"n":9007199254740993}`, 1)
			for index, body := range []string{buildProgressSnapshotFrame(response, false), buildProgressSnapshotFrame(`{"id":"r","tool_choice":"required","usage":{"input_tokens":4,"output_tokens":2}}`, true)} {
				frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
				if err != nil {
					t.Fatal(index, err)
				}
				for _, event := range frame.Events {
					if !hasNativeSnapshot || index != 0 {
						event.Native = nil
						if event.Response != nil {
							event.Response.Native = nil
						}
					}
					encoded, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
					if err != nil || len(encoded) != 1 {
						t.Fatal(index, "progress snapshot emitted extra content frames", encoded, err)
					}
					consume(encoded)
					fields, _ := encoded[0].ReadObject()
					snapshot, _ := fields["response"].ReadObject()
					sameJSON(t, snapshot["output"].Bytes(), `[`+message+`]`)
					sameJSON(t, snapshot["future_neighbor"].Bytes(), `{"n":9007199254740993}`)
					if index != 0 {
						sameJSON(t, snapshot["usage"].Bytes(), `{"input_tokens":4,"output_tokens":2,"total_tokens":6}`)
						if !snapshot["tool_choice"].IsString("required") {
							t.Fatal("progress retained stale metadata", snapshot)
						}
					}
				}
			}
		})
	}
}

func TestResponsesProgressSnapshotWithoutFrameMetadata(t *testing.T) {
	compiled := testCompiled(t, Responses)
	sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer sourceOptions.State.Release()
	targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer targetOptions.State.Release()
	targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
	response := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"retained","annotations":[]}]}]`, 1)
	frame, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"response.in_progress","response":`+response+`}`), sourceOptions)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Events) != 1 || frame.Events[0].Response == nil {
		t.Fatal("progress without optional frame metadata lost its snapshot", frame.Events)
	}
	event := frame.Events[0]
	event.Native, event.Response.Native = nil, nil
	output, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
	if err != nil || len(output) != 0 {
		t.Fatal("ordinary progress changed historical frame delivery", output, err)
	}
	frame, err = compiled.DecodeFrame(t.Context(), testValue(t, buildProgressSnapshotFrame(`{"id":"r"}`, false)), sourceOptions)
	if err != nil {
		t.Fatal(err)
	}
	event = frame.Events[0]
	event.Native = nil
	output, err = compiled.EncodeFrames(t.Context(), event, targetOptions)
	if err != nil || len(output) != 1 {
		t.Fatal("later buffered progress lost the observed snapshot", output, err)
	}
	consume(output)
	fields, _ := output[0].ReadObject()
	sameJSON(t, fields["response"].Bytes(), strings.Replace(response, `"output_tokens":0`, `"output_tokens":0,"total_tokens":3`, 1))
	if fields["safety_buffering"] != testValue(t, "false") {
		t.Fatal("progress lost the later buffering observation", output)
	}
}

func TestResponsesProgressSnapshotAdvancesIndexedText(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct {
		name, id, text string
		shouldReject   bool
	}{
		{"newer", "msg", "snapshot", false},
		{"different-identity", "other", "snapshot", true},
		{"conflicting-text", "msg", "different", true},
		{"conflicting-status", "msg", "snapshot", true},
		{"multiple-parts", "msg", "snapshot", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer sourceOptions.State.Release()
			targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer targetOptions.State.Release()
			targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
			frames := responsesPaddedTextFrames()[:3]
			if fixture.name == "conflicting-text" {
				frames = append(frames, responsesPaddedTextFrames()[3])
			}
			if fixture.name == "multiple-parts" {
				frames = append(frames, `{"type":"response.content_part.added","output_index":0,"content_index":1,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[]}}`)
			}
			message := fmt.Sprintf(`{"type":"message","id":%q,"role":"assistant","status":"in_progress","content":[{"type":"output_text","text":%q,"annotations":[]}]}`, fixture.id, fixture.text)
			if fixture.name == "multiple-parts" {
				message = strings.TrimSuffix(message, `]}`) + `,{"type":"output_text","text":"second","annotations":[]}]}`
			}
			if fixture.name == "conflicting-status" {
				message = strings.Replace(message, `"status":"in_progress"`, `"status":"completed"`, 1)
			}
			response := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[`+message+`]`, 1)
			frames = append(frames, buildProgressSnapshotFrame(response, false))
			for index, body := range frames {
				frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range frame.Events {
					event.Native = nil
					if event.Response != nil {
						event.Response.Native = nil
					}
					output, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
					if index == len(frames)-1 && fixture.shouldReject {
						requireDraftRejection(t, err, "")
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					consume(output)
					if index == len(frames)-1 {
						if len(output) != 1 || !strings.Contains(output[0].JSONString(), `"text":"snapshot"`) {
							t.Fatal("newer snapshot lost text or emitted extra content deltas", output)
						}
						if fixture.name == "multiple-parts" && !strings.Contains(output[0].JSONString(), `"text":"second"`) {
							t.Fatal("progress dropped another content part", output)
						}
					}
				}
			}
		})
	}
}

func TestResponsesProgressSnapshotSkipsUnneededOutput(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	options, _ = bindFixtureOutput(t, compiled, options)
	event := p.Event{SchemaVersion: p.SemanticSchemaVersion, Type: p.UsageUpdated, ResponseID: p.StringValue("r"), Usage: &p.Usage{Input: &p.Counter{Count: 1, Origin: p.ObservedCount}}}
	output, err := compiled.EncodeFrames(t.Context(), event, options)
	if err != nil || len(output) != 0 {
		t.Fatal("ordinary usage without a progress frame required output state", output, err)
	}
}

func TestResponsesProgressSnapshotFullLifecycle(t *testing.T) {
	const message = `{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"snapshot","annotations":[]}]}`
	response := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[`+message+`]`, 1)
	terminal := strings.ReplaceAll(response, `"status":"in_progress"`, `"status":"completed"`)
	frames := append(responsesPaddedTextFrames()[:3], buildProgressSnapshotFrame(response, false), `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"snapshot"}`, `{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"snapshot","annotations":[]}}`, `{"type":"response.completed","response":`+terminal+`}`)
	output := auditConvertFrames(t, Responses, Responses, frames)
	progressCount, completedCount := 0, 0
	var text strings.Builder
	for _, frame := range output {
		fields, _ := frame.ReadObject()
		if fields["type"].IsString("response.output_text.delta") {
			var delta string
			if err := fields["delta"].Decode(&delta); err != nil {
				t.Fatal(err)
			}
			text.WriteString(delta)
		}
		if fields["type"].IsString("response.in_progress") {
			progressCount++
		}
		if fields["type"].IsString("response.completed") {
			completedCount++
			if !strings.Contains(fields["response"].JSONString(), `"text":"snapshot"`) {
				t.Fatal("terminal lost the snapshot content", frame)
			}
		}
	}
	if text.String() != "snapshot" || progressCount != 1 || completedCount != 1 {
		t.Fatal("progress changed the complete stream content or lifecycle", text.String(), progressCount, completedCount)
	}
}

func TestResponsesProgressSnapshotCitationRanges(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, end := range []int{8, 9} {
		t.Run(fmt.Sprint(end), func(t *testing.T) {
			sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer sourceOptions.State.Release()
			targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer targetOptions.State.Release()
			validation, err := compiled.NewWireStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			defer validation.Release()
			targetOptions = targetOptions.WithOutputStream(validation)
			message := fmt.Sprintf(`{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"snapshot","annotations":[{"type":"url_citation","url":"https://example.test","title":"source","start_index":0,"end_index":%d}]}]}`, end)
			response := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[`+message+`]`, 1)
			frame, err := compiled.DecodeFrame(t.Context(), testValue(t, buildProgressSnapshotFrame(response, false)), sourceOptions)
			if err != nil {
				t.Fatal(err)
			}
			event := frame.Events[0]
			event.Native, event.Response.Native = nil, nil
			output, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
			if err == nil {
				for _, value := range output {
					err = validation.Consume(t.Context(), value)
					if err != nil {
						break
					}
				}
			}
			if end > len("snapshot") {
				requireDraftRejection(t, err, "")
			} else if err != nil || len(output) != 1 {
				t.Fatal("progress citation no longer refers to its observed snapshot", output, err)
			}
		})
	}
}

func TestResponsesProgressSnapshotMetadataDiagnostics(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ fields, path, reason string }{
		{`"background":42`, "/background", "invalid response metadata type; expected bool or null"},
		{`"background":42,"parallel_tool_calls":42`, "/background", "invalid response metadata type; expected bool or null"},
		{`"output_text":42,"parallel_tool_calls":42`, "/output_text", ""},
	} {
		t.Run(fixture.fields, func(t *testing.T) {
			_, err := compiled.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fixture.fields)), fixtureOptions(p.EvaluationContext{}))
			var conversion *p.ConversionError
			if !errors.As(err, &conversion) || len(conversion.Issues) == 0 {
				t.Fatal("invalid metadata lost its structured diagnostic", err)
			}
			issue := conversion.Issues[0]
			if issue.Path != fixture.path || fixture.reason != "" && issue.Reason != fixture.reason {
				t.Fatal("metadata validation changed field location, reason or order", issue)
			}
		})
	}
}

func TestResponsesProgressSnapshotWireRangeIsolation(t *testing.T) {
	compiled := testCompiled(t, Responses)
	const citation = `{"type":"url_citation","url":"https://example.test","title":"source","start_index":0,"end_index":8}`
	const message = `{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"snapshot","annotations":[` + citation + `]}]}`
	response := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[`+message+`]`, 1)
	progress := `{"type":"response.in_progress","sequence_number":0,"response":` + response + `}`
	for _, shouldRewrite := range []bool{false, true} {
		t.Run(fmt.Sprint(shouldRewrite), func(t *testing.T) {
			validation, err := compiled.NewWireStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			defer validation.Release()
			before := testValue(t, progress)
			after := before
			if shouldRewrite {
				after = testValue(t, strings.Replace(progress, `"snapshot"`, `"SNAPSHOT"`, 1))
			}
			err = validation.ConsumeTransformed(t.Context(), before, after, p.EvaluationContext{})
			if shouldRewrite {
				if err == nil || !strings.Contains(err.Error(), "unproven text") {
					t.Fatal("progress citation accepted rewritten text", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
			frames := []string{
				`{"type":"response.created","sequence_number":1,"response":{"id":"r","model":"m","created_at":1,"status":"in_progress","output":[]}}`,
				`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[]}}`,
				`{"type":"response.content_part.added","sequence_number":3,"output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[]}}`,
				`{"type":"response.output_text.delta","sequence_number":4,"output_index":0,"content_index":0,"item_id":"msg","delta":"snapshot"}`,
				`{"type":"response.output_text.annotation.added","sequence_number":5,"output_index":0,"content_index":0,"item_id":"msg","annotation_index":0,"annotation":` + citation + `}`,
			}
			for index, body := range frames {
				before, after := testValue(t, body), testValue(t, body)
				if index == 3 {
					after = testValue(t, strings.Replace(body, `"snapshot"`, `"SNAPSHOT"`, 1))
				}
				err := validation.ConsumeTransformed(t.Context(), before, after, p.EvaluationContext{})
				if index == len(frames)-1 {
					if err == nil || !strings.Contains(err.Error(), "unproven text") {
						t.Fatal("late real citation borrowed proof from progress snapshot", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(index, err)
				}
				if err := validation.CommitDelivery(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestResponsesProgressSnapshotUsesCurrentItems(t *testing.T) {
	compiled := testCompiled(t, Responses)
	sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer sourceOptions.State.Release()
	targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer targetOptions.State.Release()
	targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
	frames := responsesPaddedTextFrames()
	stale := strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":[{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"O","annotations":[]}]}]`, 1)
	frames = append(frames[:5], append([]string{buildProgressSnapshotFrame(stale, false)}, frames[5:]...)...)
	var deltas strings.Builder
	progressCount := 0
	for _, body := range frames {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			event.Native = nil
			if event.Response != nil {
				event.Response.Native = nil
			}
			encoded, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
			if err != nil {
				t.Fatal(err)
			}
			consume(encoded)
			for _, output := range encoded {
				fields, _ := output.ReadObject()
				if fields["type"].IsString("response.output_text.delta") {
					var text string
					if err := fields["delta"].Decode(&text); err != nil {
						t.Fatal(err)
					}
					deltas.WriteString(text)
				}
				if fields["type"].IsString("response.in_progress") {
					progressCount++
					if !strings.Contains(fields["response"].JSONString(), `"text":"OK"`) {
						t.Fatal("stale snapshot replaced current indexed deltas", output)
					}
				}
			}
		}
	}
	final, err := compiled.FinishEvents(t.Context(), targetOptions)
	if err != nil {
		t.Fatal(err)
	}
	consume(final)
	if deltas.String() != "OK" || progressCount != 1 {
		t.Fatal("progress changed content delivery", deltas.String(), progressCount)
	}
}

func TestResponsesProgressSnapshotPartialState(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, hasCreatedAt := range []bool{false, true} {
		t.Run(fmt.Sprint(hasCreatedAt), func(t *testing.T) {
			sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer sourceOptions.State.Release()
			targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer targetOptions.State.Release()
			targetOptions, consume := bindFixtureOutput(t, compiled, targetOptions)
			start := responsesPaddedTextFrames()[0]
			if !hasCreatedAt {
				start = strings.Replace(start, `"created_at":1,`, "", 1)
			}
			for index, body := range []string{start, buildProgressSnapshotFrame(`{"id":"r"}`, false)} {
				frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
				if err != nil {
					t.Fatal("sparse source progress was rejected", err)
				}
				for _, event := range frame.Events {
					event.Native = nil
					encoded, err := compiled.EncodeFrames(t.Context(), event, targetOptions)
					if index == 1 && !hasCreatedAt {
						requireDraftRejection(t, err, "/response")
						continue
					}
					if err != nil || len(encoded) != 1 {
						t.Fatal(index, encoded, err)
					}
					consume(encoded)
				}
			}
		})
	}
}

func TestResponsesProgressSnapshotOutputContract(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, response string }{
		{"id-only", `{"id":"r"}`},
		{"missing-id", strings.Replace(progressSnapshotResponse, `"id":"r",`, "", 1)},
		{"missing-model", strings.Replace(progressSnapshotResponse, `"model":"m",`, "", 1)},
		{"missing-time", strings.Replace(progressSnapshotResponse, `"created_at":1,`, "", 1)},
		{"missing-object", strings.Replace(progressSnapshotResponse, `"object":"response",`, "", 1)},
		{"missing-status", strings.Replace(progressSnapshotResponse, `"status":"in_progress",`, "", 1)},
		{"missing-output", strings.Replace(progressSnapshotResponse, `"output":[],`, "", 1)},
		{"wrong-model", strings.Replace(progressSnapshotResponse, `"model":"m"`, `"model":42`, 1)},
		{"wrong-id", strings.Replace(progressSnapshotResponse, `"id":"r"`, `"id":false`, 1)},
		{"wrong-time", strings.Replace(progressSnapshotResponse, `"created_at":1`, `"created_at":"1"`, 1)},
		{"wrong-object", strings.Replace(progressSnapshotResponse, `"object":"response"`, `"object":"other"`, 1)},
		{"wrong-status", strings.Replace(progressSnapshotResponse, `"status":"in_progress"`, `"status":false`, 1)},
		{"wrong-output", strings.Replace(progressSnapshotResponse, `"output":[]`, `"output":{}`, 1)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			validation, err := compiled.NewWireStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			frame := fmt.Sprintf(`{"type":"response.in_progress","sequence_number":0,"response":%s}`, fixture.response)
			if err := validation.Consume(t.Context(), testValue(t, frame)); err == nil {
				t.Fatal("client accepted incomplete or malformed progress response", frame)
			}
		})
	}
}
