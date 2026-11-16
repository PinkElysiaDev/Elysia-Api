package protocol

import (
	"fmt"
	"maps"
	"reflect"
)

// ProtocolTextLayout declares how selected text leaves share one payload.
// Metadata programs receive the original metadata and the text offset bindings;
// they can change its value and references, but cannot manufacture provenance.
type ProtocolTextLayout struct {
	ShouldCross   bool                            `json:"across,omitempty"`
	Measure       Expression                      `json:"measure"`
	MessageWhen   *Expression                     `json:"messageWhen,omitempty"`
	OffsetBinding string                          `json:"offsetBinding,omitempty"`
	LengthBinding string                          `json:"lengthBinding,omitempty"`
	Metadata      map[string]ProtocolTextMetadata `json:"metadata,omitempty"`
}

// ProtocolTextMetadata projects a metadata payload and its canonical references
// into the merged text. References must be declared when the input carries them.
type ProtocolTextMetadata struct {
	Value      Expression  `json:"value"`
	References *Expression `json:"references,omitempty"`
}

type compiledTextMetadata struct {
	value, references *compiledOutputPlan
}

type compiledTextLayout struct {
	shouldCross            bool
	messageWhen            *compiledOutputPlan
	measure                *compiledOutputPlan
	offsetSlot, lengthSlot int
	metadata               map[string]compiledTextMetadata
}

func compileTextLayout(declaration ProtocolContentLayout, compiler *expressionCompiler, path string, outer controlCompileScope, count *int) (*compiledContentLayout, error) {
	if declaration.Path != "/content/*/children" || declaration.Text == nil || declaration.ChildWhen != nil || declaration.Wrapper != nil || declaration.Role != "" || declaration.MovedBinding != "" || outer.whole != reflect.TypeFor[Request]() && outer.whole != reflect.TypeFor[Response]() {
		return nil, fmt.Errorf("%s: merge_text requires complete-document message children and text options", path)
	}
	if len(declaration.Steps) == 0 || declaration.Steps[0].When != nil || declaration.Steps[0].Diagnostic == nil || declaration.Steps[0].Diagnostic.Kind != "degrade" && declaration.Steps[0].Diagnostic.Kind != "reject" {
		return nil, fmt.Errorf("%s/steps: text merging requires an unconditional boundary-loss diagnostic first", path)
	}
	for _, step := range declaration.Steps {
		if len(step.Set) != 0 {
			return nil, fmt.Errorf("%s/steps: text merging cannot also change node fields", path)
		}
	}
	local := controlCompileScope{whole: outer.whole, expression: outer.expression}
	local.expression.coreRoot, local.expression.coreInput = outer.whole, reflect.TypeFor[Node]()
	local.expression.hasInputScope = true
	text := &compiledTextLayout{shouldCross: declaration.Text.ShouldCross, offsetSlot: -1, lengthSlot: -1, metadata: make(map[string]compiledTextMetadata, len(declaration.Text.Metadata))}
	plan := &compiledContentLayout{operation: declaration.Operation, pathSlot: -1, movedSlot: -1, text: text}
	if err := bindLayoutVariables(&local.expression, path, []layoutBinding{
		{declaration.PathBinding, StringType, &plan.pathSlot}, {declaration.Text.OffsetBinding, IntegerType, &text.offsetSlot}, {declaration.Text.LengthBinding, IntegerType, &text.lengthSlot},
	}); err != nil {
		return nil, err
	}
	selection := local.expression
	selection.bindings = maps.Clone(selection.bindings)
	delete(selection.bindings, declaration.Text.OffsetBinding)
	delete(selection.bindings, declaration.Text.LengthBinding)
	var err error
	plan.when, err = compiler.compilePlan(declaration.When, path+"/when", selection, 1)
	if err != nil {
		return nil, err
	}
	if !isAssignable(plan.when.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s/when: text selection must produce a boolean", path)
	}
	plan.steps, err = compileScopedControlSteps(declaration.Steps, compiler, path+"/steps", false, local, count)
	if err != nil {
		return nil, err
	}
	plan.bindingSlots = max(local.expression.bindingDepth, plan.when.bindingSlots, controlBindingSlots(plan.steps))
	text.measure, err = compiler.compilePlan(declaration.Text.Measure, path+"/text/measure", selection, 1)
	if err != nil {
		return nil, err
	}
	if !isAssignable(text.measure.expression.result, IntegerType) {
		return nil, fmt.Errorf("%s/text/measure: text measurement must produce an integer", path)
	}
	plan.bindingSlots = max(plan.bindingSlots, text.measure.bindingSlots)
	if declaration.Text.MessageWhen != nil {
		text.messageWhen, err = compiler.compilePlan(*declaration.Text.MessageWhen, path+"/text/messageWhen", selection, 1)
		if err != nil {
			return nil, err
		}
		if !isAssignable(text.messageWhen.expression.result, BooleanType) {
			return nil, fmt.Errorf("%s/text/messageWhen: message selection must produce a boolean", path)
		}
		plan.bindingSlots = max(plan.bindingSlots, text.messageWhen.bindingSlots)
	}
	local.expression.coreInput = reflect.TypeFor[ResponseMetadata]()
	for _, name := range sortedKeys(declaration.Text.Metadata) {
		metadata := declaration.Text.Metadata[name]
		metadataPath := path + "/text/metadata/" + escapePointer(name)
		value, err := compiler.compilePlan(metadata.Value, metadataPath+"/value", local.expression, 1)
		if err != nil {
			return nil, err
		}
		program := compiledTextMetadata{value: value}
		plan.bindingSlots = max(plan.bindingSlots, value.bindingSlots)
		if metadata.References != nil {
			program.references, err = compiler.compilePlan(*metadata.References, metadataPath+"/references", local.expression, 1)
			if err != nil {
				return nil, err
			}
			plan.bindingSlots = max(plan.bindingSlots, program.references.bindingSlots)
		}
		text.metadata[name] = program
	}
	return plan, nil
}
