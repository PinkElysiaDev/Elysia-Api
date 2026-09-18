package relay

import (
	"testing"
)

func streamProtocol(mutate func(*CustomProtocolStreamMapping)) CustomProtocolConfig {
	stream := &CustomProtocolStreamMapping{}
	if mutate != nil {
		mutate(stream)
	}
	return CustomProtocolConfig{
		ID: "stream-features",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: CustomProtocolResponse{Stream: stream},
	}
}

// finishWhen:isTrue——每帧 finish:false 的协议不再被「字符串化非空」误判终止。
func TestCustomProtocolStreamFinishWhen(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(streamProtocol(func(s *CustomProtocolStreamMapping) {
		s.FinishWhen = &CustomProtocolMatch{Path: "finished", Op: MatchOpIsTrue}
		s.Response = &CustomProtocolResponse{TextPath: "text"}
	}))
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	events, done, err := decoder.Decode(SSEEvent{Data: `{"text":"partial","finished":false}`})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if done || decoder.TerminalReceived() {
		t.Fatalf("finished:false must NOT terminate (legacy stringification would): events=%v", events)
	}
	if len(events) != 1 || events[0].Type != MaheshvaraEventTextDelta {
		t.Fatalf("expected one text delta: %#v", events)
	}
	events, _, err = decoder.Decode(SSEEvent{Data: `{"text":" done","finished":true}`})
	if err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if !decoder.TerminalReceived() || !decoder.SawFinishReason() {
		t.Fatal("finished:true must terminate")
	}
	var completed bool
	for _, event := range events {
		if event.Type == MaheshvaraEventResponseCompleted {
			completed = true
		}
	}
	if !completed {
		t.Fatalf("terminal frame must emit completed: %#v", events)
	}
}

// finishWhen:in —— 只把白名单里的值当终止,其余(如空串/null)照常续流。
func TestCustomProtocolStreamFinishWhenIn(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(streamProtocol(func(s *CustomProtocolStreamMapping) {
		s.FinishWhen = &CustomProtocolMatch{Path: "status", Op: MatchOpIn, Value: []byte(`["stop","length"]`)}
		s.Response = &CustomProtocolResponse{TextPath: "text"}
	}))
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	if _, done, _ := decoder.Decode(SSEEvent{Data: `{"text":"a","status":"streaming"}`}); done || decoder.TerminalReceived() {
		t.Fatal("status streaming must not terminate")
	}
	if _, _, _ = decoder.Decode(SSEEvent{Data: `{"text":"b","status":"stop"}`}); !decoder.TerminalReceived() {
		t.Fatal("status stop must terminate")
	}
}

// statusWhen:覆盖硬编码 status=="completed"(大小写/别名均可表达)。
func TestCustomProtocolStreamStatusWhen(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(streamProtocol(func(s *CustomProtocolStreamMapping) {
		s.StatusWhen = &CustomProtocolMatch{Path: "phase", Op: MatchOpEquals, Value: []byte(`"DONE"`)}
		s.Response = &CustomProtocolResponse{TextPath: "text"}
	}))
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	if _, _, _ = decoder.Decode(SSEEvent{Data: `{"text":"x","phase":"running"}`}); decoder.TerminalReceived() {
		t.Fatal("phase running must not terminate")
	}
	if _, _, _ = decoder.Decode(SSEEvent{Data: `{"text":"","phase":"DONE"}`}); !decoder.TerminalReceived() {
		t.Fatal("phase DONE must terminate via statusWhen")
	}
}

// done:{json:<value>} 按解析后的类型化值终止;doneValuesReplace 移除默认 [DONE]。
func TestCustomProtocolStreamDoneJSON(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(streamProtocol(func(s *CustomProtocolStreamMapping) {
		s.DoneValuesReplace = true
		s.Done = []CustomProtocolDoneValue{
			{JSON: []byte(`{"event":"end"}`)},
			{JSON: []byte(`true`)},
		}
		s.Response = &CustomProtocolResponse{TextPath: "text"}
	}))
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	// doneValuesReplace 后 [DONE] 不再是终止值。
	if _, done, _ := decoder.Decode(SSEEvent{Data: "[DONE]"}); done {
		t.Fatal("[DONE] must be disarmed by doneValuesReplace")
	}
	if _, done, _ := decoder.Decode(SSEEvent{Data: `true`}); !done {
		t.Fatal("data: true must terminate via done json")
	}
	decoder2, err := NewCustomProtocolStreamDecoder(streamProtocol(func(s *CustomProtocolStreamMapping) {
		s.Done = []CustomProtocolDoneValue{{JSON: []byte(`{"event":"end"}`)}}
		s.Response = &CustomProtocolResponse{TextPath: "text"}
	}))
	if err != nil {
		t.Fatalf("decoder 2: %v", err)
	}
	if _, done, _ := decoder2.Decode(SSEEvent{Data: `{"event":"end"}`}); !done {
		t.Fatal("object done value must terminate (key order irrelevant)")
	}
	if _, done, _ := decoder2.Decode(SSEEvent{Data: `{"event":"other"}`}); done {
		t.Fatal("non-matching object payload must not terminate")
	}
}

// eventKeys:自定义 JSON 载荷内的事件名判别键。
func TestCustomProtocolStreamEventKeys(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(streamProtocol(func(s *CustomProtocolStreamMapping) {
		s.EventKeys = []string{"kind"}
		s.Frames = []CustomProtocolStreamFrame{
			{Event: "delta", Response: &CustomProtocolResponse{TextPath: "payload"}},
			{Event: "fin", Terminal: true},
		}
	}))
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	events, _, err := decoder.Decode(SSEEvent{Data: `{"kind":"delta","payload":"hi"}`})
	if err != nil || len(events) != 1 || events[0].Type != MaheshvaraEventTextDelta || events[0].Delta != "hi" {
		t.Fatalf("eventKeys=kind must drive frame matching: %#v err=%v", events, err)
	}
	if _, _, _ = decoder.Decode(SSEEvent{Data: `{"kind":"fin"}`}); !decoder.TerminalReceived() {
		t.Fatal("kind=fin terminal frame must terminate")
	}
	// type 字段不再参与判别:eventKeys=[kind] 时同型数据无 kind 则不匹配任何帧。
	if events, _, _ := decoder.Decode(SSEEvent{Data: `{"type":"delta","payload":"x"}`}); len(events) != 0 {
		t.Fatal("type field must not match when eventKeys is [kind]")
	}
}
