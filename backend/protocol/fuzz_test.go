package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"
)

func FuzzJSONResourceLimits(f *testing.F) {
	for _, raw := range []string{`null`, `123`, `{"a":[true,false,0,"\\\"[]{}",{"b":"\u4f60"}]}`, `[[[[]]]]`} {
		f.Add([]byte(raw), uint8(4), uint8(12))
	}
	f.Fuzz(func(t *testing.T, raw []byte, depthLimit, nodeLimit uint8) {
		if len(raw) > fuzzInputLimit {
			return
		}
		value, err := ParseValue(raw)
		if err != nil {
			return
		}
		limits := DefaultLimits()
		limits.Depth, limits.Nodes = int(depthLimit)+1, int(nodeLimit)+1
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		depth, nodes, maximum := 0, 0, 0
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
				} else {
					depth--
				}
				maximum = max(maximum, depth)
			}
		}
		want := nodes <= limits.Nodes && maximum <= limits.Depth
		if actual := checkValueLimits(value, limits) == nil; actual != want {
			t.Fatalf("limit checker disagreed with JSON tokens: %s", raw)
		}
	})
}

const fuzzInputLimit = 64 << 10

func FuzzJSONContainers(f *testing.F) {
	for _, raw := range [][]byte{[]byte(`{"a":[null,false,0,9007199254740993,{},[]],"\\\"":true}`), []byte(`["a\\\"b",{"\u0061":1,"a":2}]`), {'{', '"', 0xff, '"', ':', '0', '}'}} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > fuzzInputLimit {
			return
		}
		value, err := ParseValue(raw)
		if err != nil || checkValueLimits(value, DefaultLimits()) != nil {
			return
		}
		var visit func(Value)
		visit = func(value Value) {
			switch valueType(value) {
			case ObjectType:
				var want map[string]json.RawMessage
				if err := json.Unmarshal(value.Bytes(), &want); err != nil {
					t.Fatal(err)
				}
				actual, err := value.ReadObject()
				if err != nil || len(actual) != len(want) {
					t.Fatal("object shape differs")
				}
				for key, expected := range want {
					if !bytes.Equal(actual[key].Bytes(), expected) {
						t.Fatalf("object field differs at %q", key)
					}
					visit(actual[key])
				}
			case ArrayType:
				var want []json.RawMessage
				if err := json.Unmarshal(value.Bytes(), &want); err != nil {
					t.Fatal(err)
				}
				actual, err := value.readArray()
				if err != nil || len(actual) != len(want) {
					t.Fatal("array shape differs")
				}
				for i, expected := range want {
					if !bytes.Equal(actual[i].Bytes(), expected) {
						t.Fatalf("array item differs at %d", i)
					}
					visit(actual[i])
				}
			}
		}
		visit(value)
	})
}

func FuzzCompileDefinition(f *testing.F) {
	for _, name := range []string{"text-alpha", "text-beta", "session-alpha", "job-alpha"} {
		seed, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			f.Fatal(err)
		}
		f.Add(seed)
	}
	f.Add([]byte(`{"schemaVersion":2,"directions":{"decode_request":{"transform":{"op":"ref","ref":"loop"}}},"expressions":{"loop":{"op":"ref","ref":"loop"}}}`))
	// A definition that declares a usage mapping and a cache intent exercises the
	// compile path where a wrong key or kind is a definition error, not a runtime
	// surprise: the reserved-name and duplicate-intent rules live here.
	f.Add([]byte(`{"schemaVersion":2,"id":"usage-seed","name":"usage-seed","version":"1","family":"usage-seed","wireVersion":"1","capabilities":{"text":true,"usage":true,"cache.breakpoints":true},"directions":{"encode_request":{"transform":{"op":"object","fields":{"prompt_cache_key":{"op":"read","from":"input","path":"/cache/0/value"}}},"capabilities":{"cache.breakpoints":true}}}}`))
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > fuzzInputLimit {
			return
		}
		compiled, issues := compiler.Compile(raw)
		if compiled == nil {
			if IssuesError(issues) == nil {
				t.Fatal("compile failure without a diagnostic")
			}
			return
		}
		if IssuesError(issues) != nil {
			t.Fatal("invalid definition produced executable code")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		report := Verify(ctx, compiled)
		if report.DefinitionHash != compiled.Hash() || report.CompilerVersion != CompilerVersion {
			t.Fatal("verification evidence detached from compilation")
		}
	})
}

func FuzzJSONMappingPresence(f *testing.F) {
	// Seeds cover JSON spelling/presence edges plus usage alias pairs: a value
	// body carrying both legacy top-level aliases must round-trip unchanged, and
	// a zero alias must stay present rather than being dropped.
	for _, seed := range []string{`{}`, `{"value":null}`, `{"value":false}`, `{"value":0}`, `{"value":900719925474099312345}`, `{"value":-0}`, `{"value":1e99}`, `{"value":[{},[],null,false,0]}`, `{"value":{"cache_read_input_tokens":0,"cache_creation_input_tokens":7}}`, `{"value":{"input_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`} {
		f.Add([]byte(seed))
	}
	expression := objectExpression(map[string]Expression{"value": readExpression("input", "/value")})
	compiler := expressionCompiler{limits: DefaultLimits()}
	compiled, err := compiler.compile(expression, "/mapping", expressionScope{}, 1)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > fuzzInputLimit {
			return
		}
		value, err := ParseValue(raw)
		if err != nil || checkValueLimits(value, DefaultLimits()) != nil || !value.IsObject() {
			return
		}
		input, _ := value.ReadObject()
		result, err := compiled.evaluate(evaluation{ctx: t.Context(), input: value, root: value, budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil {
			t.Fatal(err)
		}
		output, err := result.ReadObject()
		if err != nil || output["value"] != input["value"] {
			t.Fatalf("presence or JSON spelling changed: %s -> %s", raw, result.Bytes())
		}
	})
}

func FuzzEventReplay(f *testing.F) {
	f.Add([]byte{0, 1, 1, 3, 4, 5})
	f.Add([]byte{0, 2, 2, 4, 1})
	f.Add([]byte{4, 4})
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 1024 {
			return
		}
		limits := DefaultLimits()
		limits.StateItems, limits.BufferBytes = 16, 4096
		replay, err := NewEventReplay(replayTarget(), limits)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range actions {
			event := Event{SchemaVersion: 1, ItemID: StringValue("text")}
			switch action % 7 {
			case 0:
				event.Type, event.Item = ItemStarted, &Node{Kind: TextNode}
			case 1:
				event.Type, event.Delta = ItemDelta, StringValue("a")
			case 2:
				event.Type, event.Delta = ItemSnapshot, StringValue("abc")
			case 3:
				event.Type = ItemFinished
			case 4:
				event.Type = ResponseFinished
			case 5:
				event.Type, event.Usage = UsageUpdated, &Usage{CacheRead: &Counter{Count: 0, Origin: ObservedCount}}
			case 6:
				event.Type = OperationCancelled
			}
			if _, err := replay.Consume(event); err != nil {
				break
			}
			if replay.state.buffered > limits.BufferBytes || len(replay.items) > limits.StateItems {
				t.Fatal("state exceeded configured bounds")
			}
		}
		_ = replay.Finish()
	})
}
