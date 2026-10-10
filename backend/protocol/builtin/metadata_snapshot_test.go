package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestResponsesServiceTierSnapshotChangesAtCompletion(t *testing.T) {
	frames := responsesPaddedTextFrames()
	frames[0] = strings.Replace(frames[0], `"status":"in_progress"`, `"service_tier":"auto","status":"in_progress"`, 1)
	progress := strings.Replace(frames[0], `"response.created"`, `"response.in_progress"`, 1)
	frames = append(frames[:1], append([]string{progress}, frames[1:]...)...)
	last := len(frames) - 1
	frames[last] = strings.Replace(frames[last], `"status":"completed"`, `"service_tier":"default","status":"completed"`, 1)
	for _, target := range []string{Chat, Responses} {
		t.Run(target, func(t *testing.T) {
			wire := auditConvertFrames(t, Responses, target, frames)
			var tiers []p.Value
			for _, frame := range wire {
				fields, _ := frame.ReadObject()
				if target == Responses {
					if fields["response"].IsZero() {
						continue
					}
					fields, _ = fields["response"].ReadObject()
				}
				if tier := fields["service_tier"]; !tier.IsZero() {
					tiers = append(tiers, tier)
				}
			}
			if len(tiers) < 2 || tiers[0] != p.StringValue("auto") || tiers[len(tiers)-1] != p.StringValue("default") {
				t.Fatalf("start/final service tier snapshots were not retained: %v", tiers)
			}
		})
	}
}

func TestMetadataUpdatesKeepOwnerAndMappedFieldChecks(t *testing.T) {
	first := p.ResponseMetadata{Codec: Chat, Location: "response", Name: "service_tier", Path: "/response/service_tier", Value: p.StringValue("auto")}
	next := first
	next.Path = "/service_tier"
	next.Value = p.StringValue("default")
	merged := mergeResponseMetadata([]p.ResponseMetadata{first}, []p.ResponseMetadata{next})
	if len(merged) != 1 || merged[0] != next {
		t.Fatal(merged)
	}
	left := first
	left.Location = "choice"
	left.Name = "logprobs"
	left.Path = "/choices/0/logprobs"
	right := left
	right.Path = "/choices/1/logprobs"
	if got := mergeResponseMetadata([]p.ResponseMetadata{left}, []p.ResponseMetadata{right}); len(got) != 2 {
		t.Fatal("different choices collapsed", got)
	}
	adapter := module{name: Chat}
	if err := adapter.writeMetadata(p.Object{"service_tier": p.StringValue("flex")}, merged, "response"); err == nil {
		t.Fatal("conflicting mapped field accepted")
	}
}
