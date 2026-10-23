package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

func assertUsageEncodingResult(t testing.TB, actual, expected []byte, actualErr, expectedErr error) {
	t.Helper()
	if !bytes.Equal(actual, expected) || (actual == nil) != (expected == nil) {
		t.Fatalf("log JSON bytes differ: got %q, want %q", actual, expected)
	}
	if reflect.TypeOf(actualErr) != reflect.TypeOf(expectedErr) || (actualErr != nil && actualErr.Error() != expectedErr.Error()) {
		t.Fatalf("log JSON errors differ: got %T %v, want %T %v", actualErr, actualErr, expectedErr, expectedErr)
	}
}

func assertUsageEncodingParity(t testing.TB, value any) {
	t.Helper()
	expected, expectedErr := sonic.ConfigStd.Marshal(value)
	actual, actualErr := marshalUsageJSON(value)
	assertUsageEncodingResult(t, actual, expected, actualErr, expectedErr)
}

func TestUsageJSONEncodingParity(t *testing.T) {
	started := time.Date(2026, time.October, 23, 1, 2, 3, 4, time.UTC)
	for _, test := range []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"empty-string", ""},
		{"plain", "synthetic base64 AAABBB+++///=="},
		{"html", "<script>&\u2028\u2029</script>"},
		{"escaped-html", `\u003c\u003e\u0026\u2028\u2029`},
		{"quotes-and-controls", "\"\\\n\r\t\x00\x01<>"},
		{"large-integer", json.Number("9007199254740993")},
		{"decimal-spelling", json.Number("1.00")},
		{"invalid-number", json.Number("01")},
		{"nan", math.NaN()},
		{"infinity", math.Inf(1)},
		{"unsupported", make(chan int)},
		{"nil-json-marshaler", (*usageJSONMarshaler)(nil)},
		{"nil-text-marshaler", (*usageTextMarshaler)(nil)},
		{"containers", map[string]any{
			"z": []any{nil, true, json.Number("1.00")},
			"a": map[string]any{}, "empty": []any{}, "nil": []any(nil),
			"<\u2028\xff": "&\u2029\xff", "nested": `{"text":"<>&","n":9007199254740993}`,
		}},
		{"record", &usageRecord{
			RequestID: "synthetic", StartedAt: started, EndedAt: started.Add(time.Second),
			StatusCode: 200, RequestWarnings: []string{"<>&\u2028\u2029\xff"},
			IncomingBody:       usageBody{Content: `{"n":9007199254740993,"text":"<>&"}`},
			OutgoingBody:       usageBody{Content: "invalid UTF-8 \xff\u2028", Truncated: true},
			ProviderResponse:   usageBody{Content: `{"answer":"<>&","decimal":1.00}`},
			DownstreamResponse: usageBody{Content: "data: {\"delta\":\"\u2028\u2029\"}\n\n"},
			Delivery:           &httpDelivery{Outcome: deliveryFailed, Cause: "synthetic", Error: "delivery <>&\xff"},
			ConversionIssues:   []protocol.ConversionIssue{{Code: protocol.ConversionDegraded, Severity: protocol.SeverityWarning, Path: "/synthetic", Reason: "detail <>&\u2028\xff"}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) { assertUsageEncodingParity(t, test.value) })
	}
}

func TestUsageJSONEncodingUnicodeBoundaries(t *testing.T) {
	invalid := []string{
		"\x80", "\xff", "\xc2", "\xe2\x80", "\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80",
	}
	// Both sides of SIMD boundaries are included; the opening JSON quote adds
	// one byte to each string's position in the encoded document.
	for _, offset := range []int{0, 14, 15, 16, 30, 31, 32, 62, 63, 64} {
		for i, malformed := range invalid {
			t.Run(fmt.Sprintf("offset=%d/invalid=%d", offset, i), func(t *testing.T) {
				prefix := strings.Repeat("x", offset)
				for _, special := range []string{"<", ">", "&", "\u2028", "\u2029"} {
					assertUsageEncodingParity(t, prefix+malformed+special+malformed)
					assertUsageEncodingParity(t, prefix+special+malformed+special)
				}
			})
		}
	}
	// More than 4096 invalid bytes requires multiple UTF-8 correction batches.
	assertUsageEncodingParity(t, strings.Repeat("\xff<\u2028", 5000))
}

func TestUsageJSONEncodingRawMessages(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil", nil},
		{"empty", json.RawMessage{}},
		{"null", json.RawMessage("null")},
		{"whitespace", json.RawMessage(" \n { \"number\": 9007199254740993, \"decimal\": 1.00, \"text\": \"<>&\u2028\u2029\" } \t")},
		{"duplicate-keys", json.RawMessage(`{"a":1,"a":2}`)},
		{"escaped", json.RawMessage(`"\ud800\u2028\u003c"`)},
		{"invalid-utf8", json.RawMessage("\"\xff<\xe2\x80\xa8\xc2&\"")},
		{"syntax-error", json.RawMessage(`{"text":}`)},
		{"trailing-json", json.RawMessage(`{"a":1}{"secret":"synthetic"}`)},
		{"trailing-nul", json.RawMessage("{}\x00")},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := bytes.Clone(test.raw)
			assertUsageEncodingParity(t, test.raw)
			assertUsageEncodingParity(t, struct {
				Raw json.RawMessage `json:"raw"`
			}{test.raw})
			if !bytes.Equal(before, test.raw) {
				t.Fatal("encoding modified caller-owned RawMessage")
			}
		})
	}
}

type usageJSONMarshaler struct {
	result []byte
	err    error
	calls  int
}

func (value *usageJSONMarshaler) MarshalJSON() ([]byte, error) {
	value.calls++
	return value.result, value.err
}

type usageTextMarshaler struct {
	result []byte
	err    error
	calls  int
}

func (value *usageTextMarshaler) MarshalText() ([]byte, error) {
	value.calls++
	return value.result, value.err
}

func TestUsageJSONEncodingMarshalerErrorsAndCalls(t *testing.T) {
	sentinel := errors.New("synthetic marshaler failure")
	for _, marshaler := range []struct {
		name   string
		create func([]byte, error) (any, func() int)
	}{
		{"json", func(result []byte, err error) (any, func() int) {
			value := &usageJSONMarshaler{result: result, err: err}
			return value, func() int { return value.calls }
		}},
		{"text", func(result []byte, err error) (any, func() int) {
			value := &usageTextMarshaler{result: result, err: err}
			return value, func() int { return value.calls }
		}},
	} {
		for _, test := range []struct {
			name   string
			result string
			err    error
		}{
			{"html", ` { "html": "<>&" } `, nil},
			{"invalid-utf8", "\"\xff\u2028<&\"", nil},
			{"empty", "", nil},
			{"syntax-error", `{"value":}`, nil},
			{"returned-error", `"<ignored>"`, sentinel},
		} {
			t.Run(marshaler.name+"/"+test.name, func(t *testing.T) {
				expectedValue, expectedCalls := marshaler.create([]byte(test.result), test.err)
				actualValue, actualCalls := marshaler.create([]byte(test.result), test.err)
				expected, expectedErr := sonic.ConfigStd.Marshal(expectedValue)
				actual, actualErr := marshalUsageJSON(actualValue)
				assertUsageEncodingResult(t, actual, expected, actualErr, expectedErr)
				if actualCalls() != 1 || expectedCalls() != 1 {
					t.Fatalf("marshaler calls = %d, baseline %d; want exactly one", actualCalls(), expectedCalls())
				}
				if errors.Is(actualErr, sentinel) != errors.Is(expectedErr, sentinel) {
					t.Fatal("marshaler error chain changed")
				}
			})
		}
	}
}

func FuzzUsageJSONEncodingParity(f *testing.F) {
	for _, input := range []string{
		"", "plain", "<>&\u2028\u2029", `\u2028\u2029`, "\xff<\xc2\u2028\xed\xa0\x80&",
		`{"number":9007199254740993,"decimal":1.00}`, `{"a":1,"a":2}`, `"\ud800"`, "{}\x00", `{"x":}`,
	} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		assertUsageEncodingParity(t, string(input))
		assertUsageEncodingParity(t, map[string]any{string(input): []any{string(input), json.Number("1.00")}})
		assertUsageEncodingParity(t, json.RawMessage(input))
	})
}

func BenchmarkUsageJSONEncoding(b *testing.B) {
	for _, test := range []struct {
		name     string
		size     int
		suffix   string
		isRecord bool
	}{
		{"small", 256, "", false},
		{"small-html", 256, "<>&\u2028\u2029", false},
		{"body-2MiB", 2 << 20, "", false},
		{"body-31MiB", 31 << 20, "", false},
		{"body-31MiB-html-tail", 31 << 20, "<>&\u2028\u2029", false},
		{"record-two-31MiB-bodies", 31 << 20, "", true},
	} {
		b.Run(test.name, func(b *testing.B) {
			text := strings.Repeat("A", test.size) + test.suffix
			var value any = map[string]any{"source": map[string]any{"type": "base64", "data": text}, "number": json.Number("9007199254740993")}
			if test.isRecord {
				body := `{"data":"` + text + `"}`
				value = &usageRecord{RequestID: "synthetic", StatusCode: 200, IncomingBody: usageBody{Content: body}, OutgoingBody: usageBody{Content: body}}
			}
			expected, err := sonic.ConfigStd.Marshal(value)
			if err != nil {
				b.Fatal(err)
			}
			for _, implementation := range []struct {
				name    string
				marshal func(any) ([]byte, error)
			}{
				{"baseline", sonic.ConfigStd.Marshal},
				{"conditional", marshalUsageJSON},
			} {
				b.Run(implementation.name, func(b *testing.B) {
					actual, err := implementation.marshal(value)
					if err != nil || !bytes.Equal(actual, expected) {
						b.Fatal("benchmark output differs from baseline", err)
					}
					b.ReportAllocs()
					b.SetBytes(int64(len(expected)))
					b.ResetTimer()
					for b.Loop() {
						encoded, err := implementation.marshal(value)
						if err != nil || len(encoded) != len(expected) {
							b.Fatal("benchmark encoding failed", err)
						}
					}
				})
			}
		})
	}
}
