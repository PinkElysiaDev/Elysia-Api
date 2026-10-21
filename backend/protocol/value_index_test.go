package protocol

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestValueIndexParityAndOwnership(t *testing.T) {
	for _, raw := range []string{
		` { "duplicate":0,"dupl\u0069cate":9007199254740993,"nested":[{"text":"` + strings.Repeat("A", 128<<10) + `"}],"escaped":"\\\"" } `,
		`{"large":"` + strings.Repeat("<>&", 32768) + `","small":{"null":null,"array":[],"number":1e999}}`,
	} {
		root, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		reader := requestValueReader(root)
		var compare func(Value)
		compare = func(value Value) {
			t.Helper()
			if value.IsObject() {
				got, err := value.readObjectWithin(reader)
				want, wantErr := value.ReadObject()
				if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("object mismatch", err, wantErr)
				}
				for _, child := range got {
					compare(child)
				}
				got["mutated"] = StringValue("not shared")
				again, _ := value.readObjectWithin(reader)
				if !again["mutated"].IsZero() {
					t.Fatal("map shared")
				}
			} else if value.IsArray() {
				got, err := value.readArrayWithin(reader)
				want, wantErr := value.readArray()
				if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("array mismatch", err, wantErr)
				}
				for _, child := range got {
					compare(child)
				}
			}
		}
		compare(root)
		if reader.index == nil || len(reader.index.ends) == 0 || len(reader.index.ends) > valueIndexEntries {
			t.Fatal("index not built or bounded")
		}
	}
	// Metadata exhaustion is an optimization miss, never a resource verdict.
	root, err := ParseValue([]byte(strings.Repeat("[", typedMeterTraversalDepth+1) + `"` + strings.Repeat("x", 65536) + `"` + strings.Repeat("]", typedMeterTraversalDepth+1)))
	if err != nil {
		t.Fatal(err)
	}
	r := requestValueReader(root)
	got, err := root.readArrayWithin(r)
	want, _ := root.readArray()
	if err != nil || !reflect.DeepEqual(got, want) || r.index.ends != nil {
		t.Fatal("deep fallback changed")
	}
}

func TestValueIndexConcurrentReads(t *testing.T) {
	root, _ := ParseValue([]byte(`{"items":[{"data":"` + strings.Repeat("A", 1<<20) + `"}]}`))
	reader := requestValueReader(root)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				fields, err := root.readObjectWithin(reader)
				if err != nil {
					t.Error(err)
					return
				}
				items, err := fields["items"].readArrayWithin(reader)
				if err != nil || len(items) != 1 {
					t.Error("bad array", err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestImmutableMetricIdentityDoesNotCrossStorage(t *testing.T) {
	limits := DefaultLimits()
	cache := &semanticCache{limits: limits}
	values := make([]Value, 4)
	for i := range values {
		values[i] = StringValue(strings.Repeat(fmt.Sprint(i), 1024))
		if err := cache.check(&Node{Payload: values[i]}, limits); err != nil {
			t.Fatal(err)
		}
	}
	if cache.size() != 4 {
		t.Fatal("fixture did not spill")
	}
	runtime.GC()
	for _, v := range values {
		if _, ok := cache.lookup(v); !ok {
			t.Fatal("live identity lost")
		}
	}
	copy := Value{raw: strings.Clone(values[3].raw)}
	if _, ok := cache.lookup(copy); ok {
		t.Fatal("different storage must be a safe miss")
	}
	if got, want := cache.check(&Node{Payload: copy}, limits), checkSemanticValue(&Node{Payload: copy}, limits); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	// Same start, different extent must never share metrics.
	a := Value{raw: "12345"}
	cache.store(a, semanticValueEntry{hasMetrics: true, metrics: semanticMetrics{bytes: 5, nodes: 1}})
	if _, ok := cache.lookup(Value{raw: a.raw[:3]}); ok {
		t.Fatal("extent omitted from identity")
	}
}

func FuzzIndexedContainerParity(f *testing.F) {
	for _, raw := range []string{`{"a":[true,null,1e99]}`, `{"x":1,"\u0078":2}`, `["\\\"",{},[]]`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		value, err := ParseValue([]byte(raw))
		if err != nil {
			return
		}
		// A tiny threshold exercises every indexed span with small fuzz inputs.
		reader := &valueReader{minimumSharedBytes: 1, index: &valueIndex{root: value.raw, minimumBytes: 1}}
		var compare func(Value)
		compare = func(v Value) {
			if v.IsObject() {
				a, e := v.readObjectWithin(reader)
				b, w := v.ReadObject()
				if !reflect.DeepEqual(a, b) || fmt.Sprint(e) != fmt.Sprint(w) {
					t.Fatal("object parity")
				}
				for _, x := range a {
					compare(x)
				}
			} else if v.IsArray() {
				a, e := v.readArrayWithin(reader)
				b, w := v.readArray()
				if !reflect.DeepEqual(a, b) || fmt.Sprint(e) != fmt.Sprint(w) {
					t.Fatal("array parity")
				}
				for _, x := range a {
					compare(x)
				}
			}
		}
		compare(value)
	})
}
