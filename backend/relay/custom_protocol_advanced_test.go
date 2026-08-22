package relay

import (
	"strings"
	"testing"
)

func TestCustomProtocolArrayPathsAndFieldMappings(t *testing.T) {
	config := CustomProtocolConfig{
		ID:      "array-paths",
		Request: CustomProtocolRequest{BodyTemplate: `{"input":{{maheshvara.messages}}}`},
		Response: CustomProtocolResponse{
			TextPath: "output[0].content[0].text",
			FieldMappings: []CustomProtocolFieldMapping{
				{Target: "metadata.vendor", Value: []byte(`"example"`)},
				{Target: "service_tier", Source: "meta.tier", Transform: "string"},
			},
		},
	}
	response, err := CustomProtocolResponseToCanonical([]byte(`{"output":[{"content":[{"text":"ok"}]}],"meta":{"tier":"priority"}}`), config)
	if err != nil {
		t.Fatalf("map custom response: %v", err)
	}
	if len(response.Output) != 1 || canonicalText(response.Output[0].Content) != "ok" {
		t.Fatalf("array path was not resolved: %+v", response.Output)
	}
	if response.Metadata["vendor"] != "example" || response.ServiceTier != "priority" {
		t.Fatalf("field mappings were not applied: %+v", response)
	}
}

func TestCustomProtocolTextTransformExtractsNestedText(t *testing.T) {
	config := CustomProtocolConfig{
		ID:      "nested-text",
		Request: CustomProtocolRequest{BodyTemplate: `{"input":{{maheshvara.messages}}}`},
		Response: CustomProtocolResponse{FieldMappings: []CustomProtocolFieldMapping{
			{Target: "output", Source: "data", Transform: "output_items"},
		}},
	}
	response, err := CustomProtocolResponseToCanonical([]byte(`{"data":[{"id":"m1","message":{"content":"nested"}}]}`), config)
	if err != nil {
		t.Fatalf("map nested text: %v", err)
	}
	if len(response.Output) != 1 || canonicalText(response.Output[0].Content) != "nested" {
		t.Fatalf("nested text object was JSON-stringified instead of extracted: %+v", response.Output)
	}
}

func TestCustomProtocolHeaderAuthAllowsAPIKeyHeaders(t *testing.T) {
	base := CustomProtocolConfig{ID: "header-auth", Request: CustomProtocolRequest{BodyTemplate: `{}`, Auth: CustomProtocolAuth{Mode: "header", Header: "x-api-key"}}}
	if err := ValidateCustomProtocol(base); err != nil {
		t.Fatalf("x-api-key auth should be valid: %v", err)
	}
	base.Request.Auth.Header = "Authorization"
	base.Request.Auth.Prefix = "Token "
	if err := ValidateCustomProtocol(base); err != nil {
		t.Fatalf("Authorization header auth should be valid: %v", err)
	}
	base.Request.Auth.Header = "Host"
	if err := ValidateCustomProtocol(base); err == nil {
		t.Fatal("transport-managed Host auth header must be rejected")
	}
	base.Request.Auth = CustomProtocolAuth{}
	base.Request.Headers = map[string]string{"Authorization": "literal-secret"}
	if err := ValidateCustomProtocol(base); err == nil {
		t.Fatal("direct protected request header must be rejected")
	}
}

func TestReplaceCustomProtocolsIsAtomic(t *testing.T) {
	ClearCustomProtocols()
	t.Cleanup(ClearCustomProtocols)
	valid := CustomProtocolConfig{ID: "stable", Request: CustomProtocolRequest{BodyTemplate: `{}`}}
	if err := ReplaceCustomProtocols([]CustomProtocolConfig{valid}); err != nil {
		t.Fatalf("install valid registry: %v", err)
	}
	invalid := CustomProtocolConfig{ID: "broken"}
	if err := ReplaceCustomProtocols([]CustomProtocolConfig{invalid}); err == nil {
		t.Fatal("invalid registry replacement should fail")
	}
	if _, ok := GetCustomProtocol("stable"); !ok {
		t.Fatal("failed replacement discarded the previous valid registry")
	}
}

func TestCustomProtocolCumulativeStreamAndTerminalValues(t *testing.T) {
	config := CustomProtocolConfig{
		ID:      "cumulative-stream",
		Request: CustomProtocolRequest{BodyTemplate: `{}`},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			PayloadPath: "payload",
			Mode:        "cumulative",
			DoneValues:  []string{"END"},
			Events:      []string{"message"},
			Response:    &CustomProtocolResponse{TextPath: "text"},
		}},
	}
	decoder, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatalf("create stream decoder: %v", err)
	}
	events, done, err := decoder.Decode(SSEEvent{Event: "noise", Data: `{"payload":{"text":"ignored"}}`})
	if err != nil || done || len(events) != 0 {
		t.Fatalf("event filter failed: events=%+v done=%v err=%v", events, done, err)
	}
	events, done, err = decoder.Decode(SSEEvent{Event: "message", Data: `{"payload":{"text":"hel"}}`})
	if err != nil || done || len(events) != 1 || events[0].Delta != "hel" {
		t.Fatalf("first cumulative chunk failed: events=%+v done=%v err=%v", events, done, err)
	}
	events, done, err = decoder.Decode(SSEEvent{Event: "message", Data: `{"payload":{"text":"hello"}}`})
	if err != nil || done || len(events) != 1 || events[0].Delta != "lo" {
		t.Fatalf("cumulative suffix failed: events=%+v done=%v err=%v", events, done, err)
	}
	events, done, err = decoder.Decode(SSEEvent{Event: "message", Data: "END"})
	if err != nil || !done || len(events) != 1 || events[0].Type != CanonicalEventResponseCompleted {
		t.Fatalf("terminal value failed: events=%+v done=%v err=%v", events, done, err)
	}
	if !decoder.SawOutput() || !decoder.TerminalReceived() {
		t.Fatalf("stream state was not retained: output=%v terminal=%v", decoder.SawOutput(), decoder.TerminalReceived())
	}
}

func TestCustomProtocolRejectsLineBreaksInAuthPrefix(t *testing.T) {
	config := CustomProtocolConfig{ID: "bad-prefix", Request: CustomProtocolRequest{BodyTemplate: `{}`, Auth: CustomProtocolAuth{Mode: "header", Header: "x-api-key", Prefix: "bad\r\nvalue"}}}
	if err := ValidateCustomProtocol(config); err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("expected auth prefix validation error, got %v", err)
	}
}

func TestCustomProtocolValidatesNestedStreamMappings(t *testing.T) {
	t.Run("target", func(t *testing.T) {
		config := CustomProtocolConfig{
			ID:      "bad-stream-target",
			Request: CustomProtocolRequest{BodyTemplate: `{}`},
			Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{Response: &CustomProtocolResponse{
				FieldMappings: []CustomProtocolFieldMapping{{Target: "request.model", Source: "data.model"}},
			}}},
		}
		if err := ValidateCustomProtocol(config); err == nil || !strings.Contains(err.Error(), "stream.response.fieldMappings") {
			t.Fatalf("expected nested stream target validation error, got %v", err)
		}
	})

	t.Run("transform", func(t *testing.T) {
		config := CustomProtocolConfig{
			ID:      "bad-stream-transform",
			Request: CustomProtocolRequest{BodyTemplate: `{}`},
			Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{Response: &CustomProtocolResponse{
				FieldMappings: []CustomProtocolFieldMapping{{Target: "metadata.vendor", Source: "data.vendor", Transform: "execute"}},
			}}},
		}
		if err := ValidateCustomProtocol(config); err == nil || !strings.Contains(err.Error(), "unsupported transform") {
			t.Fatalf("expected nested stream transform validation error, got %v", err)
		}
	})
}

func TestCustomProtocolRejectsRenderedHeaderInjection(t *testing.T) {
	config := CustomProtocolConfig{
		ID: "dynamic-header",
		Request: CustomProtocolRequest{
			BodyTemplate: `{}`,
			Headers:      map[string]string{"X-User": "{{maheshvara.user}}"},
		},
	}
	_, err := RenderCustomProtocolRequest(&MaheshvaraRequest{User: "safe\r\nX-Evil: injected"}, config)
	if err == nil || !strings.Contains(err.Error(), "rendered header") {
		t.Fatalf("expected rendered header injection error, got %v", err)
	}
}

func TestCustomProtocolAllowsBodylessGetRequest(t *testing.T) {
	config := CustomProtocolConfig{
		ID: "health-check",
		Request: CustomProtocolRequest{
			Method:       "GET",
			PathTemplate: "/health/{{maheshvara.model}}",
			Auth:         CustomProtocolAuth{Mode: "none"},
		},
	}
	if err := ValidateCustomProtocol(config); err != nil {
		t.Fatalf("bodyless GET protocol should be valid: %v", err)
	}
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "vendor-model"}, config)
	if err != nil {
		t.Fatalf("render bodyless GET request: %v", err)
	}
	if rendered.Method != "GET" || rendered.Path != "/health/vendor-model" || len(rendered.Body) != 0 {
		t.Fatalf("unexpected bodyless GET request: %+v", rendered)
	}
}

func TestCustomProtocolOmitIfEmptyKeepsNonEmptyArrayItems(t *testing.T) {
	config := CustomProtocolConfig{
		ID: "array-omit",
		Request: CustomProtocolRequest{
			BodyTemplate: `{"items":[{"value":"keep"},{"value":""}]}`,
			OmitIfEmpty:  []string{"items[0]", "items[1].value"},
		},
	}
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{}, config)
	if err != nil {
		t.Fatalf("render array omit template: %v", err)
	}
	// 回归：omitIfEmpty 命中数组元素时真正从数组移除（旧实现置 nil 留洞，
	// 输出 {"items":[...,null]}，下游收到语义错误的 null 元素）。
	if string(rendered.Body) != `{"items":[{"value":"keep"}]}` {
		t.Fatalf("unexpected omit-if-empty result: %s", rendered.Body)
	}
}

func TestCustomProtocolValidatesStringTemplatesAtRegistration(t *testing.T) {
	tests := []struct {
		name    string
		request CustomProtocolRequest
		want    string
	}{
		{name: "path", request: CustomProtocolRequest{BodyTemplate: `{}`, PathTemplate: `/v1/{{maheshvara.model | execute}}`}, want: "request.path"},
		{name: "header", request: CustomProtocolRequest{BodyTemplate: `{}`, Headers: map[string]string{"X-Model": "{{maheshvara.model"}}, want: "request.headers"},
		{name: "query", request: CustomProtocolRequest{BodyTemplate: `{}`, Query: map[string]string{"model": "{{ | json}}"}}, want: "request.query"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCustomProtocol(CustomProtocolConfig{ID: "bad-" + test.name, Request: test.request})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.want, err)
			}
		})
	}
}
