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
	revisions := make(map[storage.ConversionRevisionKey]protocol.ConversionPolicy, len(policies))
	for _, record := range policies {
		revisions[record.Key()] = record.Policy
	}
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
				policy, exists := revisions[storage.ConversionRevisionKey{ID: selection.PolicyID, Hash: selection.RevisionHash}]
				if !exists {
					return nil, fmt.Errorf("conversion policy revision is unavailable: %s", selection.PolicyID)
				}
				layers = append(layers, policy)
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
	keys    map[storage.BindingKey]bool
	isolate bool
}

func (scope conversionBindingScope) includes(key storage.BindingKey) bool {
	if scope.changed != nil {
		return scope.changed.Key().Affects(key)
	}
	if scope.keys == nil {
		return true
	}
	for changed := range scope.keys {
		if changed.Affects(key) {
			return true
		}
	}
	return false
}

func (s *Server) prepareBindingUpdate(ctx context.Context, view conversionRegistry, policies []storage.ConversionPolicyRecord, bindings []storage.ProtocolBinding, changed storage.ProtocolBinding) ([]storage.ProtocolBinding, error) {
	next := slices.DeleteFunc(slices.Clone(bindings), func(binding storage.ProtocolBinding) bool { return binding.Key() == changed.Key() })
	next = append(next, changed)
	scope := conversionBindingScope{changed: &changed}
	verified, err := s.verifyConversionBindings(ctx, view, policies, next, scope)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(verified, func(binding storage.ProtocolBinding) bool { return !scope.includes(binding.Key()) }), nil
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
	for i := range updated {
		b := &updated[i]
		if !scope.includes(b.Key()) {
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
		checks, err := s.resolveBindingConversions(ctx, view, policies, bindings, *b, s.bindingModelReferences(*b, models, sourceKeys))
		if err != nil {
			if reject(err) {
				continue
			}
			return nil, err
		}
		b.Combinations = nil
		for _, check := range checks {
			b.Combinations = append(b.Combinations, protocol.VerifyBindingProfiles(ctx, check.ingress, upstream, b.Binding.Capabilities, check.conversion)...)
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
