package protocol

import (
	"strings"
	"testing"
)

func TestReadStringMatchesCodec(t *testing.T) {
	for _, raw := range []string{`""`, `"plain"`, " \n\"世界🙂\"\t ", `"<>&"`, `"a\"b\\c\n"`, `"\ud800"`, `"\ud83d\ude42"`, `"line` + "\u2028" + `separator"`, string([]byte{'"', 0xff, '"'})} {
		value := Value{raw: raw}
		var expected string
		if err := value.Decode(&expected); err != nil {
			t.Fatal(err)
		}
		actual, err := readString(value)
		if err != nil || actual != expected {
			t.Fatalf("string %q: got %q, %v; want %q", raw, actual, err, expected)
		}
	}
	for _, value := range []Value{{}, {raw: "null"}, {raw: "true"}, {raw: "1"}, {raw: "[]"}, {raw: "{}"}} {
		if _, err := readString(value); err == nil {
			t.Fatalf("non-string accepted: %s", value.raw)
		}
	}
}

func TestReadStringDetachesSmallIdentity(t *testing.T) {
	frame := Value{raw: strings.Repeat(" ", 64<<10) + `"identity"`}
	identity, err := readString(frame)
	if err != nil || identity != "identity" || sharesValueStorage(frame, Value{raw: identity}) {
		t.Fatal("small identity retained its frame", err)
	}
}
