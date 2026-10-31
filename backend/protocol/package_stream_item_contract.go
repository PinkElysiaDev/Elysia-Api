package protocol

import (
	"fmt"
	"slices"
)

func hasStreamItemSelectors(spec ProtocolStreamActionSpec) bool {
	return spec.ParentID != "" || spec.ParentIDValue != nil || spec.Ordinal != "" || spec.OrdinalValue != nil ||
		spec.WireID != "" || spec.Role != "" || spec.Status != "" || spec.Properties != nil || spec.PropertiesPath != "" ||
		spec.ChildrenCount != nil || spec.ChildrenPath != "" || spec.SealText || spec.VerifyCompleted || len(spec.RequireItems) != 0 || spec.Diagnostic != nil
}

func (compiler *packageStreamCompiler) compilePredicate(value *Expression, path string) (*compiledOutputPlan, error) {
	if value == nil {
		return nil, nil
	}
	field := streamField{}
	if err := compiler.compileValue(value, path, &field); err != nil {
		return nil, err
	}
	if !isAssignable(field.value.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s: condition must produce a boolean", path)
	}
	return field.value, nil
}

func (compiler *packageStreamCompiler) bindIteration(spec ProtocolStreamActionSpec, event *compiledPackageStreamEvent, path string) error {
	scope := compiler.scope
	scope.bindings = make(map[string]expressionBinding, len(compiler.scope.bindings)+2)
	for name, binding := range compiler.scope.bindings {
		scope.bindings[name] = binding
	}
	for _, binding := range []struct {
		name string
		kind JSONType
		slot *int
		set  *bool
	}{{spec.As, AnyType, &event.itemBinding, &event.canBindItem}, {spec.IndexAs, IntegerType, &event.indexBinding, &event.canBindIndex}} {
		if binding.name == "" {
			continue
		}
		if !definitionIdentifier.MatchString(binding.name) || spec.As != "" && spec.As == spec.IndexAs {
			return fmt.Errorf("%s: iteration bindings require distinct identifiers", path)
		}
		*binding.slot, *binding.set = scope.bindingDepth, true
		scope.bindings[binding.name] = expressionBinding{slot: scope.bindingDepth, result: binding.kind}
		scope.bindingDepth++
	}
	scope.isIteration = true
	compiler.scope = scope
	compiler.contract.bindingSlots = max(compiler.contract.bindingSlots, scope.bindingDepth)
	return nil
}

func (compiler *packageStreamCompiler) compileItemSelectors(spec ProtocolStreamActionSpec, event *compiledPackageStreamEvent, path string) error {
	if (spec.Properties == nil) != (spec.PropertiesPath == "") || (spec.ChildrenCount == nil) != (spec.ChildrenPath == "") {
		return fmt.Errorf("%s: property and child-count expressions require their diagnostic pointers", path)
	}
	isItem := slices.Contains([]EventType{ItemStarted, ItemDelta, ItemSnapshot, ItemFinished}, event.action)
	if !isItem && (spec.ParentID != "" || spec.ParentIDValue != nil || spec.Ordinal != "" || spec.OrdinalValue != nil || spec.WireID != "" || spec.Role != "" || spec.Status != "" || spec.Properties != nil || spec.ChildrenCount != nil) {
		return fmt.Errorf("%s: item selectors require an item lifecycle action", path)
	}
	if spec.SealText && (spec.Action != "text_snapshot" || spec.Text == "") || spec.VerifyCompleted && (spec.Action != "item_finish" || event.kind != MessageNode) {
		return fmt.Errorf("%s: sealText requires text_snapshot; verifyCompleted requires a container finish", path)
	}
	if event.kind != MessageNode && (spec.Action == "item_verify" || spec.ParentID != "" && spec.Action == "item_finish") && spec.Text == "" {
		return fmt.Errorf("%s/text: completed content requires its complete text selector", path)
	}
	if spec.Implicit && (event.kind == MessageNode || spec.ParentID != "") {
		return fmt.Errorf("%s/implicit: containers and ordered children require explicit starts", path)
	}
	if (spec.Ordinal != "" || spec.OrdinalValue != nil) && spec.ParentID == "" || spec.ParentIDValue != nil && spec.ParentID == "" || spec.OrdinalValue != nil && spec.Ordinal == "" {
		return fmt.Errorf("%s: ordinal requires a parent, and value selectors require their diagnostic pointer", path)
	}
	if spec.ChildrenCount != nil && event.kind != MessageNode || spec.Properties != nil && event.action == ItemDelta || spec.WireID != "" && event.kind != MessageNode || spec.Role != "" && event.kind != MessageNode || spec.Status != "" && event.kind != MessageNode {
		return fmt.Errorf("%s: container identity and children require message items; properties require a snapshot", path)
	}
	for _, selector := range []struct {
		name, pointer string
		value         *Expression
		field         *streamField
	}{
		{"parentId", spec.ParentID, spec.ParentIDValue, &event.parentID},
		{"ordinal", spec.Ordinal, spec.OrdinalValue, &event.ordinal},
		{"wireId", spec.WireID, nil, &event.wireID}, {"role", spec.Role, nil, &event.role}, {"status", spec.Status, nil, &event.status},
		{"properties", spec.PropertiesPath, spec.Properties, &event.properties}, {"childrenCount", spec.ChildrenPath, spec.ChildrenCount, &event.childrenCount},
	} {
		if selector.pointer == "" && selector.value == nil {
			continue
		}
		field, err := compileStreamField(selector.pointer, path+"/"+selector.name, compiler.limits)
		if err != nil {
			return err
		}
		if err := compiler.compileValue(selector.value, path+"/"+selector.name+"Value", &field); err != nil {
			return err
		}
		*selector.field = field
	}
	if len(spec.RequireItems) != 0 && event.action != ResponseFinished {
		return fmt.Errorf("%s/requireItems: only a response finish can require a complete item snapshot", path)
	}
	for _, kind := range spec.RequireItems {
		if kind != MessageNode || !slices.Contains(compiler.contract.itemKinds, kind) || slices.Contains(event.requiredItems, kind) {
			return fmt.Errorf("%s/requireItems: requires distinct declared container kinds", path)
		}
		event.requiredItems = append(event.requiredItems, kind)
	}
	if spec.Diagnostic != nil {
		if err := validateBoundaryDiagnostic(*spec.Diagnostic, path+"/diagnostic"); err != nil {
			return err
		}
		copy := *spec.Diagnostic
		event.diagnostic = &copy
	}
	event.sealsText, event.verifiesCompleted = spec.SealText, spec.VerifyCompleted
	return nil
}
