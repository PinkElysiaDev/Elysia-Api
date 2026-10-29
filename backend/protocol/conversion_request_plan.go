package protocol

import "fmt"

type compiledRequestProjection struct {
	spec    *requestProjectionSpec
	effects []compiledSemanticEffect
	groups  []requestProjectionGroup
}

type requestProjectionGroup struct {
	scope      string
	start, end int
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
		plan := &compiledRequestProjection{spec: spec}
		seen := map[string]bool{}
		for index, effect := range spec.Effects {
			if !definitionIdentifier.MatchString(effect.ID) || seen[effect.ID] {
				return conversionOption{}, fmt.Errorf("cache projection requires unique effect IDs")
			}
			seen[effect.ID] = true
			if !isRequestProjectionScope(effect.Scope) {
				return conversionOption{}, fmt.Errorf("core projection requires a supported semantic scope")
			}
			if err := validateRequestProjectionEffect(EncodeRequest, effect); err != nil {
				return conversionOption{}, err
			}
			condition, err := compiler.compilePlan(*effect.When, fmt.Sprintf("/value/core/effects/%d/when", index), expressionScope{}, 1)
			if err != nil {
				return conversionOption{}, err
			}
			if !isAssignable(condition.expression.result, BooleanType) || !localProjectionCondition(condition) {
				return conversionOption{}, fmt.Errorf("cache projection when must be a local boolean condition")
			}
			plan.effects = append(plan.effects, compiledSemanticEffect{id: effect.ID, kind: effect.Kind, path: effect.Path, scope: effect.Scope, when: condition, severity: effect.Severity, reason: effect.Reason})
			if len(plan.groups) == 0 || plan.groups[len(plan.groups)-1].scope != effect.Scope {
				plan.groups = append(plan.groups, requestProjectionGroup{scope: effect.Scope, start: index, end: index + 1})
			} else {
				plan.groups[len(plan.groups)-1].end = index + 1
			}
		}
		options.corePlan = plan
	}
	return conversionOption{raw: raw, value: options}, nil
}

func (c *CompiledConversion) requestProjectionError(rule ConversionRule, route ConversionContext, path string, code IssueCode, reason string) error {
	return candidateFailure(IssuesError([]ConversionIssue{{Code: code, Severity: SeverityError, Fidelity: "rejected", Protocol: route.Target, Direction: EncodeRequest, Stage: "conversion.request", Path: path, Reason: reason, RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID]}}))
}
