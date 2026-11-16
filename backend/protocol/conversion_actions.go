package protocol

import (
	"fmt"
	"slices"
	"strings"
)

type conversionAction struct {
	parseOption  func(Value) conversionOption
	name         string
	phases       []ConversionPhase
	valueSchema  map[string]any
	description  string
	isWholeValue bool
}

// Build fresh editor schemas so callers cannot mutate compiler constraints.
func conversionActionCatalog() []conversionAction {
	return []conversionAction{
		{name: "source_prelude", phases: []ConversionPhase{ConversionSourceEvent}, isWholeValue: true, description: "Normalize one declared source opening frame against the next frame before source lifecycle validation; strict mode rejects changes."},
		{name: "event_prelude", phases: []ConversionPhase{ConversionEvent}, isWholeValue: true, description: "Generate the pinned target's declared response header once when a source stream omits its opening event."},
		{name: "stream_disposition", phases: []ConversionPhase{ConversionEvent}, isWholeValue: true, description: "Authorize a loss explicitly requested by the pinned target event program, before encoding and transaction commit; strict mode rejects degradation."},
		{name: "continuation_recovery", phases: []ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent}, isWholeValue: true, description: "Project declared protected resources using host-authenticated recovery evidence and the target's explicit disposition."},
		{name: "control_event", phases: []ConversionPhase{ConversionEvent}, isWholeValue: true, description: "Apply a declared target disposition only to explicitly named, source-validated control kinds."},
		{name: "auxiliary_event", phases: []ConversionPhase{ConversionEvent}, isWholeValue: true, description: "Apply the target's declared disposition to a privately validated auxiliary source frame."},
		{name: "core_controls", phases: []ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent}, valueSchema: map[string]any{"type": "object", "required": []string{"coreVersion", "target", "mappingHash", "limits", "phase", "steps"}}, description: "Apply the selected mapping's declared typed field updates and dispositions without materializing the complete core document.", isWholeValue: true},
		{name: "set", phases: valueConversionPhases()},
		{name: "remove", phases: valueConversionPhases()},
		{name: "transform", phases: valueConversionPhases()},
		{name: "model_controls", phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "object", "required": []string{"coreVersion", "target", "policy"}}, description: "Apply declared model controls to typed request fields; retain binding overrides, ordered diagnostics and final constraints after authored rules.", isWholeValue: true},
		{name: "warn", phases: valueConversionPhases()},
		{name: "reject", phases: valueConversionPhases()},
		{name: "request_projection", phases: []ConversionPhase{ConversionRequest}, valueSchema: requestProjectionSchema(), description: "Apply the target Mapping’s declared request-field and cache-intent dispositions.", isWholeValue: true},
		{name: "buffer_node", phases: []ConversionPhase{ConversionEvent}},
		{name: "provider_signature", phases: []ConversionPhase{ConversionRequest}},
	}
}

func conversionPhases() []ConversionPhase {
	return append(valueConversionPhases(), ConversionSourceEvent)
}

func valueConversionPhases() []ConversionPhase {
	return []ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent, ConversionWire}
}

func conversionActionSchemas() (map[string]any, []string) {
	schemas := map[string]any{}
	var names []string
	for _, action := range conversionActionCatalog() {
		names = append(names, action.name)
		schema := map[string]any{"phases": action.phases}
		if action.valueSchema != nil {
			schema["value"] = action.valueSchema
		}
		if action.description != "" {
			schema["description"] = action.description
		}
		schemas[action.name] = schema
	}
	return schemas, names
}

func (action conversionAction) validate(rule ConversionRule) error {
	if !slices.Contains(action.phases, rule.Phase) {
		phases := make([]string, len(action.phases))
		for i, phase := range action.phases {
			phases[i] = string(phase)
		}
		return fmt.Errorf("%s requires %s phase", action.name, strings.Join(phases, " or "))
	}
	if action.isWholeValue && rule.Match.NodeKind != "" {
		return fmt.Errorf("%s matches the complete semantic value, not an individual node", action.name)
	}
	switch action.name {
	case "core_controls":
		return validateCoreControlRule(rule)
	case "request_projection":
		var options requestProjectionOptions
		if err := decodeContract(rule.Value.Bytes(), &options); err != nil || options.Core == nil {
			return fmt.Errorf("request_projection requires a declared core projection")
		}
	case "provider_signature":
		if rule.Match.NodeKind != ToolCallNode || rule.Match.TargetFamily == "" {
			return fmt.Errorf("provider_signature requires a request tool_call rule with an explicit target family")
		}
		_, err := ProviderSignatureValue(rule.Value)
		return err

	}
	return nil
}
