package builtin

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicNullStreamUsageDetailsKeepOwnership(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, field := range []string{"cache_creation", "output_tokens_details"} {
		for _, isStart := range []bool{true, false} {
			for _, hasLaterDetails := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/start=%t/later=%t", field, isStart, hasLaterDetails), func(t *testing.T) {
					raws, ownedIndex := nullUsageFrames(field, isStart, hasLaterDetails)
					frames, response := decodePackageStream(t, compiled, raws, p.Scope{})
					usage := response.Usage
					if usage.Input.Count != 10 || usage.Output.Count != 2 || !usage.ExecutionSpeed.IsString("fast") {
						t.Fatal("null detail changed parent counters or start speed", usage)
					}
					if hasLaterDetails {
						checkConcreteUsageDetails(t, usage, field)
					} else {
						checkNullUsageCountersAbsent(t, usage)
					}
					checkNullUsageFrameOwner(t, compiled, frames, ownedIndex, raws[ownedIndex])
					sameJSON(t, response.Attributes["wire:stream"].Bytes(), "["+raws[ownedIndex]+"]")
					if replayed := encodePackageStream(t, compiled, frames, p.Scope{}, true); !slices.Equal(replayed, raws) {
						t.Fatal("authorized native replay changed nullable observations", replayed)
					}
					checkNullUsageRebuildRejected(t, compiled, frames, ownedIndex)
					checkNullUsageForeignRejected(t, frames[ownedIndex])
					checkNullUsageEditsRejected(t, compiled, frames, ownedIndex)
					checkNullUsageCollectionRejected(t, response)
				})
			}
		}
	}
}

func nullUsageFrames(field string, isStart, hasLaterDetails bool) ([]string, int) {
	raws := executionSpeedFrames(`"fast"`)
	ownedIndex := 4
	nullDetail := fmt.Sprintf(`,%q:null`, field)
	concreteDetail := concreteUsageDetail(field)
	if isStart {
		ownedIndex = 0
		raws[0] = strings.Replace(raws[0], `"output_tokens":0`, `"output_tokens":0`+nullDetail, 1)
		if hasLaterDetails {
			raws[4] = strings.Replace(raws[4], `"output_tokens":2`, `"output_tokens":2`+concreteDetail, 1)
		}
		return raws, ownedIndex
	}
	raws[4] = strings.Replace(raws[4], `"output_tokens":2`, `"output_tokens":2`+nullDetail, 1)
	if hasLaterDetails {
		raws[4] = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1` + nullDetail + `}}`
		later := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2` + concreteDetail + `}}`
		raws = slices.Insert(raws, 5, later)
	}
	return raws, ownedIndex
}

func concreteUsageDetail(field string) string {
	if field == "cache_creation" {
		return `,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}`
	}
	return `,"output_tokens_details":{"thinking_tokens":1}`
}

func checkConcreteUsageDetails(t *testing.T, usage *p.Usage, field string) {
	t.Helper()
	if field == "cache_creation" {
		if usage.CacheCreation == nil || usage.CacheCreation.Count != 0 {
			t.Fatal("later concrete cache counters were not processed", usage)
		}
		for _, detail := range []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"} {
			counter, hasCounter := usage.Details[detail]
			if !hasCounter || counter.Count != 0 || counter.Origin != p.ObservedCount {
				t.Fatal("later explicit zero lost its observation", detail, usage)
			}
		}
		return
	}
	if usage.CacheCreation != nil || usage.Details["output.reasoning_tokens"].Count != 1 {
		t.Fatal("later concrete thinking counter was not processed", usage)
	}
}

func checkNullUsageCountersAbsent(t *testing.T, usage *p.Usage) {
	t.Helper()
	if usage.CacheCreation != nil {
		t.Fatal("null detail invented a cache total", usage)
	}
	for _, field := range []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens", "output.reasoning_tokens"} {
		if _, hasCounter := usage.Details[field]; hasCounter {
			t.Fatal("null detail invented a counter", field, usage)
		}
	}
}

func checkNullUsageFrameOwner(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, ownedIndex int, raw string) {
	t.Helper()
	for frameIndex, frame := range frames {
		for eventIndex, event := range frame.Events {
			if frameIndex != ownedIndex || eventIndex != 0 {
				if event.Unmapped != nil {
					t.Fatal("null observation ownership leaked to another event", frameIndex, eventIndex, event)
				}
				continue
			}
			owner := event.Unmapped
			if owner == nil || owner.Source.Protocol != compiled.Identity() || owner.Source.Direction != p.DecodeEvent || owner.Source.Path != "" || owner.Value != testValue(t, raw) {
				t.Fatal("explicit null lost its original frame owner", event)
			}
		}
	}
}

func cloneUsageFrameWithoutNative(frame *p.EventFrame) *p.EventFrame {
	events := slices.Clone(frame.Events)
	for index := range events {
		events[index].Native = nil
		if events[index].Response != nil {
			response := *events[index].Response
			response.Native = nil
			events[index].Response = &response
		}
	}
	return &p.EventFrame{Events: events}
}

func checkNullUsageRebuildRejected(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, ownedIndex int) {
	t.Helper()
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	options, consume := bindFixtureOutput(t, compiled, options)
	encodeUsagePrefix(t, compiled, frames[:ownedIndex], options, consume)
	output, err := compiled.EncodeFrame(t.Context(), cloneUsageFrameWithoutNative(frames[ownedIndex]), options)
	requireNullUsageIssue(t, err, "/unmapped")
	if len(output) != 0 {
		t.Fatal("rejected null detail still emitted a partial frame", output)
	}
}

func encodeUsagePrefix(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, options p.EvaluationContext, consume func([]p.Value)) {
	t.Helper()
	for index, frame := range frames {
		output, err := compiled.EncodeFrame(t.Context(), cloneUsageFrameWithoutNative(frame), options)
		if err != nil {
			t.Fatal("ordinary prefix failed before the owned observation", index, err)
		}
		consume(output)
	}
}

func checkNullUsageForeignRejected(t *testing.T, frame *p.EventFrame) {
	t.Helper()
	for _, targetName := range []string{Chat, Responses, Gemini} {
		for _, hasNative := range []bool{true, false} {
			target := testCompiled(t, targetName)
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			options, _ = bindFixtureOutput(t, target, options)
			candidate := frame
			if !hasNative {
				candidate = cloneUsageFrameWithoutNative(frame)
			}
			output, err := target.EncodeFrame(t.Context(), candidate, options)
			options.State.Release()
			requireNullUsageIssue(t, err, "/unmapped")
			if len(output) != 0 {
				t.Fatal("foreign target emitted source-owned nullable details", targetName, hasNative, output)
			}
		}
	}
}

func checkNullUsageEditsRejected(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, ownedIndex int) {
	t.Helper()
	frame := frames[ownedIndex]
	before, err := p.EncodeValue(frame.Events)
	if err != nil {
		t.Fatal(err)
	}
	edited := *frame
	edited.Events = slices.Clone(frame.Events)
	edited.Events[0].Usage = p.CloneUsage(edited.Events[0].Usage)
	edited.Events[0].Usage.Output.Count++
	edited.Events[0].Usage.Total.Count++
	for _, isConverted := range []bool{false, true} {
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		defer options.State.Release()
		options, consume := bindFixtureOutput(t, compiled, options)
		encodeUsagePrefix(t, compiled, frames[:ownedIndex], options, consume)
		var output []p.Value
		if isConverted {
			output, err = compiled.EncodeConvertedFrame(t.Context(), frame, edited.Events, options)
		} else {
			output, err = compiled.EncodeFrame(t.Context(), &edited, options)
		}
		if isConverted {
			requireNullUsageIssue(t, err, "/unmapped")
		} else if err == nil || !strings.Contains(err.Error(), "modified compound native frame") {
			t.Fatal("edited event regained native frame authority", err)
		}
		if len(output) != 0 {
			t.Fatal("edited event replayed stale source observations", output)
		}
	}
	after, err := p.EncodeValue(frame.Events)
	if err != nil || before != after {
		t.Fatal("rejection mutated source events", err)
	}
}

func checkNullUsageCollectionRejected(t *testing.T, response *p.Response) {
	t.Helper()
	for _, targetName := range []string{Anthropic, Chat, Responses, Gemini} {
		target := testCompiled(t, targetName)
		candidate := *response
		candidate.Native = nil
		_, err := target.EncodeResponseValue(t.Context(), &candidate, fixtureOptions(p.EvaluationContext{}))
		if targetName == Gemini && candidate.Usage.CacheCreation != nil {
			requireContextIssue(t, err, p.ConversionRejected, "/usage/cacheCreation")
			candidate.Usage = p.CloneUsage(candidate.Usage)
			candidate.Usage.CacheCreation = nil
			_, err = target.EncodeResponseValue(t.Context(), &candidate, fixtureOptions(p.EvaluationContext{}))
		}
		requireNullUsageIssue(t, err, "/attributes")
	}
}

func requireNullUsageIssue(t *testing.T, err error, path string) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue p.ConversionIssue) bool {
		return issue.Path == path
	}) {
		t.Fatal("nullable source observation was not explicitly refused at its ownership boundary", path, err)
	}
}

func TestAnthropicOrdinaryStreamUsageDetailsStayMapped(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, field := range []string{"cache_creation", "output_tokens_details"} {
		for _, isStart := range []bool{true, false} {
			for _, detailKind := range []string{"absent", "empty", "concrete"} {
				t.Run(fmt.Sprintf("%s/start=%t/details=%s", field, isStart, detailKind), func(t *testing.T) {
					raws := executionSpeedFrames(`"fast"`)
					if detailKind != "absent" {
						detail := fmt.Sprintf(`,%q:{}`, field)
						if detailKind == "concrete" {
							detail = concreteUsageDetail(field)
						}
						if isStart {
							raws[0] = strings.Replace(raws[0], `"output_tokens":0`, `"output_tokens":1`+detail, 1)
						} else {
							raws[4] = strings.Replace(raws[4], `"output_tokens":2`, `"output_tokens":2`+detail, 1)
						}
					}
					frames, response := decodePackageStream(t, compiled, raws, p.Scope{})
					for _, frame := range frames {
						for _, event := range frame.Events {
							if event.Unmapped != nil {
								t.Fatal("ordinary missing or concrete detail acquired an owner guard", event)
							}
						}
					}
					if !response.Attributes["wire:stream"].IsZero() {
						t.Fatal("ordinary details acquired source-owned stream evidence", response.Attributes)
					}
					if detailKind == "concrete" {
						checkConcreteUsageDetails(t, response.Usage, field)
					}
					wire := encodePackageStream(t, compiled, frames, p.Scope{}, false)
					_, rebuilt := decodePackageStream(t, compiled, wire, p.Scope{})
					if rebuilt.Usage.Input.Count != 10 || rebuilt.Usage.Output.Count != 2 || !rebuilt.Usage.ExecutionSpeed.IsString("fast") {
						t.Fatal("ordinary detail rebuild changed counters or speed", rebuilt.Usage)
					}
					if detailKind == "concrete" {
						checkConcreteUsageDetails(t, rebuilt.Usage, field)
					}
				})
			}
		}
	}
}
