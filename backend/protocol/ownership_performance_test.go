package protocol

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkValueDecodeLarge(b *testing.B) {
	value := StringValue(strings.Repeat("payload", 1<<16))
	value, _ = EncodeValue(Object{"payload": value, "number": {raw: "9007199254740993"}})
	b.ReportAllocs()
	for b.Loop() {
		var out map[string]any
		if err := value.Decode(&out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValueContainerEncoding(b *testing.B) {
	for _, size := range []int{4, 16, 32} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			object := Object{}
			for i := range size {
				object[strings.Repeat("k", i+1)] = StringValue("value")
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := EncodeValue(object); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSemanticClone(b *testing.B) {
	request := &Request{SchemaVersion: 1, Model: StringValue("m")}
	for range 128 {
		request.Content = append(request.Content, Node{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("immutable history")}}})
	}
	value := reflect.ValueOf(request)
	plan := typedLimitPlans[value.Type()]
	b.ReportAllocs()
	for b.Loop() {
		_ = cloneSemantic(value, plan)
	}
}

func TestDirectValueDecodeOwnsStringsAndNumbers(t *testing.T) {
	value, err := ParseValue([]byte(`{"payload":"` + strings.Repeat("A", 1<<20) + `","small":"owned","number":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := value.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["number"] != json.Number("9007199254740993") {
		t.Fatal("number spelling changed")
	}
	for _, key := range []string{"small", "payload"} {
		if sharesValueStorage(value, Value{raw: out[key].(string)}) {
			t.Fatal("decoded string retains input storage", key)
		}
	}
}

func TestCloneSemanticIntoOwnsEveryMutableContainer(t *testing.T) {
	value := StringValue("immutable")
	for _, root := range []any{&Request{}, &Response{}, &[]Event{{}}} {
		fillMeterValue(reflect.ValueOf(root).Elem(), value, 0)
		if events, ok := root.(*[]Event); ok {
			root = *events
		}
		original := reflect.ValueOf(root)
		cloned := cloneSemantic(original, typedLimitPlans[original.Type()])
		if !reflect.DeepEqual(root, cloned.Interface()) {
			t.Fatal("clone changed semantic contents")
		}
		var check func(reflect.Value, reflect.Value)
		check = func(a, b reflect.Value) {
			if a.Type() == reflect.TypeFor[Value]() {
				return
			}
			switch a.Kind() {
			case reflect.Pointer:
				if !a.IsNil() {
					if a.Pointer() == b.Pointer() {
						t.Fatal("shared pointer", a.Type())
					}
					check(a.Elem(), b.Elem())
				}
			case reflect.Slice:
				if a.Len() > 0 && a.Pointer() == b.Pointer() {
					t.Fatal("shared slice", a.Type())
				}
				for i := 0; i < a.Len(); i++ {
					check(a.Index(i), b.Index(i))
				}
			case reflect.Map:
				if !a.IsNil() && a.UnsafePointer() == b.UnsafePointer() {
					t.Fatal("shared map", a.Type())
				}
				for _, key := range a.MapKeys() {
					check(a.MapIndex(key), b.MapIndex(key))
				}
			case reflect.Struct:
				for i := 0; i < a.NumField(); i++ {
					if a.Type().Field(i).IsExported() {
						check(a.Field(i), b.Field(i))
					}
				}
			}
		}
		check(original, cloned)
	}
}

func FuzzJSONStringSpan(f *testing.F) {
	for _, seed := range []string{"plain", `\\\"quoted`, "<>&\u2028\u2029", "invalid\xff", strings.Repeat("A", 256)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 65536 {
			t.Skip()
		}
		encoded, _ := json.Marshal(text)
		raw := string(encoded) + `,"following"`
		if end := jsonStringEnd(raw, 0); end != len(encoded) {
			t.Fatalf("wrong string end %d, want %d", end, len(encoded))
		}
	})
}
