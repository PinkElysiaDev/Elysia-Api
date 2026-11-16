package builtin

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestResponseModelFailureAttribution(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	for _, scenario := range []string{"source_missing", "source_null", "source_invalid", "source_mapping", "after_mapping", "handwritten"} {
		t.Run(scenario, func(t *testing.T) {
			raw := october14Fixture(t, "chat_missing_model")
			fields, _ := raw.ReadObject()
			switch scenario {
			case "source_null":
				fields["model"] = testValue(t, "null")
			case "source_invalid":
				fields["model"] = testValue(t, "42")
			case "source_mapping", "after_mapping":
				fields["model"] = p.StringValue("provider-model")
			}
			raw, _ = p.EncodeValue(fields)
			response, err := compiled.DecodeResponse(t.Context(), raw.Bytes(), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			route, err := (p.ConversionContext{Transport: p.HTTPJSON, Source: compiled.Identity(), Target: compiled.Identity()}).WithResponseObservations(compiled, compiled, []byte(`{"model":"sent"}`), raw.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "source_mapping" {
				response.Model = p.Value{}
			}
			if scenario == "handwritten" {
				route = p.ConversionContext{Source: route.Source, Target: route.Target, Transport: route.Transport, Model: route.Model}
			}
			wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			value := testValue(t, string(wire))
			if scenario == "after_mapping" {
				value, err = p.ApplyMutations(value, []p.Mutation{{Op: p.DeleteValue, Path: "/model"}}, p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
			}
			err = route.AttributeResponseFailure(compiled.ValidateWireOutput(p.EncodeResponse, value), response, value)
			var conversion *p.ConversionError
			if !errors.As(err, &conversion) {
				t.Fatal("missing final validation", err)
			}
			wantSource := scenario == "source_missing" || scenario == "source_null" || scenario == "source_invalid"
			issue := conversion.Issues[0]
			if (issue.Code == p.UpstreamContractViolation) != wantSource {
				t.Fatal(issue)
			}
			if wantSource && (issue.Direction != p.DecodeResponse || issue.Protocol != compiled.Identity() || issue.Path != "/model") {
				t.Fatal(issue)
			}
		})
	}
}

func october14Fixture(t *testing.T, name string) p.Value {
	t.Helper()
	body, err := os.ReadFile("testdata/audit_20261014.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err := sonic.ConfigStd.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	return testValue(t, string(fixtures[name]))
}

func TestChatResponseModelPolicyAndPrecision(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		for _, model := range []string{"missing", "null", `""`, "42", `{}`, `"provider-model"`} {
			t.Run(mode+"/"+model, func(t *testing.T) {
				raw := october14Fixture(t, "chat_missing_model")
				if model != "missing" {
					var err error
					raw, err = p.ApplyMutations(raw, []p.Mutation{{Op: p.SetValue, Path: "/model", Value: testValue(t, model)}}, p.DefaultLimits())
					if err != nil {
						t.Fatal(err)
					}
				}
				policy := p.DefaultConversionPolicy(compiled, compiled)
				if mode == "strict" {
					policy.Mode = mode
				}
				for index := range policy.Rules {
					if mode == "disabled" && policy.Rules[index].ID == "chat-response-model" {
						policy.Rules[index].Enabled = false
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: p.HTTPJSON, Model: "routing-model"}
				route, err = route.WithResponseObservations(compiled, compiled, []byte(`{"model":"sent-model"}`), raw.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				response, err := compiled.DecodeResponse(t.Context(), raw.Bytes(), fixtureOptions(p.EvaluationContext{}))
				sink := &p.DiagnosticSink{}
				var wire []byte
				if err == nil {
					response, err = conversion.Response(t.Context(), response, route, sink)
				}
				if err == nil {
					wire, err = compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
				}
				if err == nil {
					err = compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire)))
				}
				wantSuccess := model == `"provider-model"` || model == "missing" && mode == "compatible"
				if (err == nil) != wantSuccess {
					t.Fatalf("success=%v, error=%v, body=%s", wantSuccess, err, wire)
				}
				if !wantSuccess {
					return
				}
				fields, _ := testValue(t, string(wire)).ReadObject()
				expected := "provider-model"
				if model == "missing" {
					expected = "sent-model"
				}
				if !fields["model"].IsString(expected) || string(fields["vendor_integer"].Bytes()) != "9007199254740993" {
					t.Fatal(string(wire))
				}
				if model == "missing" {
					issues := sink.Issues()
					if len(issues) != 1 || issues[0].RuleID != "chat-response-model" || issues[0].Direction != p.DecodeResponse || issues[0].Protocol != compiled.Identity() || issues[0].Fidelity != "lossy_compatible" {
						t.Fatal(issues)
					}
					if strings.Contains(string(response.Native.Value.Bytes()), `"model"`) {
						t.Fatal("native evidence mutated")
					}
				} else if len(sink.Issues()) != 0 {
					t.Fatal(sink.Issues())
				}
			})
		}
	}
}

func TestChatResponseModelOriginAndCollector(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	for _, scenario := range []string{"removed", "mapped", "unknown_request", "no_origin", "stream", "failed", "collector"} {
		t.Run(scenario, func(t *testing.T) {
			raw := october14Fixture(t, "chat_missing_model")
			if scenario == "removed" {
				raw, _ = p.ApplyMutations(raw, []p.Mutation{{Op: p.SetValue, Path: "/model", Value: p.StringValue("provider-model")}}, p.DefaultLimits())
			}
			response, err := compiled.DecodeResponse(t.Context(), raw.Bytes(), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			route, err := (p.ConversionContext{Transport: p.HTTPJSON, Source: compiled.Identity(), Target: compiled.Identity()}).WithResponseObservations(compiled, compiled, []byte(`{"model":"sent-model"}`), raw.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "removed":
				response.Model = p.Value{}
			case "mapped":
				response.Model = p.StringValue("authored-model")
			case "unknown_request":
				route = p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: p.HTTPJSON}
			case "no_origin":
				route = p.ConversionContext{Source: route.Source, Target: route.Target, Transport: route.Transport, Model: route.Model}
			case "stream":
				route.Transport = p.SSE
			case "failed":
				response.Status = p.StringValue("failed")
			}
			sink := &p.DiagnosticSink{}
			var projected *p.Response
			if scenario == "collector" {
				projected, err = conversion.ProjectCollectedResponse(t.Context(), response, route, sink)
			} else {
				projected, err = conversion.Response(t.Context(), response, route, sink)
			}
			if scenario == "unknown_request" || scenario == "no_origin" {
				if err == nil {
					t.Fatal("guessed missing context")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "collector":
				if !projected.Model.IsString("sent-model") || !response.Model.IsZero() || len(sink.Issues()) != 1 {
					t.Fatal(projected.Model, response.Model, sink.Issues())
				}
			case "mapped":
				if !projected.Model.IsString("authored-model") || len(sink.Issues()) != 0 {
					t.Fatal(projected.Model, sink.Issues())
				}
			default:
				if !projected.Model.IsZero() || len(sink.Issues()) != 0 {
					t.Fatal("unexpected repair", projected.Model, sink.Issues())
				}
			}
		})
	}
}

func TestChatResponseShapeUsesOnlyExplicitCrossWireModel(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	for _, model := range []string{"", "selected-model"} {
		for _, enabled := range []bool{false, true} {
			policy := p.DefaultConversionPolicy(compiled, compiled)
			for index := range policy.Rules {
				if policy.Rules[index].ID == "response-shape" {
					policy.Rules[index].Enabled = enabled
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Status: p.StringValue("completed"), Attributes: p.Object{"created_at": testValue(t, "1")}, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}}}
			route := p.ConversionContext{Source: p.Identity{Family: "custom", WireVersion: "v1"}, Target: compiled.Identity(), Model: model, Transport: p.HTTPJSON}
			projected, err := conversion.Response(t.Context(), response, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err == nil {
				err = compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire)))
			}
			if (err == nil) != (enabled && model != "") || !response.Model.IsZero() {
				t.Fatal("model context was guessed, bypassed or mutated", model, enabled, err)
			}
			if enabled && model != "" && !projected.Model.IsString(model) {
				t.Fatal("explicit target model lost", projected.Model)
			}
		}
	}
}

func TestAuditPlaceholderStartupRemainsRejected(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	validator, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	var frames []p.Value
	if err := october14Fixture(t, "chat_placeholder_start").Decode(&frames); err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if err = validator.Consume(t.Context(), frame); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("placeholder identity changed without a contract rejection")
	}
}

func TestChatResponseModelKeepsToolArguments(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Chat)
	raw := october14Fixture(t, "chat_missing_model")
	choices := testValue(t, `[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-synthetic","type":"function","function":{"name":"audit_echo","arguments":"{\"value\":9007199254740993}"}}]},"finish_reason":"tool_calls"}]`)
	raw, err := p.ApplyMutations(raw, []p.Mutation{{Op: p.SetValue, Path: "/choices", Value: choices}}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	route, err := (p.ConversionContext{Transport: p.HTTPJSON, Source: compiled.Identity(), Target: compiled.Identity()}).WithResponseObservations(compiled, compiled, []byte(`{"model":"sent-model"}`), raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	response, err := compiled.DecodeResponse(t.Context(), raw.Bytes(), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	response, err = conversion.Response(t.Context(), response, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := testValue(t, string(wire)).ReadObject()
	if string(fields["choices"].Bytes()) != string(choices.Bytes()) || !fields["model"].IsString("sent-model") {
		t.Fatal(string(wire))
	}
	if err := compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
}
