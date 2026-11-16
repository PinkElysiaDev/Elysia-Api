package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type responseUnionFixture struct {
	name  string
	value string
}

func responseInstructionFixtures() []responseUnionFixture {
	return []responseUnionFixture{
		{"EasyInputMessage", `{"content":"fixture","role":"user"}`},
		{"Message", `{"content":[{"text":"fixture","type":"input_text"}],"role":"user"}`},
		{"ResponseOutputMessage", `{"id":"fixture","content":[{"annotations":[{"file_id":"fixture","filename":"fixture","index":1,"type":"file_citation"}],"text":"fixture","type":"output_text"}],"role":"assistant","status":"in_progress","type":"message"}`},
		{"ResponseFileSearchToolCall", `{"id":"fixture","queries":["fixture"],"status":"in_progress","type":"file_search_call"}`},
		{"ResponseComputerToolCall", `{"id":"fixture","call_id":"fixture","pending_safety_checks":[{"id":"fixture"}],"status":"in_progress","type":"computer_call"}`},
		{"ComputerCallOutput", `{"call_id":"fixture","output":{"type":"computer_screenshot"},"type":"computer_call_output"}`},
		{"ResponseFunctionWebSearch", `{"id":"fixture","action":{"type":"search"},"status":"in_progress","type":"web_search_call"}`},
		{"ResponseFunctionToolCall", `{"arguments":"fixture","call_id":"fixture","name":"fixture","type":"function_call"}`},
		{"FunctionCallOutput", `{"output":"fixture","type":"function_call_output"}`},
		{"ToolSearchCall", `{"arguments":{"precise":9007199254740993},"type":"tool_search_call"}`},
		{"ResponseToolSearchOutputItemParam", `{"tools":[{"name":"fixture","type":"function"}],"type":"tool_search_output"}`},
		{"AdditionalTools", `{"role":"developer","tools":[{"name":"fixture","type":"function"}],"type":"additional_tools"}`},
		{"ResponseConfigurationUpdateItemParam", `{"type":"configuration_update"}`},
		{"ResponseReasoningItem", `{"id":"fixture","summary":[{"text":"fixture","type":"summary_text"}],"type":"reasoning"}`},
		{"ResponseCompactionItemParam", `{"encrypted_content":"fixture","type":"compaction"}`},
		{"ImageGenerationCall", `{"id":"fixture","status":"in_progress","type":"image_generation_call"}`},
		{"ResponseCodeInterpreterToolCall", `{"id":"fixture","container_id":"fixture","status":"in_progress","type":"code_interpreter_call"}`},
		{"LocalShellCall", `{"id":"fixture","action":{"command":["fixture"],"env":{"opaque":"fixture"},"type":"exec"},"call_id":"fixture","status":"in_progress","type":"local_shell_call"}`},
		{"LocalShellCallOutput", `{"id":"fixture","output":"fixture","type":"local_shell_call_output"}`},
		{"ShellCall", `{"action":{"commands":["fixture"]},"call_id":"fixture","type":"shell_call"}`},
		{"ShellCallOutput", `{"call_id":"fixture","output":[{"outcome":{"type":"timeout"},"stderr":"fixture","stdout":"fixture"}],"type":"shell_call_output"}`},
		{"ApplyPatchCall", `{"call_id":"fixture","operation":{"diff":"fixture","path":"fixture","type":"create_file"},"status":"in_progress","type":"apply_patch_call"}`},
		{"ApplyPatchCallOutput", `{"call_id":"fixture","status":"completed","type":"apply_patch_call_output"}`},
		{"McpListTools", `{"id":"fixture","server_label":"fixture","tools":[{"input_schema":{"precise":9007199254740993},"name":"fixture"}],"type":"mcp_list_tools"}`},
		{"McpApprovalRequest", `{"id":"fixture","arguments":"fixture","name":"fixture","server_label":"fixture","type":"mcp_approval_request"}`},
		{"McpApprovalResponse", `{"approval_request_id":"fixture","approve":true,"type":"mcp_approval_response"}`},
		{"McpCall", `{"id":"fixture","arguments":"fixture","name":"fixture","server_label":"fixture","type":"mcp_call"}`},
		{"ResponseCustomToolCallOutput", `{"call_id":"fixture","output":"fixture","type":"custom_tool_call_output"}`},
		{"ResponseCustomToolCall", `{"call_id":"fixture","input":"fixture","name":"fixture","type":"custom_tool_call"}`},
		{"CompactionTrigger", `{"type":"compaction_trigger"}`},
		{"ItemReference", `{"id":"fixture"}`},
		{"Program", `{"id":"fixture","call_id":"fixture","code":"fixture","fingerprint":"fixture","type":"program"}`},
		{"ProgramOutput", `{"id":"fixture","call_id":"fixture","result":"fixture","status":"completed","type":"program_output"}`},
	}
}

func responseToolChoiceFixtures() []responseUnionFixture {
	return []responseUnionFixture{
		{"null", `null`},
		{"none", `"none"`},
		{"auto", `"auto"`},
		{"required", `"required"`},
		{"ToolChoiceAllowed", `{"mode":"auto","tools":[{"opaque":{"precise":9007199254740993}}],"type":"allowed_tools"}`},
		{"file_search", `{"type":"file_search"}`},
		{"web_search_preview", `{"type":"web_search_preview"}`},
		{"computer", `{"type":"computer"}`},
		{"computer_use_preview", `{"type":"computer_use_preview"}`},
		{"computer_use", `{"type":"computer_use"}`},
		{"web_search_preview_2025_03_11", `{"type":"web_search_preview_2025_03_11"}`},
		{"image_generation", `{"type":"image_generation"}`},
		{"code_interpreter", `{"type":"code_interpreter"}`},
		{"ToolChoiceFunction", `{"name":"fixture","type":"function"}`},
		{"ToolChoiceMcp", `{"server_label":"fixture","type":"mcp"}`},
		{"ToolChoiceCustom", `{"name":"fixture","type":"custom"}`},
		{"ToolChoiceSpecificProgrammaticToolCallingParam", `{"type":"programmatic_tool_calling"}`},
		{"ToolChoiceApplyPatch", `{"type":"apply_patch"}`},
		{"ToolChoiceShell", `{"type":"shell"}`},
	}
}

func buildResponseUnionFrames(fields string) []string {
	frames := buildResponseMetadataFrames(fields)
	for index, frame := range frames {
		frame = strings.Replace(frame, "{", fmt.Sprintf(`{"sequence_number":%d,`, index), 1)
		frames[index] = strings.Replace(frame, `"response":{`, `"response":{"object":"response",`, 1)
	}
	return frames
}

func createResponseUnionValidation(t *testing.T, compiled *p.Compiled, isClient bool) *p.WireStreamValidation {
	t.Helper()
	createValidation := compiled.NewWireInputStreamValidation
	if isClient {
		createValidation = compiled.NewWireStreamValidation
	}
	validation, err := createValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validation.Release)
	return validation
}

func requireResponseUnionIssue(t *testing.T, err error, code p.IssueCode, path string) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) {
		t.Fatal("missing response union diagnostic", err)
	}
	for _, issue := range failure.Issues {
		if issue.Code == code && (issue.Path == path || strings.HasPrefix(issue.Path, path+"/")) {
			return
		}
	}
	t.Fatal("response union diagnostic lost its boundary or owned path", code, path, failure.Issues)
}

func TestResponsesMetadataUnionVariants(t *testing.T) {
	compiled := testCompiled(t, Responses)
	fixtures := []struct{ name, field, value string }{
		{"instructions/untyped-reference", "instructions", `[{"id":"previous","content":123,"future":{"n":9007199254740993}}]`},
		{"instructions/nullable-message-type", "instructions", `[{"type":null,"role":"developer","content":"policy"}]`},
		{"instructions/message-overlap", "instructions", `[{"type":"message","role":"user","content":[],"phase":"future","status":"completed"}]`},
		{"instructions/easy-message-extra-status", "instructions", `[{"type":"message","role":"assistant","content":[],"status":123}]`},
		{"instructions/large-integer", "instructions", `[{"type":"computer_call","id":"c","call_id":"call","status":"completed","pending_safety_checks":[],"action":{"type":"move","x":9223372036854775808,"y":0}}]`},
		{"tool_choice/allowed-open-member", "tool_choice", `{"type":"allowed_tools","mode":"required","tools":[{"future":true,"n":9007199254740993}]}`},
	}
	for _, fixture := range responseInstructionFixtures() {
		fixtures = append(fixtures, struct{ name, field, value string }{"instructions/" + fixture.name, "instructions", "[" + fixture.value + "]"})
	}
	for _, fixture := range responseToolChoiceFixtures() {
		fixtures = append(fixtures, struct{ name, field, value string }{"tool_choice/" + fixture.name, "tool_choice", fixture.value})
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			body := buildResponseMetadataBody(fmt.Sprintf(`%q:%s,"future_neighbor":{"n":9007199254740993}`, fixture.field, fixture.value))
			response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			meaning := "information"
			if fixture.value == "null" {
				meaning = "empty"
			}
			requireResponseMetadata(t, response.Metadata, fixture.field, fixture.value, meaning, "")
			response.Native = nil
			wire, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.ValidateResponseOutput(t.Context(), wire, wire, fixtureOptions(p.EvaluationContext{})); err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire.Bytes(), body)
		})
	}
	t.Run("error-envelope", func(t *testing.T) {
		wire := testValue(t, `{"error":{"message":"failed"},"instructions":false,"tool_choice":12}`)
		if err := compiled.ValidateResponseOutput(t.Context(), wire, wire, fixtureOptions(p.EvaluationContext{})); err != nil {
			t.Fatal("response success metadata checks changed the error envelope", err)
		}
	})
	for _, isClient := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure-envelope/client=%t", isClient), func(t *testing.T) {
			validation := createResponseUnionValidation(t, compiled, isClient)
			const frame = `{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"message":"failed"},"instructions":null,"tool_choice":null,"previous_response_id":null,"temperature":null,"text":null,"future_neighbor":{"n":9007199254740993}}}`
			if err := validation.Consume(t.Context(), testValue(t, frame)); err != nil {
				t.Fatal("valid failure metadata changed the failure boundary", err)
			}
			if err := validation.Finish(); err != nil {
				t.Fatal(err)
			}
		})
		t.Run(fmt.Sprintf("standalone-error/client=%t", isClient), func(t *testing.T) {
			validation := createResponseUnionValidation(t, compiled, isClient)
			const frame = `{"type":"error","sequence_number":1,"message":"failed","instructions":false,"tool_choice":12}`
			if err := validation.Consume(t.Context(), testValue(t, frame)); err != nil {
				t.Fatal("response metadata checks changed standalone errors", err)
			}
			if err := validation.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponsesMetadataUnionRejections(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, field, value string }{
		{"instructions-object", "instructions", `{"future":true}`},
		{"item-null", "instructions", `[null]`},
		{"item-scalar", "instructions", `["policy"]`},
		{"unknown-item-type", "instructions", `[{"type":"future","id":"r"}]`},
		{"item-type-number", "instructions", `[{"type":12,"id":"r"}]`},
		{"function-required-name", "instructions", `[{"type":"function_call","call_id":"c","arguments":"{}"}]`},
		{"unknown-message-part", "instructions", `[{"role":"developer","content":[{"type":"future"}]}]`},
		{"missing-message-text", "instructions", `[{"role":"developer","content":[{"type":"input_text"}]}]`},
		{"image-detail", "instructions", `[{"role":"developer","content":[{"type":"input_image","detail":"future"}]}]`},
		{"message-intersection", "instructions", `[{"type":"message","role":"user","content":[],"phase":"future","status":"future"}]`},
		{"output-message-status", "instructions", `[{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"x","annotations":[]}]}]`},
		{"required-schema-alias", "instructions", `[{"type":"configuration_update","text":{"format":{"type":"json_schema","name":"x"}}}]`},
		{"nested-shell-command", "instructions", `[{"type":"shell_call","call_id":"c","action":{"commands":[12]}}]`},
		{"nested-tool-parameter", "instructions", `[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":12}]}]`},
		{"tool-choice-array", "tool_choice", `[]`},
		{"unknown-choice-string", "tool_choice", `"future"`},
		{"unknown-choice-type", "tool_choice", `{"type":"future"}`},
		{"choice-missing-name", "tool_choice", `{"type":"function"}`},
		{"mcp-missing-label", "tool_choice", `{"type":"mcp","name":"lookup"}`},
		{"mcp-invalid-name", "tool_choice", `{"type":"mcp","server_label":"s","name":false}`},
		{"allowed-mode", "tool_choice", `{"type":"allowed_tools","mode":"none","tools":[]}`},
		{"allowed-member", "tool_choice", `{"type":"allowed_tools","mode":"auto","tools":[false]}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			fields := fmt.Sprintf("%q:%s", fixture.field, fixture.value)
			_, err := compiled.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fields)), fixtureOptions(p.EvaluationContext{}))
			requireResponseUnionIssue(t, err, p.UpstreamContractViolation, "/"+fixture.field)
			var exactPath string
			switch fixture.name {
			case "required-schema-alias":
				exactPath = "/instructions/0/text/format"
			case "function-required-name", "nested-shell-command":
				exactPath = "/instructions/0"
			}
			if exactPath != "" {
				requireContextIssue(t, err, p.UpstreamContractViolation, exactPath)
			}
			valid := `"instructions":"policy","tool_choice":"auto"`
			response, err := compiled.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(valid)), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			response.Native = nil
			replaceResponseMetadata(response.Metadata, fixture.field, testValue(t, fixture.value))
			wire, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			err = compiled.ValidateResponseOutput(t.Context(), wire, wire, fixtureOptions(p.EvaluationContext{}))
			requireResponseUnionIssue(t, err, p.ConversionRejected, "/"+fixture.field)
			if exactPath != "" {
				requireContextIssue(t, err, p.ConversionRejected, exactPath)
			}
			postEdit := testValue(t, buildResponseMetadataBody(fields))
			err = compiled.ValidateResponseOutput(t.Context(), testValue(t, buildResponseMetadataBody(valid)), postEdit, fixtureOptions(p.EvaluationContext{}))
			requireResponseUnionIssue(t, err, p.ConversionRejected, "/"+fixture.field)
			validFrames := buildResponseUnionFrames(valid)
			invalidFrames := buildResponseUnionFrames(fields)
			last := len(validFrames) - 1
			incomplete := strings.Replace(invalidFrames[last], `"response.completed"`, `"response.incomplete"`, 1)
			incomplete = strings.Replace(incomplete, `"status":"completed"`, `"status":"incomplete"`, 1)
			frames := []struct {
				name    string
				before  []string
				invalid string
			}{
				{"created", nil, invalidFrames[0]},
				{"progress", validFrames[:1], invalidFrames[1]},
				{"completed", validFrames[:last], invalidFrames[last]},
				{"incomplete", validFrames[:last], incomplete},
				{"failed", nil, `{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"message":"failed"},` + fields + `}}`},
			}
			for _, frame := range frames {
				for _, isClient := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/client=%t", frame.name, isClient), func(t *testing.T) {
						code := p.UpstreamContractViolation
						if isClient {
							code = p.ConversionRejected
						}
						validation := createResponseUnionValidation(t, compiled, isClient)
						for _, body := range frame.before {
							if err := validation.Consume(t.Context(), testValue(t, body)); err != nil {
								t.Fatal(err)
							}
						}
						err = validation.Consume(t.Context(), testValue(t, frame.invalid))
						requireResponseUnionIssue(t, err, code, "/response/"+fixture.field)
						if exactPath != "" {
							requireContextIssue(t, err, code, "/response"+exactPath)
						}
					})
				}
			}
		})
	}
}

func TestResponsesMetadataUnionFailureContainer(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, field := range []string{"previous_response_id", "temperature", "text"} {
		for _, isClient := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/client=%t", field, isClient), func(t *testing.T) {
				validation := createResponseUnionValidation(t, compiled, isClient)
				frame := fmt.Sprintf(`{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"message":"failed"},%q:false}}`, field)
				err := validation.Consume(t.Context(), testValue(t, frame))
				requireContextIssue(t, err, p.InvalidInput, "/response/"+field)
			})
		}
	}
}

func TestResponsesMetadataUnionStreamReconstruction(t *testing.T) {
	var items []string
	for _, fixture := range responseInstructionFixtures() {
		items = append(items, fixture.value)
	}
	instructions := "[" + strings.Join(items, ",") + "]"
	const choice = `{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"lookup"}]}`
	fields := `"instructions":` + instructions + `,"tool_choice":` + choice
	compiled := testCompiled(t, Responses)
	source := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer source.State.Release()
	target := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer target.State.Release()
	target, consume := bindFixtureOutput(t, compiled, target)
	var output []p.Value
	for _, body := range buildResponseMetadataFrames(fields) {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), source)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			event.Native = nil
			replaceResponseMetadata(event.Metadata, "tool_choice", p.StringValue("required"))
			if event.Response != nil {
				event.Response.Native = nil
				replaceResponseMetadata(event.Response.Metadata, "tool_choice", p.StringValue("required"))
			}
			frames, err := compiled.EncodeFrames(t.Context(), event, target)
			if err != nil {
				t.Fatal(err)
			}
			consume(frames)
			output = append(output, frames...)
		}
	}
	frames, err := compiled.FinishEvents(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	consume(frames)
	output = append(output, frames...)
	for _, frame := range output {
		object, _ := frame.ReadObject()
		if object["type"] != p.StringValue("response.completed") {
			continue
		}
		response, _ := object["response"].ReadObject()
		sameJSON(t, response["instructions"].Bytes(), instructions)
		if response["tool_choice"] != p.StringValue("required") {
			t.Fatal("reconstructed stream restored stale tool choice", frame)
		}
		return
	}
	t.Fatal("metadata union stream did not complete")
}
