package protocol

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const startupFirst = `{"id":"placeholder","model":"m","created":100,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"usage":null,"system_fingerprint":null}`
const startupSecond = `{"id":"provider","model":"m","created":100,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"service_tier":"auto"}`

func startupValue(t *testing.T, raw string) Value {
	t.Helper()
	v, err := ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func startupState(t *testing.T, mode string, enabled bool) *SourceEventState {
	t.Helper()
	c, err := ResolveConversion(ConversionPolicy{SchemaVersion: 1, ID: "test-source", Mode: mode, Rules: []ConversionRule{{ID: "chat-stream-identity", Enabled: enabled, Phase: ConversionSourceEvent, Action: "chat_stream_identity"}}})
	if err != nil {
		t.Fatal(err)
	}
	return &SourceEventState{conversion: c, limits: DefaultLimits(), sink: &DiagnosticSink{}, route: ConversionContext{Source: Identity{DefinitionID: "source"}, Target: Identity{DefinitionID: "target"}}}
}

func TestChatStartupIdentityCorrection(t *testing.T) {
	for _, created := range []int64{100, 99, 180, 9007199254740993} {
		t.Run(fmt.Sprint(created), func(t *testing.T) {
			state := startupState(t, "compatible", true)
			first := startupValue(t, startupFirst)
			second := startupValue(t, strings.Replace(startupSecond, `"created":100`, fmt.Sprintf(`"created":%d`, created), 1))
			metadata := Object{"event": StringValue("message")}
			if frames, err := state.Push(t.Context(), first, metadata); err != nil || len(frames) != 0 {
				t.Fatal(frames, err)
			}
			metadata["event"] = StringValue("changed")
			frames, err := state.Push(t.Context(), second, nil)
			if err != nil || len(frames) != 2 {
				t.Fatal(frames, err)
			}
			fields, _ := frames[0].Value.ReadObject()
			if fields["id"] != StringValue("provider") || string(fields["created"].Bytes()) != fmt.Sprint(created) || frames[1].Value != second || frames[0].Ordinal != 1 || frames[1].Ordinal != 2 || frames[0].Metadata["event"] != StringValue("message") {
				t.Fatal(frames)
			}
			original, _ := first.ReadObject()
			for name, value := range original {
				if name != "id" && name != "created" && fields[name] != value {
					t.Fatalf("modified %s", name)
				}
			}
			issues := state.sink.Issues()
			want := 1
			if created != 100 {
				want++
			}
			if len(issues) != want {
				t.Fatal(issues)
			}
			for _, issue := range issues {
				if issue.Code != ConversionDegraded || issue.Fidelity != "lossy_compatible" || issue.Protocol.DefinitionID != "source" || issue.Direction != DecodeEvent || issue.Stage != "conversion.source_event" || issue.PolicyHash != state.conversion.Hash || issue.RuleID != "chat-stream-identity" || issue.Evidence != "test-source" {
					t.Fatal(issue)
				}
			}
			third := startupValue(t, strings.Replace(startupSecond, "provider", "third", 1))
			out, err := state.Push(t.Context(), third, nil)
			if err != nil || len(out) != 1 || out[0].Value != third || len(state.sink.Issues()) != want {
				t.Fatal(out, err)
			}
			if string(first.Bytes()) != startupFirst {
				t.Fatal("original evidence modified")
			}
		})
	}
}

func TestChatStartupIdentityEscapedTags(t *testing.T) {
	state := startupState(t, "compatible", true)
	escape := strings.NewReplacer("chat.completion.chunk", `\u0063hat.completion.chunk`, "assistant", `\u0061ssistant`)
	first := startupValue(t, escape.Replace(startupFirst))
	second := startupValue(t, escape.Replace(startupSecond))
	if batch, err := state.Push(t.Context(), first, nil); err != nil || len(batch) != 0 {
		t.Fatal(batch, err)
	}
	batch, err := state.Push(t.Context(), second, nil)
	if err != nil || len(batch) != 2 {
		t.Fatal(batch, err)
	}
	original, _ := first.ReadObject()
	corrected, _ := batch[0].Value.ReadObject()
	if corrected["id"] != StringValue("provider") || batch[1].Value != second {
		t.Fatal(batch)
	}
	for field, value := range original {
		if field != "id" && corrected[field] != value {
			t.Fatalf("JSON representation changed outside identity correction: %s", field)
		}
	}
}

func TestChatStartupIdentityBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, from, to string
		first          bool
	}{
		{"text", `"content":""`, `"content":"real"`, true},
		{"missing-content", `,"content":""`, ``, true},
		{"null-content", `"content":""`, `"content":null`, true},
		{"tool", `"role":"assistant"`, `"role":"assistant","tool_calls":[]`, false},
		{"reasoning", `"role":"assistant"`, `"role":"assistant","reasoning_content":""`, false},
		{"unknown", `"service_tier":"auto"`, `"vendor":9007199254740993`, false},
		{"tier-type", `"service_tier":"auto"`, `"service_tier":false`, false},
		{"model", `"model":"m"`, `"model":"other"`, false},
		{"same-id", `"id":"provider"`, `"id":"placeholder"`, false},
		{"usage", `"usage":null`, `"usage":{"prompt_tokens":0}`, true},
		{"logprobs", `"logprobs":null`, `"logprobs":{}`, true},
		{"fingerprint", `"system_fingerprint":null`, `"system_fingerprint":"fp"`, true},
		{"null-created", `"created":100`, `"created":null`, false},
		{"negative-created", `"created":100`, `"created":-1`, false},
		{"float-created", `"created":100`, `"created":1.5`, false},
		{"empty-id", `"id":"provider"`, `"id":""`, false},
		{"null-index", `"index":0`, `"index":null`, false},
		{"choice-index", `"index":0`, `"index":1`, false},
		{"multi-choice", `"choices":[`, `"choices":[{},`, false},
		{"finish", `"finish_reason":null`, `"finish_reason":"stop"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			first, second := startupFirst, startupSecond
			if test.first {
				first = strings.Replace(first, test.from, test.to, 1)
			} else {
				second = strings.Replace(second, test.from, test.to, 1)
			}
			state := startupState(t, "compatible", true)
			var all []SourceFrame
			for _, raw := range []string{first, second, startupSecond} {
				out, err := state.Push(t.Context(), startupValue(t, raw), nil)
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, out...)
			}
			if len(all) != 3 || all[0].Value != startupValue(t, first) || all[1].Value != startupValue(t, second) || len(state.sink.Issues()) != 0 {
				t.Fatal(all, state.sink.Issues())
			}
		})
	}
}

func TestChatStartupIdentityPolicyAndCleanup(t *testing.T) {
	for _, mode := range []string{"strict", "compatible"} {
		state := startupState(t, mode, mode == "strict")
		out, err := state.Push(t.Context(), startupValue(t, startupFirst), nil)
		if err != nil || (mode == "compatible" && len(out) != 1) {
			t.Fatal(out, err)
		}
		out, err = state.Push(t.Context(), startupValue(t, startupSecond), nil)
		if mode == "strict" && (err == nil || len(out) != 0 || !strings.Contains(err.Error(), "/id")) {
			t.Fatal(out, err)
		}
		if mode == "compatible" && (err != nil || len(out) != 1) {
			t.Fatal(out, err)
		}
	}
	for _, failure := range []error{nil, context.Canceled, fmt.Errorf("read failed")} {
		state := startupState(t, "compatible", true)
		_, _ = state.Push(t.Context(), startupValue(t, startupFirst), nil)
		out := state.Finish(failure)
		if (len(out) == 1) != (failure == nil) || state.pending != nil || len(state.Finish(nil)) != 0 {
			t.Fatal(out)
		}
	}
	state := startupState(t, "compatible", true)
	state.limits.BufferBytes = len(startupFirst)
	if _, err := state.Push(t.Context(), startupValue(t, startupFirst), Object{"event": StringValue("oversize")}); err == nil || state.pending != nil {
		t.Fatal(err)
	}
	state = startupState(t, "compatible", true)
	_, _ = state.Push(t.Context(), startupValue(t, startupFirst), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := state.Push(ctx, startupValue(t, startupSecond), nil); err != context.Canceled || len(out) != 0 || state.pending != nil {
		t.Fatal(out, err)
	}
}

func TestSourceEventPhaseIsRestricted(t *testing.T) {
	for _, action := range []string{"set", "remove", "transform", "warn", "reject"} {
		_, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "invalid", Rules: []ConversionRule{{ID: "rule", Phase: ConversionSourceEvent, Action: action}}})
		if err == nil {
			t.Fatalf("allowed generic action %s before decoding", action)
		}
	}
}
