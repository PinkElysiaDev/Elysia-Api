package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestSourceUsagePublishesOnlyNewCounters(t *testing.T) {
	upstream := compileFixtureDefinition(t, presetDefinition(t, p.PresetAnthropicID))
	candidate := gatewayCandidate{compiled: upstream, binding: p.Binding{Capabilities: upstream.Definition().Capabilities}}
	route := p.ConversionContext{Source: upstream.Identity(), Target: upstream.Identity(), Transport: p.SSE}
	for _, ending := range []string{"complete", "cancel", "provider-error"} {
		t.Run(ending, func(t *testing.T) {
			record, reference := &usageRecord{}, &usageRecord{}
			var expected *p.Usage
			consumed := 0
			source, err := newProtocolSourceStream(t.Context(), candidate, upstream, route, p.EvaluationContext{State: p.NewEvaluationState()}, record, func(frame *p.EventFrame, _ p.Object) error {
				for _, event := range frame.Events {
					// The former source path merged/published on every accepted
					// event, including content with no new counters.
					expected = p.MergeUsage(expected, event.Usage)
					if event.Response != nil {
						expected = p.MergeUsage(expected, event.Response.Usage)
					}
					updateRecordProtocolUsage(reference, expected)
					consumed++
				}
				if !reflect.DeepEqual(record.ProtocolUsage, reference.ProtocolUsage) || !reflect.DeepEqual(record.Usage, reference.Usage) || !reflect.DeepEqual(record.UsageDetail, reference.UsageDetail) || record.UsageSource != reference.UsageSource {
					t.Fatal("source record differs from unconditional publication")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			push := func(body string) {
				t.Helper()
				if err := source.Push(mustProtocolValue(t, body), nil); err != nil {
					t.Fatal(err)
				}
			}
			push(`{"type":"message_start","message":{"id":"r","model":"m","role":"assistant","type":"message","content":[],"usage":{"input_tokens":7,"output_tokens":0}}}`)
			previous, previousInput := record.ProtocolUsage, record.Usage.InputTokens
			for _, body := range []string{
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}}`,
				`{"type":"content_block_stop","index":0}`,
			} {
				push(body)
				if record.ProtocolUsage != previous || record.Usage.InputTokens != previousInput {
					t.Fatal("content-only event republished unchanged counters")
				}
			}
			push(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"cache_read_input_tokens":0}}`)
			if record.ProtocolUsage == previous || record.ProtocolUsage.Total.Count != 9 || record.ProtocolUsage.CacheRead.Count != 0 {
				t.Fatal("new counters or observed zero were not published")
			}
			previous = record.ProtocolUsage
			switch ending {
			case "complete":
				push(`{"type":"message_stop"}`)
				err = source.Finish(nil)
			case "cancel":
				err = source.Finish(context.Canceled)
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation cause lost", err)
				}
				err = nil
			case "provider-error":
				push(`{"type":"error","error":{"type":"overloaded_error","message":"synthetic provider failure"}}`)
				if source.terminalFailure == nil {
					t.Fatal("provider failure cause lost")
				}
			}
			if err != nil || record.ProtocolUsage != previous || consumed == 0 {
				t.Fatal("ending lost or republished the last usage", err)
			}
		})
	}
}
