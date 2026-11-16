package server

import (
	"context"
	"slices"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

type bindingConversionKey struct {
	sourceHash, targetHash, policyHash, contextHash string
}

type bindingConversionCheck struct {
	key        bindingConversionKey
	ingress    *protocol.Compiled
	upstream   *protocol.Compiled
	conversion *protocol.CompiledConversion
}

// Evidence freshness and generation resolve the same inheritance and provider
// context. A compiler version alone cannot prove an inherited policy is current.
func (s *Server) resolveBindingConversions(ctx context.Context, view conversionRegistry, policies []storage.ConversionPolicyRecord, bindings []storage.ProtocolBinding, binding storage.ProtocolBinding, references []config.ModelRef) ([]bindingConversionCheck, error) {
	upstream, _ := view.Pin(binding.Binding.ProtocolID)
	layers := bindings
	if binding.Kind == "source" {
		layers = slices.DeleteFunc(slices.Clone(bindings), func(entry storage.ProtocolBinding) bool { return entry.Kind == "model" })
	}
	checks := []bindingConversionCheck{}
	seen := map[bindingConversionKey]bool{}
	for _, id := range view.IDs() {
		ingress, _ := view.Pin(id)
		if !ingress.Supports(protocol.DecodeRequest) || (!ingress.Supports(protocol.EncodeResponse) && !ingress.Supports(protocol.EncodeEvent)) {
			continue
		}
		for _, model := range references {
			for name, operation := range upstream.Operations() {
				if (operation.Kind != "generate" && operation.Kind != "submit" && operation.Kind != "session") || !slices.Contains(binding.Binding.Transports, operation.Transport) || (binding.Binding.Operation != "" && binding.Binding.Operation != name) {
					continue
				}
				conversion, err := resolveGatewayConversion(policies, layers, ingress, upstream, model, name, operation.Transport)
				if err != nil {
					return nil, err
				}
				if err := s.loadProviderConversionEvidence(ctx, conversion, model, upstream); err != nil {
					return nil, err
				}
				if conversion.ContextDependent() {
					conversion = conversion.WithVerificationContext(protocol.ConversionContext{Source: ingress.Identity(), Target: upstream.Identity(), Model: model.Identifier(), Operation: name, Transport: operation.Transport, Reasoning: binding.Binding.Reasoning, InterleavedThinking: operation.IsInterleavedThinking})
				}
				key := bindingConversionKey{ingress.Hash(), upstream.Hash(), conversion.Hash, conversion.ContextHash()}
				if !seen[key] {
					checks = append(checks, bindingConversionCheck{key: key, ingress: ingress, upstream: upstream, conversion: conversion})
					seen[key] = true
				}
			}
		}
	}
	return checks, nil
}

func hasBindingConversionEvidence(binding storage.ProtocolBinding, checks []bindingConversionCheck) bool {
	reports := map[bindingConversionKey][]protocol.CombinationReport{}
	for _, report := range binding.Combinations {
		if report.CompilerVersion == protocol.CompilerVersion {
			key := bindingConversionKey{report.SourceHash, report.TargetHash, report.PolicyHash, report.ContextHash}
			reports[key] = append(reports[key], report)
		}
	}
	for _, check := range checks {
		if len(reports[check.key]) == 0 || !check.upstream.HasRequestScopeEvidence(check.ingress, binding.Binding.Capabilities, reports[check.key]) {
			return false
		}
	}
	return true
}

func hasCurrentBindingRejection(binding storage.ProtocolBinding, err error) bool {
	for _, report := range binding.Combinations {
		if report.CompilerVersion != protocol.CompilerVersion || report.Fidelity != "rejected" || report.SourceHash != "" || report.TargetHash != "" {
			continue
		}
		for _, issue := range report.Issues {
			if issue.Code == protocol.VerificationRequired && issue.Path == "/binding" && issue.Reason == err.Error() {
				return true
			}
		}
	}
	return false
}
