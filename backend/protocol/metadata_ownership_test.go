package protocol

import (
	"strings"
	"testing"
)

func referenceMetadata(t *testing.T, title string) ResponseMetadata {
	t.Helper()
	return ResponseMetadata{Meaning: "url-references", Codec: "source", Location: "content", Name: "links", Value: fixtureValue(t, []string{title}),
		References: fixtureValue(t, []Object{{"url": StringValue("https://example.invalid"), "title": StringValue(title), "start": coreInteger(0), "end": coreInteger(1)}})}
}

func TestMetadataReferencesSurviveStreamingAppend(t *testing.T) {
	collector, err := NewResponseCollector(reasoningCollectionTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Release()
	start := reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode, Metadata: []ResponseMetadata{referenceMetadata(t, "first")}})
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	update := referenceMetadata(t, "second")
	update.Update = "append"
	delta := reasoningCollectionEvent(ItemDelta, "text", "", "", nil)
	delta.Delta, delta.Metadata = StringValue(""), []ResponseMetadata{update}
	if _, _, err := collector.Consume(delta); err != nil {
		t.Fatal(err)
	}
	snapshot, err := collector.Partial()
	if err != nil {
		t.Fatal(err)
	}
	metadata := snapshot.Content[0].Metadata[0]
	var references []Object
	if err := metadata.References.Decode(&references); err != nil || len(references) != 2 || !references[0]["title"].IsString("first") || !references[1]["title"].IsString("second") {
		t.Fatal("append discarded canonical references", metadata, err)
	}
	if !equalValues(metadata.Value, fixtureValue(t, []string{"first", "second"})) || metadata.Update != "" || delta.Metadata[0].Update != "append" {
		t.Fatal("append changed source metadata or its snapshot", metadata, delta.Metadata)
	}
}

func TestMetadataReferencesShareRetentionBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 1024
	collector, err := NewResponseCollector(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Release()
	if _, _, err := collector.Consume(reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode})); err != nil {
		t.Fatal(err)
	}
	delta := reasoningCollectionEvent(ItemDelta, "text", "", "", nil)
	metadata := referenceMetadata(t, strings.Repeat("r", 200))
	metadata.Value = fixtureValue(t, []int{1})
	metadata.Update = "append"
	delta.Delta, delta.Metadata = StringValue(""), []ResponseMetadata{metadata}
	for range 8 {
		if _, _, err := collector.Consume(delta); err != nil {
			if !hasIssueCode(err, LimitExceeded) {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("canonical references obtained an unmetered retention buffer")
}

func TestMetadataAppendCannotChangeMeaning(t *testing.T) {
	collector, err := NewResponseCollector(reasoningCollectionTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Release()
	metadata := ResponseMetadata{Codec: "source", Location: "content", Name: "values", Meaning: "information", Value: fixtureValue(t, []int{1})}
	if _, _, err := collector.Consume(reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode, Metadata: []ResponseMetadata{metadata}})); err != nil {
		t.Fatal(err)
	}
	metadata.Meaning, metadata.Update = "state", "append"
	delta := reasoningCollectionEvent(ItemDelta, "text", "", "", nil)
	delta.Delta, delta.Metadata = StringValue(""), []ResponseMetadata{metadata}
	if _, _, err := collector.Consume(delta); !hasIssueCode(err, InvalidInput) {
		t.Fatal("append replaced metadata meaning", err)
	}
}

func TestMetadataSemanticsUseMeaningInsteadOfWireName(t *testing.T) {
	custom := ResponseMetadata{Codec: "custom", Name: "logprobs", Location: "response", Value: StringValue("opaque")}
	if err := validateCoreMetadataValue(custom, "/metadata/0"); err != nil {
		t.Fatal("wire field spelling selected a semantic contract", err)
	}
	custom.Meaning = "token-probabilities"
	if err := validateCoreMetadataValue(custom, "/metadata/0"); !hasIssueCode(err, InvalidInput) {
		t.Fatal("declared meaning did not validate its canonical value", err)
	}
	unknown := ResponseMetadata{Codec: "responses", Name: "annotations", Location: "content", Value: fixtureValue(t, []Value{})}
	if len(comparableMetadata([]ResponseMetadata{unknown})) != 1 {
		t.Fatal("wire spelling hid an undeclared metadata value")
	}
	unknown.Meaning, unknown.References = "url-references", fixtureValue(t, []Value{})
	if len(comparableMetadata([]ResponseMetadata{unknown})) != 0 {
		t.Fatal("empty canonical references acquired content")
	}
}
