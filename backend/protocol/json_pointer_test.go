package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONPointerSonicContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, pointer, want string
		fail                     bool
	}{
		{"large-number", `{"n":9007199254740993123}`, "/n", "9007199254740993123", false},
		{"exponent", `{"n":1.2300e+10000}`, "/n", "1.2300e+10000", false},
		{"negative-zero", `[-0]`, "/0", "-0", false},
		{"null", `{"v":null}`, "/v", "null", false},
		{"false", `{"v":false}`, "/v", "false", false},
		{"empty-string", `{"v":""}`, "/v", `""`, false},
		{"missing", `{"v":null}`, "/missing/child", "", false},
		{"past-array", `[1]`, "/2/child", "", false},
		{"null-parent", `{"v":null}`, "/v/child", "", true},
		{"scalar-parent", `{"v":false}`, "/v/child", "", true},
		{"numeric-object-key", `{"01":"yes"}`, "/01", `"yes"`, false},
		{"leading-zero-index", `[0,1]`, "/01", "", true},
		{"signed-index", `[0,1]`, "/+1", "", true},
		{"negative-index", `[0,1]`, "/-1", "", true},
		{"append-index", `[0,1]`, "/-", "", true},
		{"overflow-index", `[]`, "/99999999999999999999999999999999", "", true},
		{"large-missing-index", `[]`, "/9223372036854775807", "", false},
		{"escaped-path", `{"a/b":{"~key":{"":0}}}`, "/a~1b/~0key/", "0", false},
		{"duplicate", `{"k":{"x":1},"k":{"x":2}}`, "/k/x", "2", false},
		{"escaped-duplicate", `{"k":{"x":1},"\u006b":{"x":2}}`, "/k/x", "2", false},
		{"duplicate-null", `{"k":{"x":1},"k":null}`, "/k/x", "", true},
		{"surrogate", `{"\ud83d\ude00":42}`, "/😀", "42", false},
		{"invalid-surrogate", `{"\ud800":42}`, "/�", "42", false},
		{"invalid-utf8", "{\"\xff\xfe\":42}", "/��", "42", false},
		{"raw-fragment", " \n {\"v\": { \"n\" : [1, 2] }} \t", "/v", `{ "n" : [1, 2] }`, false},
		{"identity", " {\"a\":1} ", "", " {\"a\":1} ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := ParseValue([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			pointer, err := parsePointer(tc.pointer)
			if err != nil {
				t.Fatal(err)
			}
			value, err := readValuePointer(root, pointer)
			if (err != nil) != tc.fail || (!tc.fail && value.raw != tc.want) {
				t.Fatalf("got %q, %v; want %q failure=%v", value.raw, err, tc.want, tc.fail)
			}
		})
	}
}

func TestJSONPointerOwnsSelectedFragment(t *testing.T) {
	root, err := ParseValue([]byte(`{"blob":"` + strings.Repeat("A", 1<<20) + `","selected":{"n":9007199254740993123}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{{"selected"}, {"selected", "n"}, {"blob"}} {
		child, err := readValuePointer(root, path)
		if err != nil || child.IsZero() || sharesValueStorage(root, child) {
			t.Fatalf("selected result retains input: %v %v", path, err)
		}
	}
	for i := 0; i < 8; i++ {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			value, err := readValuePointer(root, []string{"selected", "n"})
			if err != nil || value.raw != "9007199254740993123" {
				t.Fatalf("concurrent read: %v %v", value, err)
			}
		})
	}
}

func TestJSONPointerSkipsDeepSibling(t *testing.T) {
	t.Parallel()
	// Sonic validation from #49 counts the enclosing object/array towards its
	// 4096-container limit. Exercise the deepest accepted sibling, not invalid
	// input that the previous standard-library validator happened to accept.
	for _, depth := range []int{128, 4094} {
		raw := `{"skip":` + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth) + `,"value":1}`
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := readValuePointer(value, []string{"value"})
		if err != nil || selected.raw != "1" {
			t.Fatalf("depth %d: %s %v", depth, selected.raw, err)
		}
		array, err := ParseValue([]byte("[" + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth) + ",1]"))
		if err != nil {
			t.Fatal(err)
		}
		selected, err = readValuePointer(array, []string{"1"})
		if err != nil || selected.raw != "1" {
			t.Fatalf("array depth %d: %s %v", depth, selected.raw, err)
		}
	}
}

// Compare top-level lookups to the standard decoder's last-key-wins contract,
// including escaped/invalid Unicode keys. It remains test-only, not a fallback.
func FuzzJSONPointerObjectLookup(f *testing.F) {
	for _, raw := range []string{`{"a":1,"a":null}`, `{"a/b":false,"\u0061":9007199254740993123}`, `{"\ud800":{},"�":[]}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		root, err := ParseValue([]byte(raw))
		if err != nil || !root.IsObject() {
			t.Skip()
		}
		var expected map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &expected); err != nil {
			t.Fatal(err)
		}
		for key, want := range expected {
			got, err := readValuePointer(root, []string{key})
			if err != nil || got.raw != string(want) {
				t.Fatalf("key %q: got %q err=%v want=%q", key, got.raw, err, want)
			}
		}
	})
}
