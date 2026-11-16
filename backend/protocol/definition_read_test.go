package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func decodeCompactFixture(t testing.TB, source string) Expression {
	t.Helper()
	var expression Expression
	if err := decodeContract([]byte(source), &expression); err != nil {
		t.Fatal(err)
	}
	return expression
}

func TestCompactReadCodec(t *testing.T) {
	for _, fixture := range []struct {
		text, from, name, path string
	}{
		{"$input", "input", "", ""},
		{"$root/", "root", "", "/"},
		{"$item/a~1b/~0c", "item", "", "/a~1b/~0c"},
		{"$context/中文", "context", "", "/中文"},
		{"$runtime/scope/model", "runtime", "", "/scope/model"},
		{"$state/counter/value", "state", "", "/counter/value"},
		{"$binding/message", "binding", "message", ""},
		{"$binding/a~1b~0c/", "binding", "a/b~c", "/"},
		{"$binding/中文~1名/~01", "binding", "中文/名", "/~01"},
		{"$input/line\nbreak", "input", "", "/line\nbreak"},
	} {
		t.Run(fixture.text, func(t *testing.T) {
			raw, err := json.Marshal(fixture.text)
			if err != nil {
				t.Fatal(err)
			}
			expression := decodeCompactFixture(t, string(raw))
			if expression.Op != "read" || expression.From != fixture.from || expression.Name != fixture.name || expression.Path != fixture.path || !expression.isCompactRead {
				t.Fatalf("incorrect expansion: %#v", expression)
			}
			for _, field := range []string{"op", "from", "path"} {
				if !expression.present.has(field) {
					t.Fatal("expanded field presence missing", field)
				}
			}
			if expression.present.has("name") != (fixture.from == "binding") {
				t.Fatal("binding name presence changed")
			}
			for _, encode := range []func(any) ([]byte, error){json.Marshal, sonic.ConfigStd.Marshal} {
				encoded, err := encode(expression)
				if err != nil {
					t.Fatal(err)
				}
				var text string
				if err := json.Unmarshal(encoded, &text); err != nil || text != fixture.text {
					t.Fatal("compact form did not round trip", string(encoded), err)
				}
				if got := decodeCompactFixture(t, string(encoded)); !reflect.DeepEqual(got, expression) {
					t.Fatal("expanded fields changed on round trip")
				}
			}
		})
	}
}

func TestCompactReadRejectsMalformedDeclarations(t *testing.T) {
	for _, text := range []string{"", "input", "/model", "$", "$Input", "$inputx/model", "$unknown/x", "$input:field", "$binding", "$binding/", "$binding//x", "$binding/name~2/x", "$input/~", "$input/~2", "$input/#~x"} {
		raw, _ := json.Marshal(text)
		var expression Expression
		if err := decodeContract(raw, &expression); err == nil {
			t.Fatal("malformed compact expression accepted", text)
		}
	}
	for _, raw := range []string{`"$input" false`, `{"op":"read","surprise":false}`, `{"op":"read","from":"input","shorthand":"$input"}`} {
		var expression Expression
		if err := decodeContract([]byte(raw), &expression); err == nil {
			t.Fatal("invalid mixed expression accepted", raw)
		}
	}
}

func TestCompactReadPreservesObjectAndOpaqueValues(t *testing.T) {
	for _, source := range []string{`{"op":"read","path":"/model"}`, `{"op":"read","required":true}`, `{"op":"read","required":false}`} {
		expression := decodeCompactFixture(t, source)
		encoded, err := EncodeValue(expression)
		if err != nil || !encoded.IsObject() || expression.isCompactRead {
			t.Fatal("object declaration was automatically compacted", encoded, err)
		}
	}
	for _, source := range []string{
		`{"op":"literal","value":"$input/not-a-read"}`,
		`{"op":"literal","value":{"op":"read","from":"not-a-scope"}}`,
		`{"op":"enum","source":"$input","values":{"literal":"$binding/","object":{"op":"read","path":"not-a-pointer"}}}`,
	} {
		expression := decodeCompactFixture(t, source)
		encoded, err := EncodeValue(expression)
		if err != nil {
			t.Fatal(err)
		}
		roundtrip := decodeCompactFixture(t, encoded.JSONString())
		if !reflect.DeepEqual(expression.Value, roundtrip.Value) || !reflect.DeepEqual(expression.Values, roundtrip.Values) {
			t.Fatal("literal or enum data was interpreted as an expression")
		}
	}
	var document struct {
		Samples []Sample    `json:"samples"`
		Schema  ValueSchema `json:"schema"`
	}
	raw := `{"samples":[{"id":"opaque","input":"$binding/","expected":{"op":"read","from":"invalid"}}],"schema":{"type":"string","enum":["$input/~invalid"]}}`
	if err := decodeContract([]byte(raw), &document); err != nil {
		t.Fatal("sample/schema data was parsed as expression syntax", err)
	}
	if !document.Samples[0].Input.IsString("$binding/") || !document.Schema.Enum[0].IsString("$input/~invalid") {
		t.Fatal("opaque values changed")
	}
}

func TestCompactReadMutationKeepsInvalidFieldsVisible(t *testing.T) {
	for _, fixture := range []struct {
		name              string
		source            string
		change            func(*Expression)
		isError, isObject bool
	}{
		{"path", `"$input/original"`, func(e *Expression) { e.Path = "/changed" }, false, false},
		{"required", `"$input"`, func(e *Expression) { e.Required = true }, false, true},
		{"invalid-source", `"$input"`, func(e *Expression) { e.From = "invalid" }, false, true},
		{"invalid-pointer", `"$input"`, func(e *Expression) { e.Path = "/bad~2" }, false, true},
		{"unknown-field", `"$input"`, func(e *Expression) { e.unknownKeys = []string{"typo"} }, true, false},
		{"unrelated-source", `"$input"`, func(e *Expression) { e.Source = expressionPointer(literalExpression(1)) }, true, false},
		{"unrelated-fields", `"$input"`, func(e *Expression) { e.Fields = map[string]Expression{"x": literalExpression(1)} }, true, false},
		{"changed-operation", `"$input"`, func(e *Expression) { e.Op, e.From = "literal", ""; e.Value = StringValue("payload") }, true, false},
		{"emptied-name-after-scope-change", `"$binding/old"`, func(e *Expression) { e.From, e.Name = "input", "" }, true, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			expression := decodeCompactFixture(t, fixture.source)
			fixture.change(&expression)
			encoded, err := EncodeValue(expression)
			if (err != nil) != fixture.isError {
				t.Fatal("edited expression was normalized incorrectly", encoded, err)
			}
			if err != nil {
				return
			}
			if encoded.IsObject() != fixture.isObject {
				t.Fatal("incorrect authored representation", encoded)
			}
			if fixture.name == "invalid-source" || fixture.name == "invalid-pointer" {
				decoded := decodeCompactFixture(t, encoded.JSONString())
				if _, err := newExpressionCompiler(DefaultLimits(), nil).compilePlan(decoded, "/program", expressionScope{}, 1); err == nil {
					t.Fatal("invalid edit became executable after encoding")
				}
			}
			if fixture.name == "required" && !decodeCompactFixture(t, encoded.JSONString()).Required {
				t.Fatal("required read became optional")
			}
			if fixture.name == "path" && !encoded.IsString("$input/changed") {
				t.Fatal("old shorthand concealed the updated path")
			}
		})
	}
}

func TestCompactReadSchemaCatalog(t *testing.T) {
	for name, root := range map[string]map[string]any{"package": DescribeSchema().Package, "spec": DescribeSchema().Spec, "mapping": DescribeSchema().Mapping} {
		t.Run(name, func(t *testing.T) {
			schema := resolveCatalogSchema(t, map[string]any{"$ref": "#/$defs/Expression", "$defs": root["$defs"]})
			for _, text := range []string{"$input", "$root/", "$item/a~1b", "$context/中文", "$runtime/scope/model", "$state/slot", "$binding/a~1b~0c/~01"} {
				if err := schema.Validate(text); err != nil {
					t.Fatal("compact declaration absent from schema", text, err)
				}
			}
			for _, text := range []string{"$invalid", "$binding/", "$input/~2", "$binding/a~x/path"} {
				if err := schema.Validate(text); err == nil {
					t.Fatal("malformed compact declaration allowed by schema", text)
				}
			}
			if err := schema.Validate(map[string]any{"op": "object", "fields": map[string]any{"model": "$input/model"}}); err != nil {
				t.Fatal("recursive expression union not exposed", err)
			}
		})
	}
	if !strings.Contains(ExpressionCatalog()[0].Description, "$binding/") {
		t.Fatal("read catalog omitted compact authoring")
	}
}
