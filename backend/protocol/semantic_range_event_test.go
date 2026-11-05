package protocol

import (
	"strings"
	"testing"
)

func semanticRangeDocument(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeEvent]
	entry.IsFrameBatch = true
	entry.Program = Expression{Op: "array", Items: []Expression{entry.Program}}
	entry.Effects[0].Target = "/0/response_id"
	var declaration ProtocolDeliveryRange
	if err := parseTestValue(t, `{"name":"text","measure":{"op":"utf16_length","source":{"op":"read"}},"texts":{"op":"if","when":{"op":"present","source":{"op":"read","path":"/delta"}},"then":{"op":"array","items":[{"op":"object","fields":{"key":{"op":"read","path":"/itemId"},"text":{"op":"read","path":"/delta"},"mode":{"op":"literal","value":"append"},"path":{"op":"literal","value":"/delta"}}}]},"otherwise":{"op":"literal","value":[]}},"ranges":{"op":"if","when":{"op":"present","source":{"op":"read","path":"/metadata/0/value"}},"then":{"op":"read","path":"/metadata/0/value"},"otherwise":{"op":"literal","value":[]}}}`).Decode(&declaration); err != nil {
		t.Fatal(err)
	}
	entry.SemanticRanges = []ProtocolDeliveryRange{declaration}
	document.Mapping.Directions[EncodeEvent] = entry
	return document
}

func TestSemanticRangesRejectAuthorityAndStateAccess(t *testing.T) {
	for _, expression := range []Expression{
		{Op: "read", Path: "/source"},
		{Op: "read", Path: "/item/resources"},
		{Op: "read", From: "runtime", Path: "/scope"},
		{Op: "read", From: "state", Path: "/position"},
	} {
		document := semanticRangeDocument(t)
		entry := document.Mapping.Directions[EncodeEvent]
		entry.SemanticRanges[0].Texts = expression
		document.Mapping.Directions[EncodeEvent] = entry
		compiler, err := NewCompiler(DefaultLimits(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		value, err := EncodeValue(document)
		if err != nil {
			t.Fatal(err)
		}
		if _, issues := compiler.Compile(value.Bytes()); IssuesError(issues) == nil {
			t.Fatal("semantic selector acquired undeclared access", expression)
		}
	}
}

func TestSemanticEventRangesPreserveOnlyUnchangedText(t *testing.T) {
	compiled := compilePackageForTest(t, semanticRangeDocument(t))
	for _, fixture := range []struct {
		name, after, originalRange, finalRange string
		reject                                 bool
	}{
		{"identity", "😀abCDxy", `[0,8]`, `[0,8]`, false},
		{"equal-length-rewrite", "😀abEFxy", `[4,6]`, `[4,6]`, true},
		{"prefix", "😀abQxy", `[0,4]`, `[0,4]`, false},
		{"shifted-suffix", "😀abQxy", `[6,8]`, `[5,7]`, false},
		{"stale-suffix-index", "😀abQxy", `[6,8]`, `[6,8]`, true},
		{"cross-rewrite", "😀abQxy", `[3,7]`, `[3,6]`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			options := EvaluationContext{State: NewEvaluationState()}
			defer options.State.Release()
			original := Event{SchemaVersion: 2, Type: ItemDelta, ResponseID: StringValue("r"), ItemID: StringValue("text"), Sequence: coreInteger(0), Delta: StringValue("😀abCDxy")}
			changed := original
			changed.Delta = StringValue(fixture.after)
			if _, err := compiled.EncodeConvertedFrame(t.Context(), &EventFrame{Events: []Event{original}}, []Event{changed}, options); err != nil {
				t.Fatal("unreferenced text rewrite must remain allowed", err)
			}
			key := moduleStateKey{compiled: compiled, direction: EncodeEvent}
			positions := options.State.packageStates[key].semanticRanges
			if positions.stagedBytes != 0 || len(positions.pending) != 0 {
				t.Fatal("encoding retained the current text payload")
			}
			var source, output [2]int
			if err := parseTestValue(t, fixture.originalRange).Decode(&source); err != nil {
				t.Fatal(err)
			}
			if err := parseTestValue(t, fixture.finalRange).Decode(&output); err != nil {
				t.Fatal(err)
			}
			reference := func(bounds [2]int) Event {
				value, err := EncodeValue([]map[string]any{{"key": "text", "id": "reference", "start": bounds[0], "end": bounds[1], "path": "/custom/range"}})
				if err != nil {
					t.Fatal(err)
				}
				return Event{SchemaVersion: 2, Type: ItemDelta, ResponseID: StringValue("r"), ItemID: StringValue("text"), Sequence: coreInteger(1), Delta: StringValue(""),
					Metadata: []ResponseMetadata{{Name: "references", Codec: compiled.Identity().Family, Location: "content", Value: value}}}
			}
			before, after := reference(source), reference(output)
			_, err := compiled.EncodeConvertedFrame(t.Context(), &EventFrame{Events: []Event{before}}, []Event{after}, options)
			if (err != nil) != fixture.reject {
				t.Fatal("reference proof disagrees with unchanged interval", err)
			}
			if err != nil && !strings.Contains(err.Error(), "/custom/range") {
				t.Fatal(err)
			}
		})
	}
}
