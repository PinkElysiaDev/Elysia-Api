package builtin

import (
	"strings"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

const encryptedReasoningResponse = `{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"visible thought"}],"summary":[],"encrypted_content":"synthetic-opaque-state"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`

func TestResponsesEncryptedContinuationProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	scope := p.Scope{Provider: "test-provider", Account: "test-account", Model: "m"}
	for _, target := range []string{Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			to := shippedProjectionProtocol(t, target)
			r, err := from.DecodeResponse(t.Context(), []byte(encryptedReasoningResponse), p.EvaluationContext{Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Scope: scope}
			value, _ := p.EncodeValue(r)
			route, err = conversion.PreviewRecovery(p.ConversionResponse, value, route)
			if err != nil {
				t.Fatal(err)
			}
			var sink p.DiagnosticSink
			projected, err := conversion.Response(t.Context(), r, route, &sink)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wire), "visible thought") || !strings.Contains(string(wire), "OK") || strings.Contains(string(wire), "synthetic-opaque-state") {
				t.Fatal(string(wire))
			}
			if len(r.Content[0].Resources) != 1 {
				t.Fatal("source state mutated")
			}
			found := false
			for _, issue := range sink.Issues() {
				if issue.RuleID == "response-signatures" && issue.Fidelity == "recoverable_wrapped" && issue.PolicyHash != "" {
					found = true
				}
			}
			if !found {
				t.Fatal("recovery not diagnosed", sink.Issues())
			}
		})
	}
}

func TestResponsesEncryptedContinuationRestoresOnlyAuthenticatedState(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	scope := p.Scope{Provider: "provider", Account: "account", Model: "m"}
	r, err := from.DecodeResponse(t.Context(), []byte(encryptedReasoningResponse), p.EvaluationContext{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	node := r.Content[0]
	codec, _ := p.NewContinuationCodec([]byte("synthetic-test-master"))
	record := p.ContinuationRecord{Version: 1, Owner: "owner", Scope: scope, Protocol: from.Identity(), Node: node, Digest: p.ContinuationNodeDigest(node), ExpiresAt: time.Now().Unix() + 60}
	token, err := codec.Seal(record, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := codec.Open(token, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	plain := p.Node{Kind: p.ReasoningNode, Payload: p.StringValue("visible thought")}
	if err := p.RestoreContinuation(&plain, opened, "owner", scope, from.Identity()); err != nil {
		t.Fatal(err)
	}
	if len(plain.Resources) != 1 || plain.Resources[0].Kind != "encrypted_content" || plain.Resources[0].ID != p.StringValue("synthetic-opaque-state") {
		t.Fatal("encrypted state was not restored", plain.Resources)
	}
	if p.HasSignature(plain) {
		t.Fatal("encrypted state mislabeled as a provider signature")
	}
	for _, mode := range []string{"owner", "account", "model", "text"} {
		copy, changedScope, owner := plain, scope, "owner"
		switch mode {
		case "owner":
			owner = "other"
		case "account":
			changedScope.Account = "other"
		case "model":
			changedScope.Model = "other"
		case "text":
			copy.Payload = p.StringValue("edited")
		}
		if err := p.RestoreContinuation(&copy, opened, owner, changedScope, from.Identity()); err == nil {
			t.Fatal("invalid restoration accepted", mode)
		}
	}
}

func TestResponsesEncryptedContinuationPolicyBoundaries(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	scope := p.Scope{Provider: "provider", Account: "account", Model: "m"}
	r, err := from.DecodeResponse(t.Context(), []byte(encryptedReasoningResponse), p.EvaluationContext{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(to, from)
	// Isolate state protection from unrelated timestamp/item boundary loss.
	for i := range policy.Rules {
		policy.Rules[i].Enabled = policy.Rules[i].ID == "response-signatures"
	}
	policy.Mode = "strict"
	c, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Scope: scope}
	if _, err := c.Response(t.Context(), r, route, nil); err == nil {
		t.Fatal("strict mode lost opaque state")
	}
	value, _ := p.EncodeValue(r)
	recovered, err := c.PreviewRecovery(p.ConversionResponse, value, route)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := c.Response(t.Context(), r, recovered, nil)
	if err != nil || len(projected.Content[0].Resources) != 0 {
		t.Fatal(projected, err)
	}
	policy.Mode = "compatible"
	c, _ = p.ResolveConversion(policy)
	var sink p.DiagnosticSink
	if _, err := c.Response(t.Context(), r, route, &sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Issues()) != 1 || sink.Issues()[0].Fidelity != "lossy_compatible" {
		t.Fatal(sink.Issues())
	}
	for i := range policy.Rules {
		policy.Rules[i].Enabled = false
	}
	c, _ = p.ResolveConversion(policy)
	projected, err = c.Response(t.Context(), r, recovered, nil)
	if err != nil || len(projected.Content[0].Resources) != 1 {
		t.Fatal(projected, err)
	}
	if _, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{Scope: scope}); err == nil {
		t.Fatal("disabled rule secretly projected state")
	}
	var copy p.Response
	if err := value.Decode(&copy); err != nil {
		t.Fatal(err)
	}
	copy.Content[0].Resources = []p.Resource{{Kind: "file_id", ID: p.StringValue("file"), Scope: scope}}
	policy.Rules = []p.ConversionRule{{ID: "opaque-state", Order: 1, Enabled: true, Phase: p.ConversionResponse, Action: "signatures"}}
	c, _ = p.ResolveConversion(policy)
	projected, err = c.Response(t.Context(), &copy, route, nil)
	if err != nil || len(projected.Content[0].Resources) != 1 {
		t.Fatal("file reference was treated as resumable reasoning", projected, err)
	}
	if p.ContinuationResourceKey(p.Resource{Kind: "signature", ID: p.StringValue("same"), Scope: scope}) == p.ContinuationResourceKey(p.Resource{Kind: "encrypted_content", ID: p.StringValue("same"), Scope: scope}) {
		t.Fatal("resource kinds share a recovery key")
	}
}
