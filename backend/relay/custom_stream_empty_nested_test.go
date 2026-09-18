package relay

import (
	"encoding/json"
	"testing"
)

// 设计器旧版「流式映射」开关写入 stream.response = {body:{}} 空嵌套，运行时
// 必须视为未声明并继承顶层映射——否则流帧零产出，[DONE] 后 502。
func TestCustomProtocolEmptyNestedStreamResponseInheritsTopLevel(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "empty-nested",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: CustomProtocolResponse{
			TextPath:         "text",
			FinishReasonPath: "finish",
			UsagePath:        "usage",
			Stream: &CustomProtocolStreamMapping{
				Mode:     "delta",
				Response: &CustomProtocolResponse{Body: json.RawMessage(`{}`)},
			},
		},
	}
	decoder, err := NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	events, _, err := decoder.Decode(SSEEvent{Data: `{"text":"hi","usage":{"prompt_tokens":1,"completion_tokens":1}}`})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawText, sawUsage bool
	for _, event := range events {
		if event.Type == MaheshvaraEventTextDelta && event.Delta == "hi" {
			sawText = true
		}
		if event.Type == MaheshvaraEventUsageDelta {
			sawUsage = true
		}
	}
	if !sawText || !sawUsage {
		t.Fatalf("empty nested response must fall back to the top-level mapping: %#v", events)
	}
	finish, _, err := decoder.Decode(SSEEvent{Data: `{"text":"","finish":"stop"}`})
	if err != nil {
		t.Fatalf("decode finish: %v", err)
	}
	if !decoder.SawFinishReason() || !decoder.SawOutput() {
		t.Fatalf("finish must map via inherited top-level mapping: %#v", finish)
	}
	if len(finish) == 0 || finish[len(finish)-1].Type != MaheshvaraEventResponseCompleted {
		t.Fatalf("expected completed event: %#v", finish)
	}
}

// 注册时净化：空嵌套映射落库/入注册表前被置 nil（存量踩坑配置加载即自愈）；
// 非空嵌套保持原样。
func TestRegisterCustomProtocolNormalizesEmptyNestedStreamResponse(t *testing.T) {
	ClearCustomProtocols()
	t.Cleanup(ClearCustomProtocols)
	protocol := CustomProtocolConfig{
		ID:      "normalize-empty-nested",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{
			TextPath: "text",
			Stream: &CustomProtocolStreamMapping{
				Response: &CustomProtocolResponse{Body: json.RawMessage(`{}`)},
			},
		},
	}
	if err := RegisterCustomProtocol(protocol); err != nil {
		t.Fatalf("register: %v", err)
	}
	registered, ok := GetCustomProtocol("normalize-empty-nested")
	if !ok {
		t.Fatal("protocol not registered")
	}
	if registered.Response.Stream == nil || registered.Response.Stream.Response != nil {
		t.Fatalf("empty nested response must be normalized away: %#v", registered.Response.Stream)
	}

	protocol.ID = "normalize-keep-nested"
	protocol.Response.Stream.Response = &CustomProtocolResponse{TextPath: "delta"}
	if err := RegisterCustomProtocol(protocol); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	kept, _ := GetCustomProtocol("normalize-keep-nested")
	if kept.Response.Stream == nil || kept.Response.Stream.Response == nil || kept.Response.Stream.Response.TextPath != "delta" {
		t.Fatalf("non-empty nested response must survive normalization: %#v", kept.Response.Stream)
	}
}

// 嵌套映射使用 body 构造树（而非 fields）时也必须编译生效——旧实现只编译
// fields 非空的嵌套，body 树的映射标注被静默忽略。
func TestCustomProtocolNestedStreamBodyTreeCompiles(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID:      "nested-body-tree",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{
			TextPath: "unrelated",
			Stream: &CustomProtocolStreamMapping{
				Response: &CustomProtocolResponse{Body: json.RawMessage(`{"delta":{"field":"text"},"done":{"field":"stop_reason"}}`)},
			},
		},
	}
	decoder, err := NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	events, _, err := decoder.Decode(SSEEvent{Data: `{"delta":"partial","done":""}`})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 1 || events[0].Type != MaheshvaraEventTextDelta || events[0].Delta != "partial" {
		t.Fatalf("nested body tree must map via its field annotations: %#v", events)
	}
	terminal, _, err := decoder.Decode(SSEEvent{Data: `{"delta":"","done":"stop"}`})
	if err != nil {
		t.Fatalf("decode finish: %v", err)
	}
	if !decoder.SawFinishReason() || len(terminal) == 0 || terminal[len(terminal)-1].Type != MaheshvaraEventResponseCompleted {
		t.Fatalf("nested body tree finish annotation must compile: %#v", terminal)
	}
}
