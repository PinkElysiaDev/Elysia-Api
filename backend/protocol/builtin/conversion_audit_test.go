package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

var auditResponses = map[string]string{
	Chat:      `{"id":"r","object":"chat.completion","created":1,"model":"m","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"logprobs":null,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
	Responses: `{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","store":false,"output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"OK","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`,
	Anthropic: `{"id":"r","type":"message","model":"m","role":"assistant","content":[{"type":"text","text":"OK","citations":[]}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":1,"service_tier":"standard"}}`,
	Gemini:    `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP","finishMessage":"completed"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`,
}

func TestAuditActualPresetJSONMatrix(t *testing.T) {
	t.Parallel()
	for source, raw := range auditResponses {
		for target := range auditResponses {
			t.Run(source+"-"+target, func(t *testing.T) {
				from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, target)
				c, err := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
				if err != nil {
					t.Fatal(err)
				}
				r, err := from.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				original, _ := p.EncodeValue(r)
				sink := &p.DiagnosticSink{}
				route := fixtureConversionContext(t, c, p.ConversionResponse, from.Identity())
				route.Transport = p.HTTPJSON
				r, err = c.Response(t.Context(), r, route, sink)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(wire), "OK") {
					t.Fatal(string(wire))
				}
				if err = to.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
					t.Fatal(err, string(wire))
				}
				var body map[string]json.RawMessage
				_ = sonic.ConfigStd.Unmarshal(wire, &body)
				if target == Chat && len(body["created"]) == 0 {
					t.Fatal("missing Chat created", string(wire))
				}
				if target == Responses {
					if len(body["created_at"]) == 0 {
						t.Fatal("missing Responses created_at")
					}
					var output []struct {
						ID      string
						Status  string
						Content []map[string]json.RawMessage
					}
					_ = sonic.ConfigStd.Unmarshal(body["output"], &output)
					if len(output) == 0 || output[0].ID == "" || output[0].Status == "" || len(output[0].Content[0]["annotations"]) == 0 {
						t.Fatal("incomplete Responses message", string(wire))
					}
				}
				// Source accounting survives the client projection.
				var before p.Response
				_ = original.Decode(&before)
				if before.Usage == nil || before.Usage.Output.Count != 1 {
					t.Fatal("accounting fixture")
				}
			})
		}
	}
}

func TestAuditMetadataScopeAndStrict(t *testing.T) {
	source, target := testCompiled(t, Chat), testCompiled(t, Anthropic)
	response, err := source.DecodeResponse(t.Context(), []byte(auditResponses[Chat]), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	response.Native = nil
	wire, err := source.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := testValue(t, string(wire)).ReadObject()
	var choices []p.Object
	if err := fields["choices"].Decode(&choices); err != nil || len(choices) != 1 || !choices[0]["logprobs"].IsNull() || !fields["choiceExtensions"].IsZero() || !fields["service_tier"].IsString("default") {
		t.Fatal("metadata changed owner or presence", string(wire), err)
	}
	policy := p.DefaultConversionPolicy(target, source)
	policy.Mode = "strict"
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversion.Response(t.Context(), response, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil); err == nil {
		t.Fatal("strict metadata loss accepted")
	}
	bad := strings.Replace(auditResponses[Chat], `"service_tier":"default"`, `"service_tier":42`, 1)
	if _, err := source.DecodeResponse(t.Context(), []byte(bad), fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("invalid metadata type accepted")
	}
}

func TestAuditToolProjectionAndHoist(t *testing.T) {
	t.Parallel()
	from := shippedProjectionProtocol(t, Gemini)
	for _, name := range []string{Chat, Responses, Anthropic} {
		t.Run(name, func(t *testing.T) {
			to := shippedProjectionProtocol(t, name)
			policy := p.DefaultConversionPolicy(from, to)
			c, _ := p.ResolveConversion(policy)
			input := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{{Kind: p.ToolResultNode, CallID: p.StringValue("parallel_2"), Payload: testValue(t, `{"n":900719925474099312345,"ok":true}`)}}}
			input.Source = from.Identity()
			out, err := c.Request(t.Context(), input, fixtureConversionContext(t, c, p.ConversionRequest, input.Source), nil)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			if out.Content[0].Payload.Decode(&text) != nil || text != `{"n":900719925474099312345,"ok":true}` || out.Content[0].CallID != input.Content[0].CallID {
				t.Fatal(out)
			}
			policy.Mode = "strict"
			c, _ = p.ResolveConversion(policy)
			if _, err = c.Request(t.Context(), input, fixtureConversionContext(t, c, p.ConversionRequest, input.Source), nil); err == nil {
				t.Fatal("strict payload type loss accepted")
			}
		})
	}
	chat, anthropic := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
	input, err := chat.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"system","content":"be concise"},{"role":"developer","content":"use English"},{"role":"user","content":"go"}]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(chat, anthropic))
	out, err := c.Request(t.Context(), input, fixtureConversionContext(t, c, p.ConversionRequest, input.Source), nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := anthropic.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), `"role":"system"`) || !strings.Contains(string(wire), `"system"`) {
		t.Fatal(string(wire))
	}
}
