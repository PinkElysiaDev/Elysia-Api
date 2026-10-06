package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func referenceTypedLimits(value any, limits Limits) error {
	encoded, err := EncodeValue(value)
	if err != nil {
		return err
	}
	return checkValueLimits(encoded, limits)
}

func checkMeterEquivalent(t *testing.T, value any) {
	t.Helper()
	encoded, err := EncodeValue(value)
	if err != nil {
		t.Fatal(err)
	}
	limits := Limits{BufferBytes: len(encoded.raw), Nodes: 1 << 25, Depth: typedMeterTraversalDepth}
	meter := typedMeter{limits: limits}
	if !meter.visit(typedLimitPlans[reflect.TypeOf(value)], reflect.ValueOf(value)) {
		t.Fatal("valid semantic model was not measured")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded.Bytes()))
	decoder.UseNumber()
	nodes, depth, maxDepth := 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		nodes++
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				depth++
				if depth > maxDepth {
					maxDepth = depth
				}
			} else {
				depth--
			}
		}
	}
	if meter.bytes != len(encoded.raw) || meter.nodes != nodes || meter.depth != 0 {
		t.Fatalf("meter (%d,%d,%d) != encoding (%d,%d,0)", meter.bytes, meter.nodes, meter.depth, len(encoded.raw), nodes)
	}
	limits.Nodes, limits.Depth = nodes, maxDepth
	for _, delta := range []int{-1, 0, 1} {
		for _, field := range []string{"bytes", "nodes", "depth"} {
			boundary := limits
			switch field {
			case "bytes":
				boundary.BufferBytes += delta
			case "nodes":
				boundary.Nodes += delta
			case "depth":
				boundary.Depth += delta
			}
			want, got := referenceTypedLimits(value, boundary), checkTypedLimits(value, boundary)
			if (want == nil) != (got == nil) || want != nil && want.Error() != got.Error() {
				t.Fatalf("%s delta %d: got %v, want %v", field, delta, got, want)
			}
		}
	}
}

// Populate every exported field, including fields added to the contract later.
// This catches omission-rule changes without a second hand-written field list.
func fillMeterValue(value reflect.Value, raw Value, depth int) {
	if value.Type() == reflect.TypeFor[Value]() {
		value.Set(reflect.ValueOf(raw))
		return
	}
	if depth > 8 && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Map) {
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillMeterValue(value.Elem(), raw, depth+1)
	case reflect.Struct:
		for index := range value.NumField() {
			if value.Field(index).CanSet() {
				fillMeterValue(value.Field(index), raw, depth+1)
			}
		}
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		fillMeterValue(value.Index(0), raw, depth+1)
	case reflect.Map:
		value.Set(reflect.MakeMap(value.Type()))
		child := reflect.New(value.Type().Elem()).Elem()
		fillMeterValue(child, raw, depth+1)
		value.SetMapIndex(reflect.ValueOf("<field>\u2028\xff"), child)
	case reflect.String:
		value.SetString("<>&\"\n\u2028\u2029\xff")
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int64:
		value.SetInt(-9223372036854775808)
	default:
		panic("uncovered semantic field kind: " + value.Kind().String())
	}
}

func TestTypedMeterMatchesJSONContract(t *testing.T) {
	for _, raw := range []string{`null`, `0`, `false`, `[]`, `{}`, `9007199254740993123456789`, `1.2300e+40`, " { \"<key>\" : [true, null, \"<>&\u2028\u2029\", \"\\u003c\\\"\"] } ", "\"\xff\""} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range []any{&Request{}, &Response{}, &[]Event{{}}} {
			fillMeterValue(reflect.ValueOf(root).Elem(), value, 0)
			if events, ok := root.(*[]Event); ok {
				root = *events
			}
			checkMeterEquivalent(t, root)
		}
	}
	for _, root := range []any{(*Request)(nil), (*Response)(nil), []Event(nil), []Event{}, &Request{}, &Response{}, &Request{Content: []Node{}, Tools: []Tool{}, Parameters: Object{}}} {
		checkMeterEquivalent(t, root)
	}
}

func TestTypedMeterRetainsEncoderFailures(t *testing.T) {
	nodes := make([]Node, 1)
	nodes[0].Children = nodes
	for _, value := range []any{&Request{Content: nodes}, &Request{Parameters: Object{"absent": {}}}, &Response{Native: &Native{}}, struct{ Channel chan int }{}} {
		want, got := referenceTypedLimits(value, DefaultLimits()), checkTypedLimits(value, DefaultLimits())
		if want == nil || got == nil || want.Error() != got.Error() {
			t.Fatalf("encoder failure changed: got %v, want %v", got, want)
		}
	}
}

func FuzzTypedLimitEquivalence(f *testing.F) {
	for _, seed := range []string{`null`, `{"x":[0,false,null,"<>&"]}`, `"\u2028"`, `9007199254740993`, ` [ "x" ] `} {
		f.Add([]byte(seed), uint16(31))
	}
	f.Fuzz(func(t *testing.T, raw []byte, flags uint16) {
		if len(raw) > 16384 {
			t.Skip()
		}
		value, err := ParseValue(raw)
		if err != nil {
			return
		}
		request := &Request{SchemaVersion: 1, Source: Identity{Family: string(raw)}, Model: value, Content: []Node{{Kind: TextNode, Payload: value, Attributes: Object{string(raw): value}}}, Parameters: Object{"raw": value}, Native: &Native{Value: value}}
		if flags&1 != 0 {
			request.Tools = []Tool{{Kind: FunctionTool, Name: value, InputSchema: value}}
		}
		if flags&2 != 0 {
			request.Cache = []CacheIntent{{Kind: "key", Value: value}}
		}
		if flags&4 != 0 {
			request.Content[0].Children = []Node{{Kind: MessageNode, Role: value}}
		}
		checkMeterEquivalent(t, request)
		response := &Response{Content: request.Content, Native: request.Native, Usage: &Usage{Input: &Counter{Count: int64(flags)}}}
		checkMeterEquivalent(t, response)
		checkMeterEquivalent(t, []Event{{Type: ItemSnapshot, Item: &request.Content[0], Usage: response.Usage, Delta: value}})
	})
}

func BenchmarkTypedResourceCheck(b *testing.B) {
	value := StringValue(strings.Repeat("prefix <>& ", 2048))
	request := &Request{SchemaVersion: 1, Model: StringValue("m"), Content: []Node{{Kind: TextNode, Payload: value}}, Native: &Native{Value: value}}
	for _, mode := range []string{"encoding", "structural"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if mode == "encoding" {
					err = referenceTypedLimits(request, DefaultLimits())
				} else {
					err = checkTypedLimits(request, DefaultLimits())
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
