package protocol

import (
	"fmt"
	"maps"
	"reflect"
	"strconv"
)

type controlCompileScope struct {
	expression                          expressionScope
	whole                               reflect.Type
	canSelect, canUseAuxiliary, canOmit bool
	isToolResult                        bool
	isError                             bool
}

type compiledControlEach struct {
	selection string
	pathSlot  int
	steps     []compiledControlStep
}

func (scope controlCompileScope) compileSetter(root reflect.Type, path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	if scope.isToolResult {
		return compileToolResultControlSetter(path)
	}
	if scope.isError {
		return compileErrorControlSetter(path)
	}
	return compileTypedControlSetter(root, path)
}

func validateControlStepForm(step ProtocolControlStep, scope controlCompileScope, path string) error {
	if step.Each != nil && (!scope.canSelect || step.Auxiliary != nil || step.Omit || len(step.Set) != 0 || step.Diagnostic != nil) {
		return fmt.Errorf("%s/each: selection requires a complete core document and its own local steps", path)
	}
	if step.Auxiliary != nil {
		if !scope.canUseAuxiliary || step.Omit || len(step.Set) != 0 || step.Diagnostic != nil {
			return fmt.Errorf("%s/auxiliary: requires a standalone event disposition", path)
		}
		if step.Auxiliary.Disposition != "omit" || step.Auxiliary.Reason == "" {
			return fmt.Errorf("%s/auxiliary: requires an explicit omission reason", path)
		}
		if _, err := parsePointer(step.Auxiliary.Path); err != nil {
			return locateMappingError(path+"/auxiliary/path", err)
		}
	}
	if step.Omit && (!scope.canOmit || len(step.Set) != 0 || step.Diagnostic == nil || step.Diagnostic.Kind != "normalize" && step.Diagnostic.Kind != "degrade") {
		return fmt.Errorf("%s/omit: only a selected metadata entry with an explicit normalization or loss diagnostic may be omitted", path)
	}
	return nil
}

func compileControlEach(declaration ProtocolControlEach, compiler *expressionCompiler, path string, outer controlCompileScope, count *int) (*compiledControlEach, error) {
	if len(declaration.Steps) == 0 {
		return nil, fmt.Errorf("%s/steps: requires local control steps", path)
	}
	var local reflect.Type
	switch declaration.Select {
	case "metadata":
		local = reflect.TypeFor[ResponseMetadata]()
	case "node", "tool_result":
		local = reflect.TypeFor[Node]()
	case "tool":
		if outer.whole != reflect.TypeFor[Request]() {
			return nil, fmt.Errorf("%s/select: only requests declare tools", path)
		}
		local = reflect.TypeFor[Tool]()
	case "usage":
		if outer.whole == reflect.TypeFor[Request]() {
			return nil, fmt.Errorf("%s/select: requests have no usage owner", path)
		}
		local = reflect.TypeFor[Usage]()
	case "error":
		if outer.whole == reflect.TypeFor[Request]() {
			return nil, fmt.Errorf("%s/select: requests have no error owner", path)
		}
		local = reflect.TypeFor[Object]()
	default:
		return nil, fmt.Errorf("%s/select: expected node, tool, tool_result, metadata, usage or error", path)
	}
	child := controlCompileScope{whole: outer.whole, expression: outer.expression, canOmit: declaration.Select == "metadata"}
	child.isToolResult = declaration.Select == "tool_result"
	child.isError = declaration.Select == "error"
	child.expression.coreRoot, child.expression.coreInput, child.expression.hasInputScope = outer.whole, local, true
	compiled := &compiledControlEach{selection: declaration.Select, pathSlot: -1}
	if declaration.PathBinding != "" {
		if !definitionIdentifier.MatchString(declaration.PathBinding) {
			return nil, fmt.Errorf("%s/pathBinding: expected a binding identifier", path)
		}
		child.expression.bindings = maps.Clone(outer.expression.bindings)
		if child.expression.bindings == nil {
			child.expression.bindings = make(map[string]expressionBinding)
		}
		compiled.pathSlot = child.expression.bindingDepth
		child.expression.bindingDepth++
		child.expression.bindings[declaration.PathBinding] = expressionBinding{slot: compiled.pathSlot, result: StringType}
	}
	var err error
	compiled.steps, err = compileScopedControlSteps(declaration.Steps, compiler, path+"/steps", false, child, count)
	return compiled, err
}

func (each *compiledControlEach) apply(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	if each.selection == "error" {
		return each.applyErrors(state, conversion, document, route, rule, sink, owner)
	}
	if each.selection == "usage" {
		return each.applyUsage(state, conversion, document, route, rule, sink, owner)
	}
	if each.selection == "node" || each.selection == "tool_result" {
		return each.applyNodes(state, conversion, document, route, rule, sink, owner)
	}
	if each.selection == "tool" {
		return each.applyTools(state, conversion, document.(*Request), route, rule, sink, owner)
	}
	changed := false
	visit := func(items *[]ResponseMetadata, path string) error {
		input := *items
		kept := input[:0]
		for index := range input {
			if err := state.advance(); err != nil {
				return err
			}
			metadata := &input[index]
			if err := validateCoreMetadataValue(owner.CoreVersion, *metadata); err != nil {
				return err
			}
			local := each.local(state, path+"/"+strconv.Itoa(index), func(pointer []string) (JSONOutput, error) { return readCoreResponseMetadata(*metadata, pointer) })
			result, err := applyControlSteps(local, conversion, metadata, route, rule, sink, each.steps, owner)
			if err != nil {
				return err
			}
			changed = changed || result.hasChanges || result.shouldOmit
			if !result.shouldOmit {
				kept = append(kept, *metadata)
			}
		}
		clear(input[len(kept):])
		*items = kept
		return nil
	}
	err := visitControlMetadata(state, document, visit)
	return changed, err
}

func (each *compiledControlEach) local(state evaluation, path string, reader func([]string) (JSONOutput, error)) evaluation {
	state.input, state.inputReader = Value{}, reader
	state.objectReader = nil
	if each.pathSlot >= 0 {
		state.bindings[each.pathSlot] = JSONLeaf(StringValue(path))
	}
	return state
}

func (each *compiledControlEach) applyUsage(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	changed := false
	visit := func(usage *Usage, path string) error {
		if usage == nil {
			return nil
		}
		if err := state.advance(); err != nil {
			return err
		}
		check := capabilityCheck{target: Target{Protocol: route.Source, Direction: controlDirection(rule.Phase, true), Capabilities: CapabilitySet{UsageCapability: true}}, limits: state.budget.limits}
		check.usage(usage, path)
		if err := IssuesError(check.issues); err != nil {
			return err
		}
		local := each.local(state, path, func(pointer []string) (JSONOutput, error) { return readCoreUsage(*usage, pointer) })
		result, err := applyControlSteps(local, conversion, usage, route, rule, sink, each.steps, owner)
		changed = changed || result.hasChanges
		return err
	}
	var err error
	switch current := document.(type) {
	case *Response:
		err = visit(current.Usage, "/usage")
	case *Event:
		err = visit(current.Usage, "/usage")
		if err == nil && current.Response != nil {
			err = visit(current.Response.Usage, "/response/usage")
		}
	default:
		panic("compiled usage selector received a different core type")
	}
	return changed, err
}

func visitControlMetadata(state evaluation, document any, visit func(*[]ResponseMetadata, string) error) error {
	nodeMetadata := func(node *Node, path string) error {
		return visit(&node.Metadata, path+"/metadata")
	}
	response := func(value *Response, path string) error {
		if err := visit(&value.Metadata, path+"/metadata"); err != nil {
			return err
		}
		return visitControlNodeSlice(state, value.Content, path+"/content", nodeMetadata)
	}
	switch current := document.(type) {
	case *Request:
		return visitControlNodeSlice(state, current.Content, "/content", nodeMetadata)
	case *Response:
		return response(current, "")
	case *Event:
		if err := visit(&current.Metadata, "/metadata"); err != nil {
			return err
		}
		if current.Item != nil {
			if err := visitControlNode(state, current.Item, "/item", nodeMetadata); err != nil {
				return err
			}
		}
		if current.Response != nil {
			if err := response(current.Response, "/response"); err != nil {
				return err
			}
		}
		if current.Request != nil {
			return visitControlNodeSlice(state, current.Request.Content, "/request/content", nodeMetadata)
		}
		return nil
	}
	panic("compiled metadata selector received a different core type")
}
