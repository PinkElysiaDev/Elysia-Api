package server

import "github.com/elysia-api/backend/protocol"

// gatewayResponsePlan is the explicit handoff from preparation to delivery.
// It cannot retain request history, candidate probes or alternative candidates.
type gatewayResponsePlan struct {
	ingress      *protocol.Compiled
	operation    protocol.Operation
	clientModel  protocol.Value
	clientOutput *protocol.ClientOutput
}

func (plan *gatewayPlan) responsePlan(output *protocol.ClientOutput) *gatewayResponsePlan {
	return &gatewayResponsePlan{ingress: plan.ingress, operation: plan.operation, clientModel: plan.request.Model, clientOutput: output}
}

// takeCandidate transfers one attempt out of the retry owner. The caller must
// retain only its name after dispatch; the remaining candidates stay available
// until retry is impossible or a successful upstream status has been received.
func (plan *gatewayPlan) takeCandidate(index, attempts int) gatewayCandidate {
	candidate := plan.candidates[index]
	plan.candidates[index] = gatewayCandidate{}
	candidate.finalAttempt = index+1 == attempts
	return candidate
}

func (plan *gatewayPlan) releasePreparation() {
	for index := range plan.candidates {
		plan.candidates[index].releaseRequestEncoding()
		plan.candidates[index] = gatewayCandidate{}
	}
	plan.candidates = nil
	plan.request = nil
	plan.carriers = nil
}
