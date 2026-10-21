package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func normalizedPartitionState(request *Request) *conversionState {
	limits := semanticLimits(DefaultLimits())
	plan := typedLimitPlans[reflect.TypeFor[*Request]()]
	owned := cloneSemantic(reflect.ValueOf(request), plan).Interface().(*Request)
	state := &conversionState{document: owned, limits: limits, isStrict: true, cache: &semanticCache{limits: limits}}
	state.normalize(reflect.ValueOf(owned), plan)
	state.requestNormalized = true
	return state
}

func TestRequestPartitionBudgetAndNormalizationParity(t *testing.T) {
	request := &Request{}
	fillMeterValue(reflect.ValueOf(request).Elem(), parseTestValue(t, ` {"raw":"<>&","n":9007199254740993} `), 0)
	for len(request.Content) < 64 {
		request.Content = append(request.Content, request.Content...)
	}
	state := normalizedPartitionState(request)
	state.prepareRequestPartition(ConversionRequest, "responses_include")
	if state.requestPartition == nil {
		t.Fatal("partition not built")
	}
	owned := state.document.(*Request)
	for _, parameters := range []Object{nil, {}, {"new": parseTestValue(t, ` {"n":1e99,"text":"<>&"} `)}, {"absent": {}}} {
		owned.Parameters = parameters
		for _, output := range []*ClientOutput{nil, {}, {RawStreamOptions: StringValue(strings.Repeat("a", 1024))}} {
			owned.ClientOutput = output
			for _, metric := range []string{"bytes", "nodes", "depth"} {
				for _, limit := range []int{1, 2, 8, 64, 1024, 1 << 20} {
					state.limits = semanticLimits(DefaultLimits())
					switch metric {
					case "bytes":
						state.limits.BufferBytes = limit
					case "nodes":
						state.limits.Nodes = limit
					case "depth":
						state.limits.Depth = limit
					}
					got := state.checkRequestPartition()
					want := state.cache.check(owned, state.limits) == nil
					if got != want {
						t.Fatalf("partition verdict %s=%d: %v / %v", metric, limit, got, want)
					}
				}
			}
		}
	}
	owned.Parameters = Object{"normalize": parseTestValue(t, ` {"n":1e99,"text":"<>&"} `)}
	owned.ClientOutput = &ClientOutput{RawResponsesInclude: parseTestValue(t, ` ["reasoning.encrypted_content"] `)}
	oracle := normalizedPartitionState(owned)
	state.normalizeRequestPartition()
	got, _ := EncodeValue(state.document)
	want, _ := EncodeValue(oracle.document)
	if got != want {
		t.Fatal("incremental normalization differs")
	}
	state.prepareRequestPartition(ConversionRequest, "request_projection")
	if state.requestPartition != nil {
		t.Fatal("content-mutating action retained facts")
	}
}

func TestRequestPartitionRuleParity(t *testing.T) {
	storage, _ := EncodeValue(responsesStorageOptions{SourceCodec: "responses", TargetCodec: "anthropic", OnUnsupported: "degrade"})
	projection, _ := EncodeValue(requestProjectionOptions{TargetCodec: "anthropic"})
	rules := []ConversionRule{
		{ID: "include", Enabled: true, Phase: ConversionRequest, Action: "responses_include", Value: StringValue("anthropic")},
		{ID: "stream", Enabled: true, Phase: ConversionRequest, Action: "stream_options", Value: StringValue("anthropic")},
		{ID: "store", Enabled: true, Phase: ConversionRequest, Action: "responses_storage", Value: storage},
		{ID: "safety", Enabled: true, Phase: ConversionRequest, Action: "gemini_safety_settings", Value: StringValue("anthropic")},
		{ID: "projection", Enabled: true, Phase: ConversionRequest, Action: "request_projection", Value: projection},
		{ID: "include2", Enabled: true, Phase: ConversionRequest, Action: "responses_include", Value: StringValue("anthropic")},
	}
	for i := range rules {
		rules[i].Order = i
	}
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "partition", Mode: "compatible", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{SchemaVersion: 1, Parameters: Object{"stream": parseTestValue(t, `false`), "responses_include": parseTestValue(t, `["reasoning.encrypted_content"]`), "store": parseTestValue(t, `true`), "gemini_safety_settings": parseTestValue(t, `[]`)}, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("f"), Options: Object{"eager_input_streaming": parseTestValue(t, `true`)}}}, ClientOutput: &ClientOutput{RawStreamOptions: parseTestValue(t, `{"include_usage":true}`)}}
	for range 64 {
		request.Content = append(request.Content, Node{Kind: TextNode, Payload: StringValue("history")})
	}
	fast, slow := normalizedPartitionState(request), normalizedPartitionState(request)
	fastSink, slowSink := &DiagnosticSink{}, &DiagnosticSink{}
	for _, rule := range c.phases[ConversionRequest] {
		slow.requestNormalized = false
		slow.requestPartition = nil // established full-walk oracle
		_, a := fast.apply(t.Context(), c, ConversionRequest, rule, ConversionContext{}, fastSink)
		_, b := slow.apply(t.Context(), c, ConversionRequest, rule, ConversionContext{}, slowSink)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatalf("%s: errors %v / %v", rule.Action, a, b)
		}
		x, _ := EncodeValue(fast.document)
		y, _ := EncodeValue(slow.document)
		if x != y || !reflect.DeepEqual(fastSink.Issues(), slowSink.Issues()) {
			t.Fatal("rule boundary differs", rule.Action)
		}
		if rule.Action == "request_projection" && fast.requestPartition != nil {
			t.Fatal("partition survived mutation")
		}
	}
}

func TestRequestPartitionCompileSelection(t *testing.T) {
	for _, count := range []int{1, 3, 4, 16} {
		rules := make([]ConversionRule, count)
		for i := range rules {
			rules[i] = ConversionRule{Action: "stream_options"}
		}
		markRequestPartitions(rules)
		for _, rule := range rules {
			if rule.partitionRequest != (count >= 4) {
				t.Fatal("unexpected run selection", count)
			}
		}
	}
	rules := []ConversionRule{{Action: "stream_options"}, {Action: "responses_include"}, {Action: "responses_storage"}, {Action: "gemini_safety_settings", Match: ConversionMatch{Model: "conditional"}}, {Action: "stream_options"}}
	markRequestPartitions(rules)
	for _, rule := range rules {
		if rule.partitionRequest {
			t.Fatal("conditional rule did not break run")
		}
	}
}

func FuzzRequestPartitionParity(f *testing.F) {
	for _, raw := range []string{`null`, ` {"n":9007199254740993,"x":"<>&"} `, `[true,[],{}]`} {
		f.Add(raw, uint16(256))
	}
	f.Fuzz(func(t *testing.T, raw string, budget uint16) {
		if len(raw) > 8192 {
			t.Skip()
		}
		value, err := ParseValue([]byte(raw))
		if err != nil {
			return
		}
		request := &Request{Content: make([]Node, 64)}
		for i := range request.Content {
			request.Content[i] = Node{Kind: TextNode, Payload: StringValue("old")}
		}
		state := normalizedPartitionState(request)
		state.prepareRequestPartition(ConversionRequest, "responses_include")
		request = state.document.(*Request)
		request.Parameters = Object{"new": value}
		state.limits.BufferBytes = int(budget) + 1
		if state.checkRequestPartition() != (state.cache.check(request, state.limits) == nil) {
			t.Fatal("byte budget changed")
		}
		oracle := normalizedPartitionState(request)
		state.normalizeRequestPartition()
		a, _ := EncodeValue(state.document)
		b, _ := EncodeValue(oracle.document)
		if a != b {
			t.Fatal("normalization changed")
		}
	})
}
