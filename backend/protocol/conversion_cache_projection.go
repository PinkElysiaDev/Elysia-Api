package protocol

import (
	"context"
	"fmt"
)

type compiledCacheProjection struct {
	spec    *cacheProjectionSpec
	effects []compiledSemanticEffect
}

func requestProjectionSchema() map[string]any {
	codec := map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}
	return map[string]any{"default": map[string]any{"targetCodec": "responses"}, "oneOf": []any{
		map[string]any{"type": "object", "required": []string{"targetCodec"}, "properties": map[string]any{"sourceCodec": codec, "targetCodec": codec}},
		map[string]any{"type": "object", "required": []string{"core"}, "properties": map[string]any{"core": map[string]any{"type": "object", "required": []string{"coreVersion", "target", "mappingHash", "effects"}}}},
	}}
}

func compileRequestProjectionOption(raw Value, limits Limits) (conversionOption, error) {
	var options requestProjectionOptions
	if err := raw.Decode(&options); err != nil {
		return conversionOption{}, err
	}
	if options.Core != nil {
		spec := options.Core
		if spec.CoreVersion != ProtocolCoreVersion || spec.MappingHash == "" || spec.Target.DefinitionID == "" || len(spec.Effects) == 0 || len(spec.Effects) > limits.Mutations {
			return conversionOption{}, fmt.Errorf("invalid core cache projection identity or effect count")
		}
		compiler := expressionCompiler{limits: limits, references: spec.Expressions, resolving: map[string]bool{}, used: map[string]bool{}}
		plan := &compiledCacheProjection{spec: spec}
		seen := map[string]bool{}
		for index, effect := range spec.Effects {
			if !definitionIdentifier.MatchString(effect.ID) || seen[effect.ID] {
				return conversionOption{}, fmt.Errorf("cache projection requires unique effect IDs")
			}
			seen[effect.ID] = true
			if effect.Scope != cacheIntentScope || (effect.Kind != "drop" && effect.Kind != "default") {
				return conversionOption{}, fmt.Errorf("core cache projection accepts only scoped default or drop effects")
			}
			if err := validateCacheDisposition(EncodeRequest, effect); err != nil {
				return conversionOption{}, err
			}
			condition, err := compiler.compilePlan(*effect.When, fmt.Sprintf("/value/core/effects/%d/when", index), expressionScope{}, 1)
			if err != nil {
				return conversionOption{}, err
			}
			if !isAssignable(condition.expression.result, BooleanType) || !localCacheCondition(condition) {
				return conversionOption{}, fmt.Errorf("cache projection when must be a local boolean condition")
			}
			plan.effects = append(plan.effects, compiledSemanticEffect{id: effect.ID, kind: effect.Kind, scope: cacheIntentScope, when: condition, severity: effect.Severity, reason: effect.Reason})
		}
		options.cachePlan = plan
	}
	return conversionOption{raw: raw, value: options}, nil
}

func (c *CompiledConversion) projectCacheIntents(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, plan *compiledCacheProjection, sink *DiagnosticSink) error {
	if plan == nil {
		return fmt.Errorf("core cache projection requires a compiled rule")
	}
	if route.Target != plan.spec.Target || request.SchemaVersion != plan.spec.CoreVersion {
		return streamIssue(InvalidInput, "/", "cache projection does not match the pinned target and core version")
	}
	if !hasRequestCacheIntents(request) {
		return nil
	}
	budget := &evaluationBudget{limits: c.phaseLimits(ctx, ConversionRequest), stepLimit: c.limits.Nodes}
	return requestCaches(request, func(intents *[]CacheIntent, at string) error {
		kept := 0
		for index, intent := range *intents {
			shouldDrop := false
			for _, effect := range plan.effects {
				isMatch, err := matchCacheDisposition(ctx, intent, effect, budget)
				if err != nil {
					return err
				}
				if !isMatch {
					continue
				}
				path := fmt.Sprintf("%s/%d", at, index)
				if intent.Kind == "resource" || intent.Resource != nil {
					return c.cacheProjectionError(rule, route, path, UnsupportedCapability, "cache resources require an explicit target representation and cannot be omitted by cache disposition")
				}
				switch intent.Kind {
				case "key", "retention", "breakpoint", "mode", "options.ttl", "prewarm":
				default:
					return c.cacheProjectionError(rule, route, path+"/kind", InvalidInput, "unknown cache intent cannot be omitted by cache disposition")
				}
				if plan.spec.Native && request.Native != nil && CanPreserveNative(request.Native.Source, Target{Protocol: plan.spec.Target, Direction: EncodeRequest}) {
					return c.cacheProjectionError(rule, route, path, UnsupportedNative, "cache disposition cannot prove that same-wire native replay will not restore the omitted field")
				}
				if effect.kind == "default" {
					c.normalized(rule, route, path, effect.reason, sink)
				} else if err := c.issueSeverity(rule, ConversionRequest, route, path, effect.reason, sink, true, effect.severity); err != nil {
					return err
				}
				shouldDrop = true
				break
			}
			if !shouldDrop {
				(*intents)[kept] = intent
				kept++
			}
		}
		if kept != len(*intents) {
			clear((*intents)[kept:])
			*intents = (*intents)[:kept]
		}
		return nil
	})
}

func (c *CompiledConversion) cacheProjectionError(rule ConversionRule, route ConversionContext, path string, code IssueCode, reason string) error {
	return candidateFailure(IssuesError([]ConversionIssue{{Code: code, Severity: SeverityError, Fidelity: "rejected", Protocol: route.Target, Direction: EncodeRequest, Stage: "conversion.request", Path: path, Reason: reason, RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID]}}))
}
