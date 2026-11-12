package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestStringComparisonPreservesWireBytes(t *testing.T) {
	for _, raw := range []string{`"failed"`, `"\u0066ailed"`, ` "failed" `} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if !value.IsString("failed") || CheckGenerationOutcome(&Response{Status: value}) == nil {
			t.Fatal("equivalent failure not recognized")
		}
		if !bytes.Equal(value.Bytes(), []byte(raw)) {
			t.Fatal("wire spelling changed")
		}
	}
	for _, raw := range []string{`null`, `false`, `0`, `[]`, `{}`} {
		value, _ := ParseValue([]byte(raw))
		if value.IsString("") {
			t.Fatalf("%s matched empty string", raw)
		}
	}
	if (Value{}).IsString("") {
		t.Fatal("absent matched empty string")
	}
}

func TestEmptyStringComparison(t *testing.T) {
	for _, test := range []struct {
		raw   string
		empty bool
	}{
		{raw: `""`, empty: true}, {raw: " \t\n\r" + `""` + " \n", empty: true},
		{raw: `" "`}, {raw: `"\u0000"`}, {raw: `"\u0020"`},
		{raw: `"\\u0000"`}, {raw: `null`}, {raw: `0`}, {raw: `false`},
		{raw: `[]`}, {raw: `{}`}, {raw: `"` + strings.Repeat("x", 2<<20) + `"`},
	} {
		value, err := ParseValue([]byte(test.raw))
		if err != nil {
			t.Fatal(err)
		}
		if got := value.IsString(""); got != test.empty {
			t.Fatalf("%q: got %v", test.raw, got)
		}
		if value.JSONString() != test.raw {
			t.Fatal("comparison changed wire bytes")
		}
	}
	if (Value{}).IsString("") {
		t.Fatal("absent matched empty")
	}
}
