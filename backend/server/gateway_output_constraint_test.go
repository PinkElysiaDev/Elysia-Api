package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

const gatewayOutputSchema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`
const gatewayOutputName = "answer_format"
const gatewayNumericOutputSchema = `{"type":"object","properties":{"answer":{"type":"number","enum":[9007199254740993,9007199254740992,1.25,1e-3,-0,0.0,1,1.0,10e-1]},"fixed":{"type":"integer","enum":[9007199254740993]}},"required":["answer","fixed"],"additionalProperties":false}`
const gatewayStringOutputSchema = `{"type":"object","properties":{"answer":{"type":"string","enum":["ExactCase","exactcase"," 大自在天 ","","é","e\u0301","😀","ExactCase"]}},"required":["answer"],"additionalProperties":false}`

type gatewayOutputCase struct {
	name, schema  string
	hasStringEnum bool
}

type gatewayOutputFixture struct {
	server   *Server
	packages map[string]*protocol.Compiled
	target   string
	calls    atomic.Int32
	received atomic.Pointer[protocol.Value]
}

func TestGatewayOutputConstraintDirections(t *testing.T) {
	cases := []gatewayOutputCase{
		{name: "schema", schema: gatewayOutputSchema},
		{name: "numeric-enum", schema: gatewayNumericOutputSchema},
		{name: "string-enum", schema: gatewayStringOutputSchema, hasStringEnum: true},
	}
	for _, target := range gatewayPackageNames {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			fixture := newGatewayOutputFixture(t, target)
			// A target owns its store and provider; related cases remain serial so
			// each request observes only its own call count and captured wire.
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					for _, source := range gatewayPackageNames {
						for _, operation := range []string{"generate", "stream"} {
							t.Run(source+"/"+operation, func(t *testing.T) {
								fixture.checkRequest(t, source, operation, test)
							})
						}
					}
				})
			}
		})
	}
}

func TestGatewayOutputConstraintRejectsStaleEvidence(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	packages := defaultGatewayPackages(t, server)
	source, target := packages["openai-chat"], packages["anthropic"]
	conversion, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(source, target))
	if err != nil {
		t.Fatal(err)
	}
	binding := protocol.Binding{
		ProtocolID: target.Identity().DefinitionID, RevisionHash: target.Hash(), Transports: []protocol.Transport{protocol.HTTPJSON},
		Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true, protocol.OutputConstraintsCapability: true},
	}
	candidate := gatewayCandidate{compiled: target, conversion: conversion, binding: binding,
		operation: target.Operations()["generate"], operationName: "generate", model: config.ModelRef{ID: "m", Name: "m"}}
	candidate.conversion = conversion.WithVerificationContext(candidate.conversionContext(source, false))
	reports := protocol.VerifyBindingProfiles(t.Context(), source, target, binding.Capabilities, candidate.conversion)
	request := &protocol.Request{SchemaVersion: protocol.ProtocolCoreVersion, Model: protocol.StringValue("m"),
		Content:    []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("hi")}}}},
		Parameters: protocol.Object{"outputConstraint": mustProtocolValue(t, `{"version":1,"kind":"json-schema","dialect":"json-schema","enforcement":"required","schema":`+gatewayOutputSchema+`}`)},
	}
	candidate.combinations = reports
	if _, err := matchGatewayCombination(t.Context(), source, candidate, request, binding); err != nil {
		t.Fatal("current target refusal evidence blocked a supported schema", err)
	}
	for _, field := range []string{"policy", "binding"} {
		t.Run(field, func(t *testing.T) {
			candidate.combinations = slices.Clone(reports)
			for index := range candidate.combinations {
				if field == "policy" {
					candidate.combinations[index].PolicyHash = "stale-policy"
				} else {
					candidate.combinations[index].BindingHash = "stale-binding"
				}
			}
			if _, err := matchGatewayCombination(t.Context(), source, candidate, request, binding); err == nil || !strings.Contains(err.Error(), "/binding/combinations") {
				t.Fatal("stale schema evidence authorized a route", field, err)
			}
		})
	}
}

func gatewayOutputRejectionPath(source, target string, hasStringEnum bool) string {
	if hasStringEnum && source == "anthropic" && target != source {
		return "/parameters"
	}
	if hasStringEnum && target == "anthropic" && source != target {
		return "/parameters/outputConstraint/schema/properties/answer/enum"
	}
	hasSourceName := source == "openai-chat" || source == "openai-responses"
	requiresTargetName := target == "openai-chat" || target == "openai-responses"
	if !hasSourceName && requiresTargetName {
		return "/parameters/outputConstraint/name"
	}
	return ""
}

func newGatewayOutputFixture(t *testing.T, targetName string) *gatewayOutputFixture {
	t.Helper()
	server, _ := newProtocolAdminTestServer(t)
	fixture := &gatewayOutputFixture{server: server, packages: defaultGatewayPackages(t, server), target: targetName}
	target := fixture.packages[targetName]
	jsonReply := buildPackageGatewayReply(t, target, false)
	streamReply := buildPackageGatewayReply(t, target, true)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.calls.Add(1)
		isStream, err := fixture.captureRequest(request)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if isStream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write(streamReply)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jsonReply)
	}))
	t.Cleanup(provider.Close)
	setupGatewayModelWithCapabilities(t, server, target, provider.URL, protocol.CapabilitySet{
		protocol.TextCapability: true, protocol.UsageCapability: true, protocol.OutputConstraintsCapability: true,
	})
	return fixture
}

func (fixture *gatewayOutputFixture) captureRequest(request *http.Request) (bool, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return false, err
	}
	value, err := protocol.ParseValue(body)
	if err != nil {
		return false, err
	}
	fields, err := value.ReadObject()
	if err != nil {
		return false, err
	}
	fixture.received.Store(&value)
	return fields["stream"].JSONString() == "true" || request.URL.Query().Get("alt") == "sse", nil
}

func (fixture *gatewayOutputFixture) checkRequest(t *testing.T, sourceName, operation string, test gatewayOutputCase) {
	t.Helper()
	source := fixture.packages[sourceName]
	isStream := operation == "stream"
	body := buildGatewayOutputRequest(t, sourceName, isStream, test.schema)
	path := strings.ReplaceAll(source.Operations()[operation].Path, "{model}", "group")
	request := httptest.NewRequest(http.MethodPost, "/gateway/"+source.Identity().DefinitionID+path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer gateway-test-token")
	request.Header.Set("Content-Type", "application/json")
	before := fixture.calls.Load()
	fixture.received.Store(nil)
	reply := httptest.NewRecorder()
	serveTestHTTP(fixture.server.engine, reply, request)
	if rejectionPath := gatewayOutputRejectionPath(sourceName, fixture.target, test.hasStringEnum); rejectionPath != "" {
		if reply.Code == http.StatusOK || fixture.calls.Load() != before || !strings.Contains(reply.Body.String(), rejectionPath) {
			t.Fatal("unsupported output constraint reached upstream or lost its diagnostic", rejectionPath, reply.Code, fixture.calls.Load()-before, reply.Body.String())
		}
		return
	}
	if reply.Code != http.StatusOK || fixture.calls.Load() != before+1 || reply.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
		t.Fatal("schema request failed at the actual gateway", reply.Code, fixture.calls.Load()-before, reply.Body.String())
	}
	checkGatewayOutputWire(t, fixture.target, *fixture.received.Load(), test.schema)
	response := collectPackageGatewayReply(t, source, reply, isStream, protocol.Scope{})
	checkPackageGatewayReply(t, response, "hi", 20, 2)
}

func buildGatewayOutputRequest(t *testing.T, name string, isStream bool, schema string) string {
	t.Helper()
	fields, err := mustProtocolValue(t, buildPackageGatewayRequest(name, isStream)).ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	switch name {
	case "openai-chat":
		fields["response_format"] = mustProtocolValue(t, `{"type":"json_schema","json_schema":{"name":"`+gatewayOutputName+`","strict":true,"schema":`+schema+`}}`)
	case "openai-responses":
		fields["text"] = mustProtocolValue(t, `{"format":{"type":"json_schema","name":"`+gatewayOutputName+`","strict":true,"schema":`+schema+`}}`)
	case "anthropic":
		fields["output_config"] = mustProtocolValue(t, `{"format":{"type":"json_schema","schema":`+schema+`}}`)
	case "google-generate-content":
		generation, err := fields["generationConfig"].ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		generation["responseMimeType"] = protocol.StringValue("application/json")
		generation["responseJsonSchema"] = mustProtocolValue(t, schema)
		fields["generationConfig"] = mustEncodedProtocolValue(t, generation)
	}
	return mustEncodedProtocolValue(t, fields).JSONString()
}

func readGatewayOutputField(t *testing.T, value protocol.Value, path ...string) protocol.Value {
	t.Helper()
	for _, name := range path {
		fields, err := value.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		value = fields[name]
	}
	return value
}

func checkGatewayOutputWire(t *testing.T, name string, value protocol.Value, expectedSchema string) {
	t.Helper()
	var schema protocol.Value
	if name == "google-generate-content" {
		if !readGatewayOutputField(t, value, "generationConfig", "responseMimeType").IsString("application/json") || !readGatewayOutputField(t, value, "generationConfig", "responseSchema").IsZero() {
			t.Fatal("JSON Schema was emitted with another schema dialect", value)
		}
		schema = readGatewayOutputField(t, value, "generationConfig", "responseJsonSchema")
	} else {
		var format protocol.Value
		switch name {
		case "openai-chat":
			if !readGatewayOutputField(t, value, "response_format", "type").IsString("json_schema") {
				t.Fatal("schema format type changed", value)
			}
			format = readGatewayOutputField(t, value, "response_format", "json_schema")
		case "openai-responses":
			format = readGatewayOutputField(t, value, "text", "format")
		case "anthropic":
			format = readGatewayOutputField(t, value, "output_config", "format")
		}
		if name != "openai-chat" && !readGatewayOutputField(t, format, "type").IsString("json_schema") {
			t.Fatal("schema format type changed", format)
		}
		if name == "openai-chat" || name == "openai-responses" {
			if !readGatewayOutputField(t, format, "name").IsString(gatewayOutputName) || readGatewayOutputField(t, format, "strict").JSONString() != "true" {
				t.Fatal("schema name or enforcement changed", format)
			}
		}
		schema = readGatewayOutputField(t, format, "schema")
	}
	var actual, expected any
	if err := schema.Decode(&actual); err != nil {
		t.Fatal(err)
	}
	if err := mustProtocolValue(t, expectedSchema).Decode(&expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("request schema was weakened or changed", schema)
	}
}
