package jsonutil

import (
	"testing"

	"github.com/bytedance/sonic/decoder"
)

func TestValidRejectsMalformedJSON(t *testing.T) {
	for _, raw := range []string{"", " ", "n", "nu", "nul", "t", "tr", "tru", "f", "fa", "fal", "fals", "[n]", `{"n":n}`, `{} {}`, `"\0"`, `"\v"`, `"\u000x"`, `"\u123"`, "\"line\nbreak\"", "\"\x00\"", "\"\\\xf1\xe6\xc4\xd20\x8e\x8e\x8e\x8e00000\""} {
		if Valid([]byte(raw)) {
			t.Errorf("accepted invalid JSON %q", raw)
		}
	}
	for _, literal := range []string{"null", "true", "false"} {
		data := []byte(literal)
		for size := 1; size < len(data); size++ {
			if Valid(data[:size]) {
				t.Errorf("read past slice length: %q[:%d]", literal, size)
			}
		}
	}
}

func TestValidPreservesNumberRangeAndStringSemantics(t *testing.T) {
	for _, raw := range []string{`"\\0"`, `"\u1234"`, `"\"\\\/\b\f\n\r\t"`, `"\ud800"`, "\"\xff\"", `{"a":1e9999}`, `{"n":9007199254740993,"n":1e+02}`} {
		if !Valid([]byte(raw)) {
			t.Errorf("rejected valid JSON %q", raw)
		}
	}
}

func FuzzValidCompatibility(f *testing.F) {
	for _, raw := range []string{`null`, `true`, `false`, `"\\0"`, `"\0"`, "\"\\\xf1\"", `{"n":1e9999,"n":9007199254740993}`, "\"\xff\""} {
		f.Add([]byte(raw), uint16(len(raw)))
	}
	f.Fuzz(func(t *testing.T, backing []byte, length uint16) {
		if len(backing) > 65536 {
			t.Skip()
		}
		data := backing[:min(len(backing), int(length))]
		// Retain the former materializing validator as an independent oracle.
		reader := decoder.NewDecoder(string(data))
		reader.UseNumber()
		reader.ValidateString()
		var decoded any
		want := reader.Decode(&decoded) == nil && reader.CheckTrailings() == nil
		if got := Valid(data); got != want {
			t.Fatalf("Valid(%q) = %v; want %v", data, got, want)
		}
	})
}
