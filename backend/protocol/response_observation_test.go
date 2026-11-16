package protocol

import (
	"strings"
	"testing"
)

func TestResponseObservationCoreValues(t *testing.T) {
	for _, fixture := range []struct {
		meaning, raw, failure string
	}{
		{metadataImageCompression, "false", ""},
		{metadataImageCompression, "true", ""},
		{metadataImageCompression, "null", ""},
		{metadataImageCompression, `"disabled"`, "/observation"},
		{metadataImageCompression, `0`, "/observation"},
		{metadataImageCompression, `{}`, "/observation"},
		{metadataInputTransformations, `[]`, ""},
		{metadataInputTransformations, `null`, ""},
		{metadataInputTransformations, `[{"sourceItemId":"source-1","kind":"reasoning","action":"remove","reason":"model-mismatch"}]`, ""},
		{metadataInputTransformations, `[{"sourceItemId":"","kind":"reasoning","action":"remove","reason":"model-mismatch"}]`, "/0/sourceItemId"},
		{metadataInputTransformations, `[{"sourceItemId":"source-1","kind":"reasoning","action":"removed","reason":"model-mismatch"}]`, "/0/action"},
		{metadataInputTransformations, `[{"sourceItemId":"source-1","kind":"text","action":"remove","reason":"model-mismatch"}]`, "/0/kind"},
		{metadataInputTransformations, `[{"sourceItemId":"source-1","kind":"reasoning","action":"remove","reason":"model_mismatch"}]`, "/0/reason"},
		{metadataInputTransformations, `[{"sourceItemId":"source-1","kind":"reasoning","action":"remove","reason":"model-mismatch","scope":"invented"}]`, "/0"},
		{metadataInputTransformations, `{}`, "/observation"},
	} {
		t.Run(fixture.meaning+"/"+fixture.raw, func(t *testing.T) {
			value, err := ParseValue([]byte(fixture.raw))
			if err != nil {
				t.Fatal(err)
			}
			field := ResponseMetadata{Meaning: fixture.meaning, Location: "response", Path: "/observation", Value: value}
			err = validateCoreMetadataValue(field, "/metadata/0")
			if fixture.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
				field.Location = "content"
				if validateCoreMetadataValue(field, "/metadata/0") == nil {
					t.Fatal("response observation acquired a content owner")
				}
			} else if err == nil || !strings.Contains(err.Error(), fixture.failure) {
				t.Fatal("invalid canonical observation lost its precise path", err)
			}
		})
	}
}
