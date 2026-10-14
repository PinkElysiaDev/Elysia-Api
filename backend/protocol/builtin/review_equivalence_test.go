package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// These fingerprints freeze complete observations before the dev69 quality
// refactor. A mismatch prints the observation; it must not be rebaselined merely
// because a refactor changed frames, diagnostics, accounting or policy identity.
func TestReviewConversionEquivalence(t *testing.T) {
	observed := map[string]string{}
	baseline := map[string]string{}
	const baselinePath = "testdata/review-dev69-equivalence.json"
	if raw, err := os.ReadFile(baselinePath); err == nil {
		if err := json.Unmarshal(raw, &baseline); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
	check := func(name string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(name, err)
		}
		sum := sha256.Sum256(raw)
		observed[name] = hex.EncodeToString(sum[:])
		if observed[name] != baseline[name] {
			t.Errorf("%s changed from %s to %s:\n%s", name, baseline[name], observed[name], raw)
		}
	}
	for _, source := range []string{Chat, Responses, Anthropic, Gemini} {
		from := shippedProjectionProtocol(t, source)
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			for _, mode := range []string{"compatible", "strict"} {
				policy := p.DefaultConversionPolicy(to, from)
				policy.Mode = mode
				// Keep the dev69 policy identity frozen. dev70 adds a dedicated
				// control action; its effect on existing samples is checked below
				// separately from the historical hashes.
				current, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				legacyRules := make([]p.ConversionRule, 0, len(policy.Rules))
				for _, rule := range policy.Rules {
					if rule.Action != "responses_turn_state" {
						legacyRules = append(legacyRules, rule)
					}
				}
				policy.Rules = legacyRules
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				name := source + "/" + target + "/" + mode
				report := p.VerifyBindingCombination(t.Context(), to, from, nil, conversion)
				report.CompilerVersion = "2.0.0-dev.69"
				check(name+"/combination", report)
				for _, sample := range from.Definition().Samples {
					if sample.Direction != p.DecodeRequest && sample.Direction != p.DecodeResponse && sample.Direction != p.DecodeEvent {
						continue
					}
					observation := observeReviewConversion(t, from, to, conversion, sample)
					check(name+"/"+sample.ID, observation)
					modern := observeReviewConversion(t, from, to, current, sample)
					modern.PolicyHash = observation.PolicyHash
					for i := range modern.Issues {
						if modern.Issues[i].PolicyHash == current.Hash {
							modern.Issues[i].PolicyHash = conversion.Hash
						}
						if revision := conversion.RuleRevisions[modern.Issues[i].RuleID]; revision != "" {
							modern.Issues[i].PolicyRevision = revision
						}
					}
					before, _ := json.Marshal(observation)
					after, _ := json.Marshal(modern)
					if string(before) != string(after) {
						t.Errorf("dev70 changed existing sample %s/%s:\n%s\n%s", name, sample.ID, before, after)
					}
				}
				if source == Responses {
					check(name+"/padded-stream", observeReviewConversion(t, from, to, conversion, p.Sample{Direction: p.DecodeEvent, Sequence: true, Input: testValue(t, "["+strings.Join(responsesPaddedTextFrames(), ",")+"]")}))
				}
				if source == Chat {
					raw := `[{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"OK"},"logprobs":{"content":` + auditLogprobs + `},"finish_reason":null}]},{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{"annotations":` + citationChat + `},"finish_reason":"stop"}]}]`
					check(name+"/metadata-stream", observeReviewConversion(t, from, to, conversion, p.Sample{Direction: p.DecodeEvent, Sequence: true, Input: testValue(t, raw)}))
				}
			}
		}
	}
	if len(observed) != len(baseline) {
		t.Fatalf("coverage changed: observed=%d baseline=%d", len(observed), len(baseline))
	}
}

type reviewConversionObservation struct {
	PolicyHash string
	Source     []p.Value
	Output     []p.Value
	Issues     []p.ConversionIssue
	Failure    string
}

func observeReviewConversion(t *testing.T, from, to *p.Compiled, conversion *p.CompiledConversion, sample p.Sample) (observation reviewConversionObservation) {
	t.Helper()
	sink := &p.DiagnosticSink{}
	observation.PolicyHash = conversion.Hash
	defer func() { observation.Issues = sink.Issues() }()
	options := p.EvaluationContext{State: p.NewEvaluationState(), Diagnostics: sink, Values: sample.Context, Scope: sample.Scope}
	route := conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE)
	recordSource := func(value any) {
		encoded, err := p.EncodeValue(value)
		if err != nil {
			t.Fatal(err)
		}
		observation.Source = append(observation.Source, encoded)
	}
	var err error
	switch sample.Direction {
	case p.DecodeRequest:
		var request *p.Request
		request, err = from.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
		if err == nil {
			recordSource(request)
			request, err = conversion.Request(t.Context(), request, route, sink)
		}
		if err == nil {
			var raw []byte
			raw, err = to.EncodeRequest(t.Context(), request, options)
			if err == nil {
				observation.Output = append(observation.Output, testValue(t, string(raw)))
			}
		}
	case p.DecodeResponse:
		var response *p.Response
		response, err = from.DecodeResponse(t.Context(), sample.Input.Bytes(), options)
		if err == nil {
			recordSource(response)
			response, err = conversion.Response(t.Context(), response, route, sink)
		}
		if err == nil {
			var raw []byte
			raw, err = to.EncodeResponse(t.Context(), response, options)
			if err == nil {
				observation.Output = append(observation.Output, testValue(t, string(raw)))
			}
		}
	case p.DecodeEvent:
		input := []p.Value{sample.Input}
		if sample.Sequence {
			if err = sample.Input.Decode(&input); err != nil {
				break
			}
		}
		state := p.NewConversionEventState(conversion, route)
		emit := func(event p.Event) error {
			converted, e := conversion.Event(t.Context(), event, route, sink)
			if e != nil {
				return e
			}
			frames, e := to.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{converted}}, options)
			observation.Output = append(observation.Output, frames...)
			return e
		}
		for _, inputFrame := range input {
			var frame *p.EventFrame
			frame, err = from.DecodeFrame(t.Context(), inputFrame, options)
			if err != nil {
				break
			}
			recordSource(frame.Events)
			for _, event := range p.DeliveryFrameEvents(frame.Events) {
				var batch []p.Event
				batch, err = state.Push(event)
				if err != nil {
					break
				}
				for _, next := range batch {
					if err = emit(next); err != nil {
						break
					}
				}
				if err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			var tail []p.Event
			tail, err = state.Drain()
			if err == nil {
				for _, event := range tail {
					if err = emit(event); err != nil {
						break
					}
				}
			}
		}
		if err == nil {
			var frames []p.Value
			frames, err = to.FinishEvents(t.Context(), options)
			observation.Output = append(observation.Output, frames...)
		}
	}
	if err != nil {
		observation.Failure = err.Error()
	}
	return observation
}
