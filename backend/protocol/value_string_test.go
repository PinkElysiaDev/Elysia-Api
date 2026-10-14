package protocol

import (
	"bytes"
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
