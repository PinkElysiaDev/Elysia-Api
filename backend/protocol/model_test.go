package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func parseTestValue(t *testing.T, raw string) Value {
	t.Helper()
	value, err := ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestValuePresenceAndPrecision(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `0`, `""`, `[]`, `{}`, `9007199254740993123456789`, `1.234567890123456789e30`} {
		t.Run(raw, func(t *testing.T) {
			value := parseTestValue(t, raw)
			if value.IsZero() {
				t.Fatal("present value classified as missing")
			}
			encoded, err := json.Marshal(struct {
				Field Value `json:"field,omitzero"`
			}{value})
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != `{"field":`+raw+`}` {
				t.Fatalf("representation changed: %s", encoded)
			}
			copied := value.Bytes()
			copied[0] = 'x'
			if string(value.Bytes()) != raw {
				t.Fatal("native value mutable through Bytes")
			}
		})
	}
	encoded, err := json.Marshal(struct {
		Field Value `json:"field,omitzero"`
	}{})
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("missing field: %s %v", encoded, err)
	}
	var decoded any
	if err := parseTestValue(t, `9007199254740993123456789`).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != json.Number("9007199254740993123456789") {
		t.Fatalf("number converted: %#v", decoded)
	}
	if _, err := json.Marshal(Object{"bad": {}}); err == nil {
		t.Fatal("absent value silently became null")
	}
}

func TestOrderedContractRoundTrip(t *testing.T) {
	native := `{"type":"custom_tool_call","name":"patch","vendor":{"exact":9007199254740993123,"nullable":null}}`
	request := Request{
		SchemaVersion: SemanticSchemaVersion,
		Source:        Identity{Family: "responses", WireVersion: "v1", DefinitionID: "my-wire", Revision: "revision-3"},
		Model:         StringValue("m"),
		Content: []Node{
			{Kind: MessageNode, Role: StringValue("developer"), Children: []Node{{Kind: TextNode, Payload: StringValue("prefix")}}},
			{Kind: ToolCallNode, CallID: StringValue("c1"), Name: StringValue("patch"), Input: &ToolInput{Kind: TextInput, Value: StringValue("free text\nnot JSON")}, Native: &Native{Source: Provenance{Path: "/input/1", Direction: DecodeRequest}, Value: parseTestValue(t, native)}},
			{Kind: ToolResultNode, CallID: StringValue("c1"), Payload: parseTestValue(t, `null`)},
		},
		Tools:      []Tool{{Kind: FreeTextTool, Name: StringValue("patch"), Format: parseTestValue(t, `{"type":"text"}`)}},
		Parameters: Object{"seed": parseTestValue(t, `9007199254740993123`), "stream": parseTestValue(t, `false`)},
		Cache:      []CacheIntent{{Kind: "resource", Location: "request", Resource: &Resource{Kind: "cache", ID: StringValue("cache/1"), Scope: Scope{Provider: "p", Account: "account-a", Model: "m"}}}},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequestContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, *decoded) {
		t.Fatalf("contract changed: %s", encoded)
	}
	if !strings.Contains(string(encoded), native) {
		t.Fatalf("native JSON altered: %s", encoded)
	}
	for _, bad := range []string{`{"schemaVersion":2}`, `{"schemaVersion":1,"typo":true}`, string(encoded) + ` {}`} {
		if _, err := DecodeRequestContract([]byte(bad)); err == nil {
			t.Fatalf("invalid contract accepted: %s", bad)
		}
	}
}

func TestUsagePresenceAndOrigin(t *testing.T) {
	usage := Usage{Input: &Counter{Count: 0, Origin: ObservedCount}, Total: &Counter{Count: 7, Origin: InferredCount}}
	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	var restored Usage
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Input == nil || restored.Input.Count != 0 || restored.CacheRead != nil || restored.Total.Origin != InferredCount {
		t.Fatalf("usage provenance lost: %s", encoded)
	}
}
