package protocol

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/bytedance/sonic"
)

func TestScalarFastPathMatchesDecoder(t *testing.T) {
	inputs := []string{
		`""`, `"user"`, `"<>&"`, `"escaped\ntext"`, `"a"`, `"\ud800"`, `"😀"`, `"中文"`, "\"\xff\"",
		`true`, `false`, `null`, `0`, `-0`, `42`, `-42`, `9223372036854775807`, `-9223372036854775808`,
		`18446744073709551615`, `18446744073709551616`, `-9223372036854775809`, `1.0`, `1e2`, `+1`, `01`, `-01`, `-`,
		`{}`, `[]`, `[1]`, `{"a":1}`, `"unterminated`, `"bad\x"`, "\"raw\nnewline\"", `tru`, `false true`, `null x`, `1 2`, `"x" "y"`,
		` `, " true", "\v42", " \t\n\rnull \t", " \ttrue \r\n", " \n\"user\"\t ",
	}
	for _, kind := range []reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[bool](), reflect.TypeFor[int](), reflect.TypeFor[int64](), reflect.TypeFor[uint64]()} {
		for _, raw := range inputs {
			t.Run(kind.String()+"/"+fmt.Sprintf("%q", raw), func(t *testing.T) {
				got, want := reflect.New(kind), reflect.New(kind)
				switch kind.Kind() {
				case reflect.String:
					got.Elem().SetString("unchanged")
					want.Elem().SetString("unchanged")
				case reflect.Bool:
					got.Elem().SetBool(true)
					want.Elem().SetBool(true)
				case reflect.Int, reflect.Int64:
					got.Elem().SetInt(73)
					want.Elem().SetInt(73)
				case reflect.Uint64:
					got.Elem().SetUint(73)
					want.Elem().SetUint(73)
				}
				expected := sonic.ConfigStd.UnmarshalFromString(raw, want.Interface())
				actual := (Value{raw: raw}).Decode(got.Interface())
				if reflect.TypeOf(actual) != reflect.TypeOf(expected) || fmt.Sprint(actual) != fmt.Sprint(expected) || !reflect.DeepEqual(got.Interface(), want.Interface()) {
					t.Fatalf("decoder mismatch: got %T %v %#v; want %T %v %#v", actual, actual, got.Elem().Interface(), expected, expected, want.Elem().Interface())
				}
			})
		}
		for _, raw := range []string{`null`, `true`, `"a"`, `42`, `bad`} {
			target := reflect.Zero(reflect.PointerTo(kind)).Interface()
			actual := (Value{raw: raw}).Decode(target)
			expected := sonic.ConfigStd.UnmarshalFromString(raw, target)
			if reflect.TypeOf(actual) != reflect.TypeOf(expected) || fmt.Sprint(actual) != fmt.Sprint(expected) {
				t.Fatalf("nil %s: %v / %v", kind, actual, expected)
			}
		}
	}
	if err := (Value{}).Decode(new(string)); err == nil || err.Error() != "cannot decode an absent value" {
		t.Fatal(err)
	}
}

func TestFixedStringValuesMatchCodec(t *testing.T) {
	for _, text := range []string{"", "message", "text", "user", "assistant", "function", "reasoning", "tool_call", "summary", "automatic", "unknown model text", "<>&", "中文", "\xff", "\n"} {
		want, err := sonic.ConfigStd.MarshalToString(text)
		if err != nil || StringValue(text).raw != want {
			t.Fatalf("%q: %q / %q (%v)", text, StringValue(text).raw, want, err)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { _ = StringValue("message") }); allocs != 0 {
		t.Fatalf("constant allocated: %v", allocs)
	}
}
