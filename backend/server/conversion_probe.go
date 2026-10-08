package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) providerEvidenceScope(ref config.ModelRef) (string, error) {
	codec, err := protocol.NewContinuationCodec(s.config.GetDBEncryptionKey())
	if err != nil {
		return "", err
	}
	scope, _ := protocol.EncodeValue(modelProtocolScope(ref))
	return codec.Digest(string(scope.Bytes())), nil
}

func (s *Server) loadProviderConversionEvidence(ctx context.Context, c *protocol.CompiledConversion, model config.ModelRef, upstream *protocol.Compiled) error {
	needed := false
	for _, r := range c.Policy.Rules {
		needed = needed || r.Enabled && r.Action == "provider_signature"
	}
	if !needed {
		return nil
	}
	scope, err := s.providerEvidenceScope(model)
	if err != nil {
		return err
	}
	c.VerifiedProviderRules, err = s.store.ConversionProviderEvidence(ctx, c.Hash, scope, upstream.Hash(), time.Now().Unix())
	return err
}

// Explicit admin action: performs a synthetic request, never executes any tool
// the provider returns. Evidence authorizes one exact policy/account/model only.
func (s *Server) adminConversionSignatureProbe(c *gin.Context) {
	var input struct {
		Hash      string `json:"hash"`
		RuleID    string `json:"ruleId"`
		IngressID string `json:"ingressId"`
		SourceID  string `json:"sourceId"`
		ModelID   string `json:"modelId"`
		KeyIndex  *int   `json:"keyIndex,omitempty"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	fail := func(err error) { respondFail(c, http.StatusBadRequest, "conversion_verification_failed", err.Error()) }
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	ingress, ok := service.Pin(input.IngressID)
	if !ok {
		fail(fmt.Errorf("select an active ingress protocol"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	models, err := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{ShouldIncludeDisabledSources: true})
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	sourceKeys := collectSourceKeys(sources)
	var model config.ModelRef
	for _, m := range models {
		if m.ID == input.ModelID && m.SourceID == input.SourceID {
			model, _ = s.resolveModelSource(m, sourceKeys)
			break
		}
	}
	if model.ID == "" || !model.ToolsCapable {
		fail(fmt.Errorf("model requires an explicitly enabled function-tool capability"))
		return
	}
	if input.KeyIndex != nil {
		selected := false
		for _, source := range sources {
			if source.ID != input.SourceID {
				continue
			}
			index := *input.KeyIndex
			if index < 0 || index >= len(source.APIKeys) {
				break
			}
			key := source.APIKeys[index]
			for _, allowed := range source.EffectiveKeys() {
				if !key.Disabled && allowed.Value == key.Value && allowed.KeyAllowsModel(model.ID) {
					model.APIKey, selected = key.Value, true
					break
				}
			}
		}
		if !selected {
			fail(fmt.Errorf("keyIndex must select an enabled source key permitted to call this model"))
			return
		}
	}
	policies, bindings, _, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	candidate, e := makeGatewayCandidate(service.View(), bindings, model, protocol.HTTPJSON, "generate")
	if e != nil {
		fail(e)
		return
	}
	drafts, err := s.store.ListConversionPolicies(ctx, false)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	var draft *storage.ConversionPolicyRecord
	for i := range drafts {
		if drafts[i].ID == c.Param("policyId") && drafts[i].Hash == input.Hash {
			draft = &drafts[i]
			break
		}
	}
	if draft == nil {
		respondProtocolError(c, protocol.ErrRevisionConflict)
		return
	}
	next := []storage.ConversionPolicyRecord{}
	for _, p := range policies {
		if p.ID != draft.ID {
			next = append(next, p)
		}
	}
	selected := *draft
	selected.ActiveHash = selected.Hash
	selected.Selector = protocol.ConversionMatch{SourceID: input.IngressID, TargetID: candidate.compiled.Identity().DefinitionID}
	next = append(next, selected)
	conversion, err := resolveGatewayConversion(next, bindings, ingress, candidate.compiled, model, candidate.operationName, candidate.operation.Transport)
	if err != nil {
		fail(err)
		return
	}
	var signature protocol.Value
	route := candidate.conversionContext(ingress, false)
	for _, r := range conversion.Policy.Rules {
		if r.ID == input.RuleID && r.Enabled && r.Action == "provider_signature" && r.Match.MatchesContext(route) {
			signature, err = protocol.ProviderSignatureValue(r.Value)
			break
		}
	}
	if err != nil || signature.IsZero() {
		fail(fmt.Errorf("select an enabled provider_signature rule matching this protocol and model"))
		return
	}
	if err = s.validateOutbound(model.BaseURL); err != nil {
		fail(err)
		return
	}
	origin := protocol.Provenance{Protocol: candidate.compiled.Identity(), Direction: protocol.DecodeResponse, Scope: candidate.scope}
	local := protocol.Provenance{Protocol: protocol.AgentIdentity()}
	schema, _ := protocol.ParseValue([]byte(`{"type":"object","properties":{},"additionalProperties":false}`))
	args, _ := protocol.ParseValue([]byte(`{}`))
	result, _ := protocol.ParseValue([]byte(`{"result":"ok"}`))
	name, id := protocol.StringValue("elysia_signature_probe"), protocol.StringValue("elysia_probe_1")
	request, err := candidate.compiled.BuildAgentRequest(ctx, protocol.Request{SchemaVersion: protocol.SemanticSchemaVersion, Source: protocol.AgentIdentity(), Model: protocol.StringValue(model.Identifier()), Content: []protocol.Node{
		{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("This is a compatibility probe. After the tool result, reply OK. Do not call any tools.")}}},
		{Kind: protocol.ToolCallNode, CallID: id, Name: name, Input: &protocol.ToolInput{Kind: protocol.JSONInput, Value: args}, Source: &origin, Resources: []protocol.Resource{{Kind: "signature", ID: signature, Scope: candidate.scope}}},
		{Kind: protocol.ToolResultNode, CallID: id, Name: name, Payload: result, Source: &local},
	}, Tools: []protocol.Tool{{Kind: protocol.FunctionTool, Name: name, InputSchema: schema}}}, protocol.AgentPreferences{MaxOutputTokens: 32})
	if err != nil {
		fail(err)
		return
	}
	if err = protocol.IssuesError(protocol.CheckRoute(request, candidate.compiled, candidate.binding, candidate.scope, protocol.HTTPJSON)); err != nil {
		fail(err)
		return
	}
	options := protocol.EvaluationContext{Scope: candidate.scope}
	body, err := candidate.compiled.EncodeRequest(ctx, request, options)
	if err != nil {
		fail(err)
		return
	}
	if err = candidate.compiled.CheckOperationInput(candidate.operation, body); err != nil {
		fail(err)
		return
	}
	response, err := s.protocolTransport.SendProtocolRequest(ctx, model.BaseURL, model.APIKey, candidate.operation, body, map[string]string{"model": model.Identifier()})
	if err != nil {
		fail(err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fail(fmt.Errorf("upstream rejected compatibility probe with HTTP %d", response.StatusCode))
		return
	}
	body, err = protocol.ReadBoundedBody(response.Body, candidate.compiled.ResourceLimits().BufferBytes)
	if err != nil {
		fail(err)
		return
	}
	semantic, err := candidate.compiled.DecodeResponse(ctx, body, options)
	if err == nil {
		err = protocol.CheckGenerationOutcome(semantic)
	}
	if err == nil {
		err = protocol.IssuesError(protocol.CheckModelResponse(semantic, candidate.compiled, candidate.binding, candidate.scope))
	}
	if err != nil {
		fail(err)
		return
	}
	scope, err := s.providerEvidenceScope(model)
	if err != nil {
		fail(err)
		return
	}
	expires := time.Now().Add(30 * 24 * time.Hour).Unix()
	if err = s.store.SaveConversionProviderEvidence(ctx, conversion.Hash, input.RuleID, scope, candidate.compiled.Hash(), expires); err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"verified": true, "policyHash": conversion.Hash, "ruleId": input.RuleID, "targetRevision": candidate.compiled.Hash(), "modelId": model.Identifier(), "expiresAt": expires, "toolsExecuted": false})
}
