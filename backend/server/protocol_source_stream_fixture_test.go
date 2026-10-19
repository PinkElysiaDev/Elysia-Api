package server

import (
	"fmt"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func chatStartupFrames(t *testing.T, sample protocol.Sample, firstTime int) []protocol.Value {
	t.Helper()
	var frames []protocol.Value
	if err := sample.Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	canonicalTime := 1
	if firstTime == 81 {
		firstTime, canonicalTime = 1, 81
		for index, frame := range frames {
			value, _ := protocol.EncodeValue(canonicalTime)
			updated, err := protocol.ApplyMutations(frame, []protocol.Mutation{{Op: protocol.ReplaceValue, Path: "/created", Value: value}}, protocol.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			frames[index] = updated
		}
	}
	first, err := protocol.ParseValue([]byte(fmt.Sprintf(`{"id":"synthetic-prelude","model":"m","created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"usage":null,"system_fingerprint":null}`, firstTime)))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := protocol.ParseValue([]byte(fmt.Sprintf(`{"id":"r","model":"m","created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"service_tier":"auto"}`, canonicalTime)))
	return append([]protocol.Value{first, second}, frames...)
}
