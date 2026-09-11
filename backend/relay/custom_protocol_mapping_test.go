package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func compileBodyOrDie(t *testing.T, raw string) customBodyCompileResult {
	t.Helper()
	result, err := compileCustomProtocolBody(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("compileCustomProtocolBody: %v", err)
	}
	return result
}

func TestCompileCustomProtocolBody(t *testing.T) {
	body := `{
	  "model": {"field": "model", "mode": "string"},
	  "input": {"field": "messages"},
	  "params": {
	    "temperature": {"field": "temperature", "default": 0.7, "omitIfEmpty": true},
	    "api_version": {"value": "2026-01-01"},
	    "seed": 42,
	    "nested": [{"field": "top_p", "omitIfEmpty": true}]
	  },
	  "stream": {"field": "stream"}
	}`
	result := compileBodyOrDie(t, body)
	// 对象键按字典序稳定输出；叶子按 mode 决定引号内/外形态。
	expected := `{"input": {{maheshvara.messages}}, "model": "{{maheshvara.model}}", "params": {"api_version": "2026-01-01", "nested": [{{maheshvara.top_p}}], "seed": 42, "temperature": {{maheshvara.temperature | default:0.7}}}, "stream": {{maheshvara.stream}}}`
	if result.Template != expected {
		t.Fatalf("compiled template mismatch:\n got: %s\nwant: %s", result.Template, expected)
	}
	if len(result.OmitIfEmpty) != 2 || result.OmitIfEmpty[0] != "params.nested[0]" || result.OmitIfEmpty[1] != "params.temperature" {
		t.Fatalf("unexpected omitIfEmpty: %#v", result.OmitIfEmpty)
	}
	// string 形态字段未显式指定 mode 时默认引号内。
	result = compileBodyOrDie(t, `{"m": {"field": "model"}}`)
	if result.Template != `{"m": "{{maheshvara.model}}"}` {
		t.Fatalf("string-shape default mode mismatch: %s", result.Template)
	}
}

func TestCompileCustomProtocolBodyRejectsUnknown(t *testing.T) {
	cases := []string{
		`{"a": {"field": "nonexistent"}}`,                // 未知请求字段
		`{"a": {"field": "model", "mode": "yaml"}}`,      // 非法 mode
		`{"a": {"mode": "string"}}`,                      // 缺 field 且非合法常量
		`{"a": {"field": "model", "extra": 1}}`,          // 未知注解键
		`{"a": {"field": "model", "omitIfEmpty": true}}`, // 根下 omitIfEmpty 无意义但允许
	}
	for _, body := range cases[:4] {
		if _, err := compileCustomProtocolBody(json.RawMessage(body)); err == nil {
			t.Fatalf("expected error for %s", body)
		}
	}
	if _, err := compileCustomProtocolBody(json.RawMessage(cases[4])); err != nil {
		t.Fatalf("root omitIfEmpty (non-root path) should compile: %v", err)
	}
}

func TestCompileCustomProtocolResponseFields(t *testing.T) {
	fields := []CustomProtocolResponseFieldMapping{
		{Path: "result.text", Field: "text"},
		{Path: "result.reason", Field: "reasoning"},
		{Path: "finish", Field: "stop_reason"},
		{Path: "usage.prompt", Field: "usage.input_tokens"},
		{Path: "usage.done", Field: "usage.output_tokens", Transform: "int"},
		{Path: "vendor.tier", Field: "service_tier"},
		{Path: "vendor.tag", Field: "metadata.vendor"},
	}
	compiled, err := compileCustomProtocolResponseFields("response", fields)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if compiled.TextPath != "result.text" || compiled.ReasoningPath != "result.reason" || compiled.FinishReasonPath != "finish" {
		t.Fatalf("direct paths mismatch: %#v", compiled)
	}
	if len(compiled.FieldMappings) != 4 {
		t.Fatalf("expected 4 field mappings, got %#v", compiled.FieldMappings)
	}
	first := compiled.FieldMappings[0]
	if first.Target != "usage.input_tokens" || first.Source != "usage.prompt" || first.Transform != "int" {
		t.Fatalf("usage.* should default to int transform: %#v", first)
	}

	duplicate := append(fields, CustomProtocolResponseFieldMapping{Path: "other", Field: "text"})
	if _, err := compileCustomProtocolResponseFields("response", duplicate); err == nil {
		t.Fatal("duplicate text mapping must be rejected")
	}
	bad := []CustomProtocolResponseFieldMapping{{Path: "a", Field: "not_a_field"}}
	if _, err := compileCustomProtocolResponseFields("response", bad); err == nil {
		t.Fatal("unknown response field must be rejected")
	}
}

// 声明式配置注册后，渲染与映射链路与 legacy 模板行为等价。
func TestDeclarativeProtocolEndToEnd(t *testing.T) {
	ClearCustomProtocols()
	t.Cleanup(ClearCustomProtocols)
	config := CustomProtocolConfig{
		ID:   "declarative",
		Type: CustomProtocolTypeLLM,
		Request: CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v2/generate",
			Body: json.RawMessage(`{
			  "model": {"field": "model", "mode": "string"},
			  "messages": {"field": "messages"},
			  "temperature": {"field": "temperature", "default": 0.2, "omitIfEmpty": true},
			  "stream": {"field": "stream"},
			  "api_version": {"value": "2026-01-01"}
			}`),
		},
		Response: CustomProtocolResponse{
			Fields: []CustomProtocolResponseFieldMapping{
				{Path: "answer.text", Field: "text"},
				{Path: "finish", Field: "stop_reason"},
				{Path: "usage.prompt", Field: "usage.input_tokens"},
				{Path: "usage.completion", Field: "usage.output_tokens"},
			},
		},
	}
	if err := RegisterCustomProtocol(config); err != nil {
		t.Fatalf("register declarative protocol: %v", err)
	}

	temperature := 0.9
	request := &MaheshvaraRequest{
		Model:       "vendor-model",
		Messages:    []MaheshvaraMessage{{Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "hi"}}}},
		Temperature: &temperature,
		Stream:      true,
	}
	rendered, err := RenderCustomProtocolRequest(request, config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rendered.Body, &body); err != nil {
		t.Fatalf("rendered body is not JSON: %v (%s)", err, rendered.Body)
	}
	if body["model"] != "vendor-model" || body["api_version"] != "2026-01-01" || body["stream"] != true {
		t.Fatalf("rendered scalars mismatch: %s", rendered.Body)
	}
	if body["temperature"] != 0.9 {
		t.Fatalf("temperature should render 0.9, got %#v", body["temperature"])
	}
	if _, ok := body["messages"].([]any); !ok {
		t.Fatalf("messages should render as native array: %s", rendered.Body)
	}

	// temperature 缺省 → default 生效；空数组/空值 + omitIfEmpty → 键被删除。
	rendered, err = RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m", Messages: []MaheshvaraMessage{}}, config)
	if err != nil {
		t.Fatalf("render minimal: %v", err)
	}
	body = map[string]any{}
	_ = json.Unmarshal(rendered.Body, &body)
	if body["temperature"] != 0.2 {
		t.Fatalf("default should apply, got %#v", body["temperature"])
	}

	upstream := `{"answer":{"text":"ok"},"finish":"stop","usage":{"prompt":2,"completion":3}}`
	mapped, err := CustomProtocolResponseToMaheshvara([]byte(upstream), config)
	if err != nil {
		t.Fatalf("map response: %v", err)
	}
	if text := maheshvaraOutputText(mapped); text != "ok" {
		t.Fatalf("mapped text mismatch: %q", text)
	}
	if mapped.StopReason != "stop" {
		t.Fatalf("stop reason mismatch: %q", mapped.StopReason)
	}
	if mapped.Usage == nil || mapped.Usage.InputTokens != 2 || mapped.Usage.OutputTokens != 3 {
		t.Fatalf("usage mapping mismatch: %#v", mapped.Usage)
	}
}

// legacy：不含字段引用的 request.body 维持"原样模板"语义。
func TestLegacyPlainBodyStillWorks(t *testing.T) {
	config := CustomProtocolConfig{
		ID:   "legacy-plain",
		Type: CustomProtocolTypeLLM,
		Request: CustomProtocolRequest{
			Body: json.RawMessage(`{"echo":true,"model":"{{maheshvara.model}}"}`),
		},
		Response: CustomProtocolResponse{TextPath: "data"},
	}
	if err := ValidateCustomProtocol(config); err != nil {
		t.Fatalf("legacy plain body should validate: %v", err)
	}
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m"}, config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(rendered.Body), `"model":"m"`) || !strings.Contains(string(rendered.Body), `"echo":true`) {
		t.Fatalf("legacy body should render verbatim: %s", rendered.Body)
	}
}

func maheshvaraOutputText(response *MaheshvaraResponse) string {
	var parts []string
	for _, item := range response.Output {
		for _, part := range item.Content {
			if part.Type == MaheshvaraContentText && part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func TestValidateCustomProtocolDeclarativeSurfaces(t *testing.T) {
	config := CustomProtocolConfig{
		ID:   "bad-declarative",
		Type: CustomProtocolTypeLLM,
		Request: CustomProtocolRequest{
			Body: json.RawMessage(fmt.Sprintf(`{"a": {"field": %q}}`, "no_such_field")),
		},
	}
	if err := ValidateCustomProtocol(config); err == nil || !strings.Contains(err.Error(), "no_such_field") {
		t.Fatalf("unknown request field should surface, got %v", err)
	}
	config.Request.Body = json.RawMessage(`{"a": {"field": "model"}}`)
	config.Response.Fields = []CustomProtocolResponseFieldMapping{{Path: "x", Field: "text"}, {Path: "y", Field: "text"}}
	if err := ValidateCustomProtocol(config); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate field mapping should surface, got %v", err)
	}
}

func TestExtractResponseBodyFields(t *testing.T) {
	body := `{
	  "request_id": {"field": "id", "value": "req-1"},
	  "result": {
	    "message": {
	      "text": {"field": "text", "value": "你好"},
	      "reasoning": {"field": "reasoning", "value": "思考…"}
	    }
	  },
	  "choices": [
	    {"delta": {"content": {"value": "（结构占位，未映射）"}}}
	  ],
	  "weird key": {"field": "status", "value": "done"},
	  "usage": {"prompt": {"field": "usage.input_tokens", "value": 2}},
	  "flat": "纯结构占位",
	  "nested": {"const": {"value": [1, 2]}}
	}`
	fields, err := extractResponseBodyFields("response", json.RawMessage(body))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	byPath := map[string]string{}
	for _, field := range fields {
		byPath[field.Path] = field.Field
		if _, err := parseCustomPath(field.Path); err != nil {
			t.Fatalf("generated path %q must parse: %v", field.Path, err)
		}
	}
	for path, want := range map[string]string{
		"$.request_id":          "id",
		"$.result.message.text": "text",
		"$['weird key']":        "status",
		"$.usage.prompt":        "usage.input_tokens",
	} {
		if byPath[path] != want {
			t.Fatalf("path %s should map %s, got %s", path, want, byPath[path])
		}
	}

	// 同一字段在树内重复声明 → 报错。
	dup := `{"a": {"field": "text"}, "b": {"field": "text"}}`
	if _, err := extractResponseBodyFields("response", json.RawMessage(dup)); err == nil {
		t.Fatal("duplicate field in body tree must be rejected")
	}

	// 残缺注解与未知键拒绝。
	for _, bad := range []string{
		`{"a": {"transform": "int"}}`,
		`{"a": {"field": "text", "extra": 1}}`,
		`{"a": {"mode": "string"}}`,
	} {
		if _, err := extractResponseBodyFields("response", json.RawMessage(bad)); err == nil {
			t.Fatalf("expected error for %s", bad)
		}
	}
}

func TestResponseBodyTreeEndToEnd(t *testing.T) {
	config := CustomProtocolConfig{
		ID:   "resp-body-tree",
		Type: CustomProtocolTypeLLM,
		Request: CustomProtocolRequest{
			Body: json.RawMessage(`{"model": {"field": "model", "mode": "string"}}`),
		},
		Response: CustomProtocolResponse{
			Body: json.RawMessage(`{
			  "answer": {"text": {"field": "text", "value": "ok"}},
			  "finish": {"field": "stop_reason", "value": "stop"},
			  "usage": {"prompt": {"field": "usage.input_tokens", "value": 2}, "done": {"field": "usage.output_tokens", "value": 3}}
			}`),
			Fields: []CustomProtocolResponseFieldMapping{{Path: "x", Field: "text"}},
		},
	}
	if err := ValidateCustomProtocol(config); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("body+fields duplicate must be rejected, got %v", err)
	}

	config.Response.Fields = nil
	if err := ValidateCustomProtocol(config); err != nil {
		t.Fatalf("body tree should validate: %v", err)
	}

	// 树内未知响应字段在注册校验时拒绝。
	unknown := CustomProtocolConfig{
		ID:       "resp-body-unknown",
		Type:     CustomProtocolTypeLLM,
		Request:  CustomProtocolRequest{Body: json.RawMessage(`{"m": {"field": "model", "mode": "string"}}`)},
		Response: CustomProtocolResponse{Body: json.RawMessage(`{"a": {"field": "not_a_field"}}`)},
	}
	if err := ValidateCustomProtocol(unknown); err == nil || !strings.Contains(err.Error(), "not_a_field") {
		t.Fatalf("unknown response field must surface, got %v", err)
	}
	upstream := `{"answer":{"text":"ok"},"finish":"stop","usage":{"prompt":2,"done":3}}`
	mapped, err := CustomProtocolResponseToMaheshvara([]byte(upstream), config)
	if err != nil {
		t.Fatalf("map response: %v", err)
	}
	if text := maheshvaraOutputText(mapped); text != "ok" {
		t.Fatalf("text mismatch: %q", text)
	}
	if mapped.Usage == nil || mapped.Usage.InputTokens != 2 || mapped.Usage.OutputTokens != 3 {
		t.Fatalf("usage mismatch: %#v", mapped.Usage)
	}
}
