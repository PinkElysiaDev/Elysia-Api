package relay

import (
	"strings"
	"testing"
)

// frames[] 异构帧映射:按 JSON type 字段匹配帧型,未声明的帧型跳过,
// terminal 帧按事件名收尾(Responses 型协议形态)。
func TestCustomProtocolStreamFramesDecode(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "frames-vendor",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v1/runs",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			Frames: []CustomProtocolStreamFrame{
				{Event: "response.output_text.delta", Response: &CustomProtocolResponse{TextPath: "delta"}},
				{Event: "response.reasoning_text.delta", Response: &CustomProtocolResponse{ReasoningPath: "delta"}},
				{Event: "response.completed", Terminal: true, PayloadPath: "response", Response: &CustomProtocolResponse{UsagePath: "usage"}},
			},
		}},
	}
	decoder, err := NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}

	// 未声明的帧型(无文本/终态语义)必须被跳过而不是误映射。
	skipped, done, err := decoder.Decode(SSEEvent{Data: `{"type":"response.created"}`})
	if err != nil || done || len(skipped) != 0 {
		t.Fatalf("unmatched frame must be skipped: events=%v done=%v err=%v", skipped, done, err)
	}
	if decoder.TerminalReceived() {
		t.Fatal("response.created must not terminate the stream")
	}

	first, _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.output_text.delta","delta":"Hel"}`})
	if err != nil {
		t.Fatalf("decode text delta: %v", err)
	}
	if len(first) != 1 || first[0].Type != MaheshvaraEventTextDelta || first[0].Delta != "Hel" {
		t.Fatalf("unexpected text delta events: %#v", first)
	}
	second, _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.output_text.delta","delta":"lo"}`})
	if err != nil {
		t.Fatalf("decode text delta 2: %v", err)
	}
	if len(second) != 1 || second[0].Delta != "lo" {
		t.Fatalf("unexpected second delta: %#v", second)
	}
	reasoning, _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.reasoning_text.delta","delta":"hmm"}`})
	if err != nil {
		t.Fatalf("decode reasoning delta: %v", err)
	}
	if len(reasoning) != 1 || reasoning[0].Type != MaheshvaraEventReasoningDelta || reasoning[0].ReasoningDelta != "hmm" {
		t.Fatalf("reasoning must map via its own frame, got: %#v", reasoning)
	}

	terminal, _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":2}}}`})
	if err != nil {
		t.Fatalf("decode completed: %v", err)
	}
	if !decoder.TerminalReceived() {
		t.Fatal("terminal frame must mark the stream terminal")
	}
	var sawUsage, sawCompleted bool
	for _, event := range terminal {
		if event.Type == MaheshvaraEventUsageDelta && event.Usage != nil && event.Usage.InputTokens == 3 {
			sawUsage = true
		}
		if event.Type == MaheshvaraEventResponseCompleted {
			sawCompleted = true
		}
	}
	if !sawUsage || !sawCompleted {
		t.Fatalf("terminal frame must yield usage + completed, got: %#v", terminal)
	}
}

// done 语义:doneValue 命中返回 done=true;finish reason 映射只置终态不提前
// 结束——调用方要继续排水接收 usage 尾帧。
func TestCustomProtocolStreamFinishIsNotDone(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "openai-ish",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			Response: &CustomProtocolResponse{
				TextPath:         "choices[0].delta.content",
				FinishReasonPath: "choices[0].finish_reason",
				UsagePath:        "usage",
			},
		}},
	}
	decoder, err := NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	finish, done, err := decoder.Decode(SSEEvent{Data: `{"choices":[{"delta":{},"finish_reason":"stop"}]}`})
	if err != nil {
		t.Fatalf("decode finish frame: %v", err)
	}
	if done {
		t.Fatal("finish_reason must not signal done; caller keeps draining for trailing usage")
	}
	if !decoder.TerminalReceived() || !decoder.SawFinishReason() {
		t.Fatal("finish_reason must mark terminal and saw-finish")
	}
	if len(finish) != 1 || finish[0].Type != MaheshvaraEventResponseCompleted {
		t.Fatalf("finish frame must emit a completed event: %#v", finish)
	}
	// 终态后的 usage 尾帧仍可解码(排水场景)。
	trailing, _, err := decoder.Decode(SSEEvent{Data: `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":9}}`})
	if err != nil {
		t.Fatalf("decode trailing usage: %v", err)
	}
	if len(trailing) != 1 || trailing[0].Type != MaheshvaraEventUsageDelta {
		t.Fatalf("trailing usage frame must decode to a usage event: %#v", trailing)
	}
	if _, done, err := decoder.Decode(SSEEvent{Data: "[DONE]"}); err != nil || !done {
		t.Fatalf("doneValue must return done=true, got done=%v err=%v", done, err)
	}
}

// 空补全:finish_reason 有值但零输出,SawFinishReason 必须为真(调用方据此
// 放行);[DONE] 兜底且无 finish 的空流仍应可判异常。
func TestCustomProtocolStreamSawFinishReason(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "finish-only",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			Response: &CustomProtocolResponse{FinishReasonPath: "finish"},
		}},
	}
	decoder, err := NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	if _, _, err := decoder.Decode(SSEEvent{Data: `{"finish":"content_filter"}`}); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoder.SawFinishReason() || decoder.SawOutput() {
		t.Fatalf("finish-only frame: sawFinish=%v sawOutput=%v", decoder.SawFinishReason(), decoder.SawOutput())
	}

	bare, err := NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		t.Fatalf("decoder 2: %v", err)
	}
	if _, _, err := bare.Decode(SSEEvent{Data: "[DONE]"}); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if bare.SawFinishReason() {
		t.Fatal("[DONE] without finish_reason must not count as saw-finish")
	}
}

func TestValidateCustomProtocolStreamFrames(t *testing.T) {
	base := func(stream *CustomProtocolStreamMapping) CustomProtocolConfig {
		return CustomProtocolConfig{
			ID:  "frames-validation",
			Request: CustomProtocolRequest{
				Method:       "POST",
				PathTemplate: "/v1/x",
				BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
			},
			Response: CustomProtocolResponse{Stream: stream},
		}
	}
	if err := ValidateCustomProtocol(base(&CustomProtocolStreamMapping{
		Frames: []CustomProtocolStreamFrame{
			{Event: "a", Response: &CustomProtocolResponse{TextPath: "delta"}},
			{Event: "b", Terminal: true, PayloadPath: "resp", Response: &CustomProtocolResponse{UsagePath: "usage"}},
		},
	})); err != nil {
		t.Fatalf("valid frames must pass: %v", err)
	}
	if err := ValidateCustomProtocol(base(&CustomProtocolStreamMapping{
		Frames: []CustomProtocolStreamFrame{{PayloadPath: "x"}},
	})); err == nil || !strings.Contains(err.Error(), "requires event or match") {
		t.Fatalf("frame without event/match must fail, got: %v", err)
	}
	if err := ValidateCustomProtocol(base(&CustomProtocolStreamMapping{
		Frames: []CustomProtocolStreamFrame{{Event: "a", PayloadPath: "bad[path"}},
	})); err == nil || !strings.Contains(err.Error(), "payloadPath") {
		t.Fatalf("bad frame payloadPath must fail, got: %v", err)
	}
	if err := ValidateCustomProtocol(base(&CustomProtocolStreamMapping{
		Frames: []CustomProtocolStreamFrame{{Event: "a", Response: &CustomProtocolResponse{
			Stream: &CustomProtocolStreamMapping{Mode: "delta"},
		}}},
	})); err == nil || !strings.Contains(err.Error(), "cannot contain another stream mapping") {
		t.Fatalf("nested stream inside a frame must fail, got: %v", err)
	}
}
