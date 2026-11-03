package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageSemanticResponseRanges(t *testing.T) {
	for _, name := range []string{"openai-chat", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, name)
			decode := func(text string, start, end int) *p.Response {
				wire := deliveryRangeResponse(t, name, text, start, end, true)
				fields, _ := wire.ReadObject()
				delete(fields, "output_text")
				wire, err := p.EncodeValue(fields)
				if err != nil {
					t.Fatal(err)
				}
				response, err := compiled.DecodeResponseValue(t.Context(), wire, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				return response
			}
			for _, fixture := range []struct {
				name, text                     string
				start, end, nextStart, nextEnd int
				isValid                        bool
			}{
				{"identity", "😀abCDxy", 4, 6, 4, 6, true},
				{"equal-length", "😀abEFxy", 4, 6, 4, 6, false},
				{"prefix", "😀abQxy", 0, 4, 0, 4, true},
				{"shifted-suffix", "😀abQxy", 6, 8, 5, 7, true},
				{"stale-suffix", "😀abQxy", 6, 8, 6, 8, false},
				{"cross-rewrite", "😀abQxy", 3, 7, 3, 6, false},
				{"out-of-bounds", "😀abCDxy", 6, 9, 6, 9, false},
			} {
				t.Run(fixture.name, func(t *testing.T) {
					before := decode("😀abCDxy", fixture.start, fixture.end)
					after := decode(fixture.text, fixture.nextStart, fixture.nextEnd)
					metadata, err := p.EncodeValue(after.Content[0].Children[0].Metadata)
					if err != nil {
						t.Fatal(err)
					}
					policy := p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "semantic-ranges", Rules: []p.ConversionRule{
						{ID: "text", Order: 1, Enabled: true, Phase: p.ConversionResponse, Match: p.ConversionMatch{NodeKind: p.TextNode}, Action: "set", Path: "/payload", Value: p.StringValue(fixture.text)},
						{ID: "references", Order: 2, Enabled: true, Phase: p.ConversionResponse, Match: p.ConversionMatch{NodeKind: p.TextNode}, Action: "set", Path: "/metadata", Value: metadata},
					}}
					conversion, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
					converted, err := conversion.Response(t.Context(), before, route, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = compiled.EncodeResponseValue(t.Context(), converted, p.EvaluationContext{})
					if (err == nil) != fixture.isValid || err != nil && !strings.Contains(err.Error(), "reference") {
						t.Fatal("response encoding missed semantic reference proof", err)
					}
					for i := range policy.Rules {
						policy.Rules[i].Phase = p.ConversionEvent
					}
					conversion, err = p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					event, err := conversion.Event(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, Response: before}, route, nil)
					if err != nil {
						t.Fatal(err)
					}
					options := p.EvaluationContext{State: p.NewEvaluationState()}
					defer options.State.Release()
					err = compiled.ValidateEventReferences(t.Context(), event, options)
					if (err == nil) != fixture.isValid || err != nil && !strings.Contains(err.Error(), "reference") {
						t.Fatal("compound response event missed semantic reference proof", err)
					}
				})
			}
		})
	}
}

func TestPackageResponseReferenceIdentity(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	metadata := p.ResponseMetadata{Name: "annotations", Codec: "responses", Location: "content", Path: "/reference", Value: testValue(t, `[{"type":"url_citation","url":"https://example.test","title":"reference","start_index":0,"end_index":2}]`)}
	original := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Content: []p.Node{
		{ID: p.StringValue("first"), Kind: p.TextNode, Payload: p.StringValue("ab"), Metadata: []p.ResponseMetadata{metadata}},
		{ID: p.StringValue("second"), Kind: p.TextNode, Payload: p.StringValue("xy"), Metadata: []p.ResponseMetadata{metadata}},
	}}
	for _, isRenamed := range []bool{false, true} {
		content := []p.Node{original.Content[1], original.Content[0]}
		if isRenamed {
			content[0].ID = p.StringValue("unproven")
		}
		value, _ := p.EncodeValue(content)
		conversion, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "reorder", Rules: []p.ConversionRule{{ID: "items", Enabled: true, Phase: p.ConversionResponse, Action: "set", Path: "/content", Value: value}}})
		if err != nil {
			t.Fatal(err)
		}
		response, err := conversion.Response(t.Context(), original, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.ValidateResponseReferences(t.Context(), response); (err != nil) != isRenamed {
			t.Fatal("reference identity was not preserved across reordering", isRenamed, err)
		}
	}
	value, _ := p.EncodeValue(original)
	fields, _ := value.ReadObject()
	delete(fields, "source")
	value, _ = p.EncodeValue(fields)
	conversion, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "insert", Rules: []p.ConversionRule{{ID: "response", Enabled: true, Phase: p.ConversionEvent, Action: "set", Path: "/response", Value: value}}})
	if err != nil {
		t.Fatal(err)
	}
	event, err := conversion.Event(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished}, p.ConversionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	defer options.State.Release()
	if err := compiled.ValidateEventReferences(t.Context(), event, options); err == nil {
		t.Fatal("inserted response forged its own original reference proof")
	}
}
