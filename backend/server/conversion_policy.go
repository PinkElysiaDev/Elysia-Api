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

func (s *Server) verifyConversionBindings(ctx context.Context, view conversionRegistry, policies []storage.ConversionPolicyRecord, bindings []storage.ProtocolBinding, isolate ...bool) ([]storage.ProtocolBinding, error) {
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
		if b.Unbound {
			continue
		}
		reject := func(err error) bool {
			if len(isolate) == 0 || !isolate[0] {
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
		references := []config.ModelRef{}
		for _, m := range models {
			if m.SourceID == b.SourceID && (b.Kind == "source" || m.ID == b.ModelID) {
				ref, ok := s.resolveModelSource(m, sourceKeys)
				if ok {
					references = append(references, ref)
				}
			}
		}
		if len(references) == 0 {
			references = append(references, config.ModelRef{ID: b.ModelID, SourceID: b.SourceID, Name: b.ModelID})
		}
		b.Combinations = nil
		for _, id := range view.IDs() {
			ingress, _ := view.Pin(id)
			if !ingress.Supports(protocol.DecodeRequest) || !ingress.Supports(protocol.EncodeResponse) {
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
					if operation.Kind != "generate" || !slices.Contains(b.Binding.Transports, operation.Transport) || (b.Binding.Operation != "" && b.Binding.Operation != operationName) {
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
		if !hasPassingGatewayCombination(b.Combinations) && (len(isolate) == 0 || !isolate[0]) {
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
