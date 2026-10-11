package server

import (
	"context"
	"fmt"
	"slices"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func conversionPolicyRank(selector protocol.ConversionMatch) int {
	if selector.SourceID == "" && selector.TargetID == "" && selector.SourceFamily == "" && selector.TargetFamily == "" && selector.SourceWire == "" && selector.TargetWire == "" {
		return 0
	}
	return 1
}

func resolveGatewayConversion(policies []storage.ConversionPolicyRecord, bindings []storage.ProtocolBinding, ingress, upstream *protocol.Compiled, model config.ModelRef, operation string, transport protocol.Transport) (*protocol.CompiledConversion, error) {
	layers := []protocol.ConversionPolicy{protocol.DefaultConversionPolicy(ingress, upstream)}
	route := protocol.ConversionContext{Source: ingress.Identity(), Target: upstream.Identity(), Model: model.Identifier(), Operation: operation, Transport: transport}
	for rank := 0; rank <= 1; rank++ {
		matched := false
		for _, p := range policies {
			if p.ActiveHash == "" || conversionPolicyRank(p.Selector) != rank || !p.Selector.MatchesContext(route) {
				continue
			}
			if matched {
				return nil, fmt.Errorf("ambiguous active conversion policy")
			}
			matched = true
			layers = append(layers, p.Policy)
		}
	}
	for _, kind := range []string{"source", "model"} {
		for _, b := range bindings {
			if b.Kind != kind || b.SourceID != model.SourceID || (kind == "model" && b.ModelID != model.ID) || b.Conversion == nil {
				continue
			}
			selection := b.Conversion
			if selection.PolicyID != "" {
				found := false
				for _, p := range policies {
					if p.ID == selection.PolicyID && p.Hash == selection.RevisionHash {
						layers = append(layers, p.Policy)
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("conversion policy revision is unavailable: %s", selection.PolicyID)
				}
			}
			if selection.Overrides != nil {
				layer := *selection.Overrides
				layer.ID = kind + "-override"
				layers = append(layers, layer)
			}
		}
	}
	return protocol.ResolveConversion(layers...)
}

type conversionBindingScope struct {
	changed *storage.ProtocolBinding
	isolate bool
}

func (s *Server) verifyConversionBindings(ctx context.Context, view conversionRegistry, policies []storage.ConversionPolicyRecord, bindings []storage.ProtocolBinding, scopes ...conversionBindingScope) ([]storage.ProtocolBinding, error) {
	scope := conversionBindingScope{}
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	updated := slices.Clone(bindings)
	models, err := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{ShouldIncludeDisabledSources: true})
	if err != nil {
		return nil, err
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	sourceKeys := collectSourceKeys(sources)
bindingLoop:
	for i := range updated {
		b := &updated[i]
		if changed := scope.changed; changed != nil && upgradeBindingIdentity(*b) != upgradeBindingIdentity(*changed) && !(changed.Kind == "source" && b.Kind == "model" && b.SourceID == changed.SourceID) {
			continue
		}
		if b.Unbound {
			continue
		}
		reject := func(err error) bool {
			if !scope.isolate {
				return false
			}
			b.Combinations = []protocol.CombinationReport{{CompilerVersion: protocol.CompilerVersion, Fidelity: "rejected", Issues: []protocol.ConversionIssue{{Code: protocol.VerificationRequired, Severity: protocol.SeverityError, Path: "/binding", Reason: err.Error()}}}}
			return true
		}
		upstream, ok := view.Pin(b.Binding.ProtocolID)
		if !ok {
			err := fmt.Errorf("inactive binding protocol %s", b.Binding.ProtocolID)
			if reject(err) {
				continue
			}
			return nil, err
		}
		issues := protocol.CheckBinding(b.Binding, upstream)
		if b.Kind == "group" {
			issues = protocol.CheckIngressBinding(b.Binding, upstream)
		}
		if err := protocol.IssuesError(issues); err != nil {
			if reject(err) {
				continue
			}
			return nil, err
		}
		if b.Kind == "group" {
			continue
		}
		references := s.bindingModelReferences(*b, models, sourceKeys)
		b.Combinations = nil
		for _, id := range view.IDs() {
			ingress, _ := view.Pin(id)
			if !ingress.Supports(protocol.DecodeRequest) || (!ingress.Supports(protocol.EncodeResponse) && !ingress.Supports(protocol.EncodeEvent)) {
				continue
			}
			seen := map[string]bool{}
			for _, model := range references {
				// A source report excludes model overrides; model-level evidence
				// is computed with that model's complete inheritance instead.
				layers := bindings
				if b.Kind == "source" {
					layers = slices.DeleteFunc(slices.Clone(bindings), func(v storage.ProtocolBinding) bool { return v.Kind == "model" })
				}
				for operationName, operation := range upstream.Operations() {
					if (operation.Kind != "generate" && operation.Kind != "submit" && operation.Kind != "session") || !slices.Contains(b.Binding.Transports, operation.Transport) || (b.Binding.Operation != "" && b.Binding.Operation != operationName) {
						continue
					}
					conversion, err := resolveGatewayConversion(policies, layers, ingress, upstream, model, operationName, operation.Transport)
					if err != nil {
						if reject(err) {
							continue bindingLoop
						}
						return nil, err
					}
					if err := s.loadProviderConversionEvidence(ctx, conversion, model, upstream); err != nil {
						if reject(err) {
							continue bindingLoop
						}
						return nil, err
					}
					if conversion.ContextDependent() {
						conversion = conversion.WithVerificationContext(protocol.ConversionContext{Source: ingress.Identity(), Target: upstream.Identity(), Model: model.Identifier(), Operation: operationName, Transport: operation.Transport})
					}
					key := conversion.Hash + conversion.ContextHash()
					if seen[key] {
						continue
					}
					seen[key] = true
					b.Combinations = append(b.Combinations, protocol.VerifyBindingProfiles(ctx, ingress, upstream, b.Binding.Capabilities, conversion)...)
				}
			}
		}
		if !hasPassingGatewayCombination(b.Combinations) && !scope.isolate {
			return nil, fmt.Errorf("conversion policy leaves binding %s/%s without a verified route", b.SourceID, b.ModelID)
		}
	}
	return updated, nil
}

type conversionRegistry interface {
	IDs() []string
	Pin(string) (*protocol.Compiled, bool)
}
type conversionDefinitions map[string]*protocol.Compiled

func (d conversionDefinitions) IDs() []string {
	ids := []string{}
	for id := range d {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
func (d conversionDefinitions) Pin(id string) (*protocol.Compiled, bool) {
	v, ok := d[id]
	return v, ok
}

func (s *Server) bindingModelReferences(binding storage.ProtocolBinding, models []storage.Model, sourceKeys map[string]sourceKeyMeta) []config.ModelRef {
	references := []config.ModelRef{}
	for _, model := range models {
		if model.SourceID == binding.SourceID && (binding.Kind == "source" || model.ID == binding.ModelID) {
			if ref, ok := s.resolveModelSource(model, sourceKeys); ok {
				references = append(references, ref)
			}
		}
	}
	if len(references) == 0 {
		references = append(references, config.ModelRef{ID: binding.ModelID, SourceID: binding.SourceID, Name: binding.ModelID})
	}
	return references
}
