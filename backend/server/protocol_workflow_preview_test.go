package server

import (
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

func TestProtocolWorkflowPreviewUsesSessionAndTaskRuntime(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"sessions/session-alpha", "tasks/job-beta"} {
		document := loadGatewayPackage(t, "testdata/"+directory)
		value, err := protocol.EncodeValue(document)
		if err != nil {
			t.Fatal(err)
		}
		input := protocol.PreviewInput{Definition: value}
		if document.Spec.ID == "session-alpha" {
			input.Mode = "session"
			input.Sample = document.Mapping.SessionSamples[0].ID
		} else {
			sample := document.Mapping.TaskSamples[0]
			input.Mode, input.Operation, input.Kind, input.Purpose, input.Input = "task", sample.Operation, sample.Kind, sample.Purpose, sample.Input
		}
		expected := service.PreviewWorkflow(t.Context(), input)
		if len(expected.Issues) > 0 {
			t.Fatal(expected.Issues)
		}
		body, err := sonic.ConfigStd.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		ctx, response := adminProtocolContext("POST", "/api/admin/protocols/preview", string(body))
		s.adminProtocolPreviewV2(contextForTest(ctx), ctx)
		flushTestResponse(ctx)
		if response.Code != 200 {
			t.Fatal(response.Body)
		}
		var decoded struct {
			Data protocol.PreviewResult `json:"data"`
		}
		if err := sonic.ConfigStd.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if string(decoded.Data.Output.Bytes()) != string(expected.Output.Bytes()) || string(decoded.Data.Semantic.Bytes()) != string(expected.Semantic.Bytes()) {
			t.Fatal("UI preview diverged from shared workflow runtime")
		}
	}
}

func TestProtocolTokenCountPreviewHTTPAndCLI(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	document, _ := conversionPackage(t, protocol.PresetGeminiID).Package()
	document.Spec.ID = "count-preview"
	definition, err := protocol.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	tctx := &protocolAuthorContext{draft: definition.Bytes()}
	for _, sample := range document.Mapping.TokenCountSamples {
		if sample.ID != "count-text-decode" && sample.ID != "count-text-encode" && sample.Direction != protocol.DecodeResponse && sample.Direction != protocol.EncodeResponse {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			input := protocol.PreviewInput{Definition: definition, Mode: "count_tokens", Operation: "count_tokens", Direction: sample.Direction, Input: sample.Input,
				ConversionContext: protocol.ConversionContext{Model: "m"}}
			body, err := sonic.ConfigStd.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx, response := adminProtocolContext("POST", "/api/admin/protocols/preview", string(body))
			s.adminProtocolPreviewV2(contextForTest(ctx), ctx)
			flushTestResponse(ctx)
			var decoded struct {
				Data protocol.PreviewResult `json:"data"`
			}
			if err := sonic.ConfigStd.Unmarshal(response.Body.Bytes(), &decoded); err != nil || response.Code != 200 || len(decoded.Data.Issues) != 0 {
				t.Fatal(response.Body, err)
			}
			result := s.runCLIWithOptions(t.Context(), tctx, "elysia protocol preview --mode count_tokens --operation count_tokens --direction "+string(sample.Direction)+" --model m --sample '"+sample.Input.JSONString()+"'", true)
			if !result.OK {
				t.Fatal(result.Summary, mustCLIData(t, result))
			}
			var preview protocol.PreviewResult
			_, payload, ok := strings.Cut(result.Data.(map[string]any)["output"].(string), "\nShared runtime preview\n")
			if !ok {
				t.Fatal("CLI omitted the count preview", result)
			}
			if err := sonic.ConfigStd.Unmarshal([]byte(strings.TrimSpace(payload)), &preview); err != nil {
				t.Fatal(err)
			}
			if preview.Output.JSONString() != decoded.Data.Output.JSONString() {
				t.Fatal("HTTP and CLI count preview diverged", preview, decoded.Data)
			}
		})
	}
}
