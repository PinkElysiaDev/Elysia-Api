package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestMetadataUpdatesKeepOwnerAndMappedFieldChecks(t *testing.T) {
	compiled := testCompiled(t, Chat)
	first := p.ResponseMetadata{Codec: Chat, SourceCodec: Chat, Location: "response", Name: "service_tier", Path: "/response/service_tier", Value: p.StringValue("auto")}
	next := first
	next.Path, next.Value = "/service_tier", p.StringValue("default")
	left := first
	left.Location, left.Name, left.Path = "choice", "logprobs", "/choices/0/logprobs"
	right := left
	right.Path = "/choices/1/logprobs"
	collector, err := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Release()
	for _, event := range []p.Event{
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, ResponseID: p.StringValue("r"), Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Metadata: []p.ResponseMetadata{first}}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.MetadataUpdated, ResponseID: p.StringValue("r"), Metadata: []p.ResponseMetadata{next}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("choice:0"), Item: &p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant")}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("choice:1"), Item: &p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant")}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemSnapshot, ItemID: p.StringValue("choice:0"), Item: &p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant"), Metadata: []p.ResponseMetadata{left}}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemSnapshot, ItemID: p.StringValue("choice:1"), Item: &p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant"), Metadata: []p.ResponseMetadata{right}}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("choice:0")},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("choice:1")},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, ResponseID: p.StringValue("r"), Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}},
	} {
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	response, err := collector.Finish()
	if err != nil || len(response.Metadata) != 1 || response.Metadata[0] != next {
		t.Fatal("response metadata path change created a second owner", response, err)
	}
	if len(response.Content) != 2 || len(response.Content[0].Metadata) != 1 || len(response.Content[1].Metadata) != 1 || response.Content[0].Metadata[0] != left || response.Content[1].Metadata[0] != right {
		t.Fatal("choice metadata collapsed across its core item owners", response.Content)
	}
	response.Content = nil
	response.Attributes["service_tier"] = p.StringValue("flex")
	if _, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("mapped attribute conflicted with the retained metadata value")
	}
}

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
