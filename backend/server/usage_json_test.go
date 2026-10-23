package server

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestUsageJSONPreservesNumbersAndOwnsStrings(t *testing.T) {
	input := `{"number":9007199254740993,"decimal":1.00,"unknown":{"text":"` + strings.Repeat("synthetic", 1024) + `"}}`
	value, ok := decodeUsageJSON(input)
	if !ok {
		t.Fatal("valid log JSON rejected")
	}
	object := value.(map[string]any)
	if object["number"] != json.Number("9007199254740993") || object["decimal"] != json.Number("1.00") {
		t.Fatal("log number spelling or precision changed", object["number"], object["decimal"])
	}
	text := object["unknown"].(map[string]any)["text"].(string)
	if text != strings.Repeat("synthetic", 1024) {
		t.Fatal("unknown log field changed")
	}
	inputStart, textStart := reflect.ValueOf(input).Pointer(), reflect.ValueOf(text).Pointer()
	if textStart >= inputStart && textStart < inputStart+uintptr(len(input)) {
		t.Fatal("decoded field retained the complete input string")
	}
	for _, suffix := range []string{` {"secret":"synthetic"}`, " trailing"} {
		if _, ok := decodeUsageJSON(input + suffix); ok {
			t.Fatalf("trailing content accepted: %q", suffix)
		}
	}
}

func FuzzUsageJSONDecoderParity(f *testing.F) {
	for _, input := range []string{
		"", " \r\n\t", `null`, `false`, `0000`, `[]`, `{}`, `"plain"`, `"\ud800"`,
		`{"a":1,"a":2,"unknown":[null,true,9007199254740993,1.00,1e999]}`,
		`{"nested":"{\"x-codex-turn-state\":\"synthetic\"}"}`,
		`{"valid":true} {"secret":"synthetic"}`, `{"x":"truncated`,
		"{\"invalid\":\"\xff\"}", "{\"key\x00\":true}", "{}\x00", "data: {}\n\n",
	} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		expected, expectedOK := decodeUsageJSONFromReader(input)
		actual, actualOK := decodeUsageJSON(input)
		if actualOK != expectedOK || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("decoder contract changed for %q: got %#v/%t, want %#v/%t", input, actual, actualOK, expected, expectedOK)
		}
	})
}

// Retain the previous reader-based boundary as a differential oracle.
func decodeUsageJSONFromReader(input string) (any, bool) {
	reader := sonic.ConfigStd.NewDecoder(strings.NewReader(input))
	reader.UseNumber()
	var value any
	if reader.Decode(&value) != nil {
		return nil, false
	}
	var trailing any
	if reader.Decode(&trailing) != io.EOF {
		return nil, false
	}
	return value, true
}
