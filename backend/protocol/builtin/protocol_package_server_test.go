package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageServerArtifacts(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	options := p.EvaluationContext{Scope: scope}
	for _, item := range []string{
		`{"type":"file_search_call","id":"file","status":"completed","queries":[],"results":null}`,
		`{"type":"file_search_call","id":"file","status":"completed","queries":[],"results":[]}`,
		`{"type":"file_search_call","id":"file","status":"completed","queries":[]}`,
		`{"type":"image_generation_call","id":"image","status":"completed","result":null,"output_format":null}`,
		`{"type":"code_interpreter_call","id":"code","status":"completed","code":null,"container_id":"container","outputs":null}`,
		`{"type":"code_interpreter_call","id":"code","status":"completed","code":"","container_id":"container","outputs":[]}`,
		`{"type":"web_search_call","id":"search","status":"completed","extension":{"n":9007199254740993}}`,
	} {
		t.Run(item, func(t *testing.T) {
			raw := `{"id":"r","model":"m","object":"response","status":"completed","created_at":1,"output":[` + item + `]}`
			response, err := compiled.DecodeResponse(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Content) != 1 || response.Content[0].Kind != p.ServerToolNode || response.Content[0].Native != nil {
				t.Fatal("execution was not mapped independently", response.Content)
			}
			response.Native = nil
			wire, err := compiled.EncodeResponse(t.Context(), response, options)
			if err != nil {
				t.Fatal("artifact reconstruction", err)
			}
			object, err := testValue(t, string(wire)).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			if !equalAgentMigrationValues(t, object["output"], testValue(t, "["+item+"]")) {
				t.Fatal("artifact presence, null or extension changed", string(wire))
			}
			history := &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Content: response.Content}
			request, err := compiled.EncodeRequest(t.Context(), history, options)
			if err != nil {
				t.Fatal("execution history", err)
			}
			object, err = testValue(t, string(request)).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			if !equalAgentMigrationValues(t, object["input"], testValue(t, "["+item+"]")) {
				t.Fatal("history reconstruction changed execution", string(request))
			}
		})
	}
	raw := `{"id":"r","model":"m","object":"response","status":"completed","created_at":1,"output":[{"type":"code_interpreter_call","id":"code","status":"completed","code":null,"container_id":"container","outputs":null}]}`
	response, err := compiled.DecodeResponse(t.Context(), []byte(raw), options)
	if err != nil {
		t.Fatal(err)
	}
	response.Native = nil
	other := options
	other.Scope.Account = "other"
	if _, err := compiled.EncodeResponse(t.Context(), response, other); err == nil {
		t.Fatal("container crossed its source account")
	}
}

func responsesServerStream(t *testing.T) []string {
	t.Helper()
	final := []string{
		`{"type":"web_search_call","id":"search","status":"completed","action":{"type":"search","query":"q"}}`,
		`{"type":"file_search_call","id":"file","status":"completed","queries":["q"],"results":[{"file_id":"file-1","filename":"notes.txt","text":"excerpt","score":0.5,"attributes":null}]}`,
		`{"type":"image_generation_call","id":"image","status":"completed","result":"AQID","output_format":"png"}`,
		`{"type":"code_interpreter_call","id":"code","status":"completed","code":"print(1)","container_id":"container","outputs":[{"type":"logs","logs":"1"}]}`,
	}
	initial := []string{
		`{"type":"web_search_call","id":"search","status":"in_progress"}`,
		`{"type":"file_search_call","id":"file","status":"in_progress","queries":[],"results":null}`,
		`{"type":"image_generation_call","id":"image","status":"in_progress","result":null,"output_format":"png"}`,
		`{"type":"code_interpreter_call","id":"code","status":"in_progress","code":null,"container_id":"container","outputs":null}`,
	}
	frames := []string{`{"type":"response.created","response":{"id":"r","model":"m","object":"response","status":"in_progress","created_at":1,"output":[]}}`}
	for index, item := range initial {
		frames = append(frames, fmt.Sprintf(`{"type":"response.output_item.added","output_index":%d,"item":%s}`, index, item))
	}
	frames = append(frames,
		`{"type":"response.web_search_call.searching","output_index":0,"item_id":"search"}`,
		`{"type":"response.file_search_call.searching","output_index":1,"item_id":"file"}`,
		`{"type":"response.image_generation_call.partial_image","output_index":2,"item_id":"image","partial_image_index":0,"partial_image_b64":"AQI="}`,
		`{"type":"response.code_interpreter_call_code.delta","output_index":3,"item_id":"code","delta":"print("}`,
		`{"type":"response.code_interpreter_call_code.delta","output_index":3,"item_id":"code","delta":"1)"}`,
		`{"type":"response.code_interpreter_call.interpreting","output_index":3,"item_id":"code"}`,
		`{"type":"response.code_interpreter_call_code.done","output_index":3,"item_id":"code","code":"print(1)"}`,
		`{"type":"response.code_interpreter_call.completed","output_index":3,"item_id":"code"}`,
	)
	for index, item := range final {
		frames = append(frames, fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item))
	}
	frames = append(frames, `{"type":"response.completed","response":{"id":"r","model":"m","object":"response","status":"completed","created_at":1,"output":[`+strings.Join(final, ",")+`],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"tool_usage":{"web_search":{"num_requests":0}}}}`)
	for index, raw := range frames {
		frames[index] = fmt.Sprintf(`{"sequence_number":%d,%s`, index, raw[1:])
	}
	return frames
}

func TestResponsesPackageServerStream(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	raws := responsesServerStream(t)
	frames, response := decodePackageStream(t, compiled, raws, scope)
	for index, raw := range raws {
		fields, err := testValue(t, raw).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		if fields["type"].IsString("response.code_interpreter_call_code.done") {
			item := frames[index].Events[0].Item
			if item == nil || !item.Status.IsString("interpreting") || !item.Input.Value.IsString("print(1)") {
				t.Fatal("code snapshot regressed its observed execution state", item)
			}
		}
	}
	check := func(response *p.Response) {
		t.Helper()
		if len(response.Content) != 4 || response.Usage == nil || response.Usage.Total == nil || response.Usage.Total.Count != 3 {
			t.Fatal("server response or model usage changed", response)
		}
		for _, node := range response.Content {
			if node.Kind != p.ServerToolNode || !node.CallID.IsZero() {
				t.Fatal("upstream execution became a client tool call", node)
			}
		}
		if !response.Content[3].Input.Value.IsString("print(1)") || !response.Content[1].Children[0].Attributes["excerpt"].IsString("excerpt") || !response.Content[2].Children[0].Payload.IsObject() {
			t.Fatal("server input or artifacts lost", response.Content)
		}
		for _, name := range []string{"web_search", "file_search", "image_generation", "code_interpreter"} {
			expected := p.Counter{Count: 1, Origin: p.InferredCount}
			if name == "web_search" {
				expected = p.Counter{Count: 0, Origin: p.ObservedCount}
			}
			if response.Usage.Details["tools."+name+"_calls"] != expected {
				t.Fatal("execution count did not respect authoritative zero or was duplicated", name, response.Usage)
			}
		}
	}
	check(response)
	rendered := encodePackageStream(t, compiled, frames, scope, false)
	_, rebuilt := decodePackageStream(t, compiled, rendered, scope)
	check(rebuilt)
	for _, tag := range []string{"response.code_interpreter_call_code.done", "response.image_generation_call.partial_image", "response.code_interpreter_call.interpreting"} {
		count := 0
		for _, raw := range rendered {
			fields, err := testValue(t, raw).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			if fields["type"].IsString(tag) {
				count++
			}
		}
		if count != 1 {
			t.Fatal("server observation was omitted or duplicated", tag, count)
		}
	}
	native := encodePackageStream(t, compiled, frames, scope, true)
	if len(native) != len(raws) {
		t.Fatal("native server frames duplicated", len(native), len(raws))
	}
	for index, raw := range raws {
		if native[index] != raw {
			t.Fatal("native server bytes changed", index)
		}
	}
	response.Native = nil
	request, err := compiled.EncodeRequest(t.Context(), &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Content: response.Content}, p.EvaluationContext{Scope: scope})
	if err != nil {
		t.Fatal("server follow-up", err)
	}
	if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(request))); err != nil {
		t.Fatal(err)
	}
}
