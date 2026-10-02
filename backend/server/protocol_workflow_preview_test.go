package server

import (
	"encoding/json"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestProtocolWorkflowPreviewUsesSessionAndTaskRuntime(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session-alpha", "job-beta"} {
		definition := loadGatewayDefinition(t, name)
		value, err := protocol.EncodeValue(definition)
		if err != nil {
			t.Fatal(err)
		}
		input := protocol.PreviewInput{Definition: value}
		if name == "session-alpha" {
			input.Mode = "session"
			input.Sample = definition.SessionSamples[0].ID
		} else {
			sample := definition.TaskSamples[0]
			input.Mode, input.Operation, input.Kind, input.Purpose, input.Input = "task", sample.Operation, sample.Kind, sample.Purpose, sample.Input
		}
		expected := service.PreviewWorkflow(t.Context(), input)
		if len(expected.Issues) > 0 {
			t.Fatal(expected.Issues)
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		ctx, response := adminProtocolContext("POST", "/api/admin/protocols/preview", string(body))
		s.adminProtocolPreviewV2(ctx)
		if response.Code != 200 {
			t.Fatal(response.Body)
		}
		var decoded struct {
			Data protocol.PreviewResult `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if string(decoded.Data.Output.Bytes()) != string(expected.Output.Bytes()) || string(decoded.Data.Semantic.Bytes()) != string(expected.Semantic.Bytes()) {
			t.Fatal("UI preview diverged from shared workflow runtime")
		}
	}
}
