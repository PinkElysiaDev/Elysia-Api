package server

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayPreparationHandoff(t *testing.T) {
	request := &protocol.Request{Model: protocol.StringValue("alias"), Content: []protocol.Node{{Payload: protocol.StringValue("history")}}}
	output := &protocol.ClientOutput{CollectUsage: true}
	encoding := &gatewayRequestEncoding{value: protocol.StringValue("wire"), request: request}
	plan := &gatewayPlan{request: request, carriers: []string{"carrier"}, candidates: []gatewayCandidate{{prepared: request, encoding: encoding}, {prepared: request}}}
	delivery := plan.responsePlan(output)
	first := plan.takeCandidate(0, 2)
	if first.finalAttempt || plan.candidates[0].prepared != nil || plan.candidates[1].prepared == nil || plan.request == nil {
		t.Fatal("first handoff lost retry ownership")
	}
	first.releaseRequestEncoding()
	second := plan.takeCandidate(1, 2)
	if !second.finalAttempt || second.prepared == nil {
		t.Fatal("last candidate unavailable")
	}
	plan.releasePreparation()
	plan.releasePreparation()
	if plan.request != nil || plan.candidates != nil || plan.carriers != nil || encoding.request != nil || !encoding.value.IsZero() {
		t.Fatal("preparation retained request state")
	}
	if !delivery.clientModel.IsString("alias") || delivery.clientOutput != output {
		t.Fatal("delivery policy was discarded")
	}
	if len(request.Content) != 1 {
		t.Fatal("release mutated another owner's semantic request")
	}
}
