package protocol

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type continuationSelection struct{ token, source, remove string }
type continuationInput struct {
	compiled   *Compiled
	input      Value
	selections []continuationSelection
	removals   map[string]bool
}

// ExtractContinuations selects reserved carrier locations without editing the
// input. The returned context keeps original provenance through request decoding.
func (compiled *Compiled) ExtractContinuations(ctx context.Context, wire Value, options EvaluationContext) (EvaluationContext, []string, error) {
	if compiled.packagePlan == nil || compiled.packagePlan.continuation == nil || compiled.packagePlan.continuation.extract == nil {
		return options, nil, nil
	}
	ctx = withDocumentMetrics(ctx, wire.Len())
	options = options.withInputView(wire)
	view := options.inputView
	if !view.continuationChecked || view.continuationCompiled != compiled {
		if err := view.checkResources(ctx, wire, compiled.RequestLimits(ctx), "continuation_input"); err != nil {
			return options, nil, resourceIssue(err, compiled.identity, DecodeRequest, "decode.request")
		}
		if err := compiled.validateWireInputView(ctx, DecodeRequest, view, compiled.RequestLimits(ctx), compiled.wireContracts[DecodeRequest]); err != nil {
			return options, nil, err
		}
		plan := compiled.packagePlan.continuation.extract
		state := evaluation{ctx: ctx, input: wire, root: wire, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), budget: &evaluationBudget{limits: compiled.RequestLimits(ctx), stepLimit: compiled.limits.Nodes}, capture: &nativeCaptureState{source: Provenance{Protocol: compiled.identity, Direction: DecodeRequest, Scope: options.Scope}}}
		output, err := plan.build(state)
		if err != nil {
			return options, nil, compiled.mappingError(DecodeRequest, err)
		}
		if err := checkPlannedOutputLimits(output, compiled.RequestLimits(ctx), "continuation_selection"); err != nil {
			return options, nil, err
		}
		selection, err := compiled.readContinuationSelections(wire, output.output, &view.borrowed)
		if err != nil {
			return options, nil, compiled.mappingError(DecodeRequest, err)
		}
		view.continuation, view.continuationChecked, view.continuationCompiled = selection, true, compiled
	}
	options.continuationInput = view.continuation
	if view.continuation == nil {
		return options, nil, nil
	}
	tokens := make([]string, len(view.continuation.selections))
	for i, selected := range view.continuation.selections {
		tokens[i] = selected.token
	}
	return options, tokens, nil
}

func (compiled *Compiled) readContinuationSelections(wire Value, output JSONOutput, reader *valueReader) (*continuationInput, error) {
	items, err := plannedArrayItems(output)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > compiled.limits.StateItems {
		return nil, resourceFailure("continuation_selection", "items", compiled.limits.StateItems, len(items))
	}
	selection := &continuationInput{compiled: compiled, input: wire, removals: map[string]bool{}}
	seen := map[string]bool{}
	bytes := 0
	for _, item := range items {
		fields, err := plannedObjectFields(item)
		if err != nil {
			return nil, err
		}
		var token Value
		var source, remove Provenance
		for _, field := range fields {
			switch field.key {
			case "token":
				if err := assignCoreValue(field.value, &token, reader); err != nil {
					return nil, err
				}
			case "source", "remove":
				if field.value.node == nil || field.value.node.origin != capturedProvenanceOrigin {
					return nil, fmt.Errorf("continuation selection requires original wire provenance")
				}
				target := &source
				if field.key == "remove" {
					target = &remove
				}
				if err := writeCoreProvenance(field.value, target, reader); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unknown continuation selection field %q", field.key)
			}
		}
		if source.Protocol != compiled.identity || remove.Protocol != compiled.identity || remove.Path == "" || source.Path != remove.Path && !strings.HasPrefix(source.Path, remove.Path+"/") {
			return nil, fmt.Errorf("continuation removal must contain its proven token location")
		}
		original, err := readValuePointer(wire, mustContinuationPointer(source.Path))
		if err != nil || !equalValues(original, token) {
			return nil, fmt.Errorf("continuation token changed from its original location")
		}
		text, err := readString(token)
		if err != nil || !strings.HasPrefix(text, ContinuationPrefix) || seen[source.Path] {
			return nil, fmt.Errorf("invalid or repeated reserved continuation token")
		}
		seen[source.Path] = true
		bytes += len(text)
		if bytes > compiled.limits.BufferBytes {
			return nil, resourceFailure("continuation_selection", "bytes", compiled.limits.BufferBytes, bytes)
		}
		selection.selections = append(selection.selections, continuationSelection{strings.Clone(text), source.Path, remove.Path})
		selection.removals[remove.Path] = true
	}
	return selection, nil
}

func mustContinuationPointer(path string) []string {
	pointer, err := parsePointer(path)
	if err != nil {
		panic("captured continuation path is invalid")
	}
	return pointer
}

func (plan *compiledOutputPlan) isCarrier(state evaluation) (plannedOutput, error) {
	location := nativeReadLocation(plan.source.expression, state.capture)
	if !location.isWire {
		return plannedOutput{}, fmt.Errorf("carrier selection requires an original wire location")
	}
	selection := state.runtime.continuationInput
	return plannedLeaf(coreBoolean(selection != nil && selection.removals[location.path])), nil
}

// AcceptContinuations completes the host's authentication boundary after every
// selected token has passed ownership, scope and history checks.
func (request *Request) AcceptContinuations(tokens []string) error {
	input := request.continuationInput
	if input == nil {
		if len(tokens) == 0 {
			return nil
		}
		return operationMetadataError("/continuation", "request has no selected continuation tokens")
	}
	if len(tokens) != len(input.selections) {
		return operationMetadataError("/continuation", "authentication did not cover every selected token")
	}
	for i, token := range tokens {
		if token != input.selections[i].token {
			return operationMetadataError("/continuation", "authentication tokens differ from the decoded request")
		}
	}
	request.continuationsAccepted = true
	return nil
}

func (input *continuationInput) strip(ctx context.Context, value Value, path string, limits Limits) (Value, error) {
	if input == nil {
		return value, nil
	}
	paths := []string{}
	for remove := range input.removals {
		if path == "" {
			paths = append(paths, remove)
		} else if strings.HasPrefix(remove, path+"/") {
			paths = append(paths, strings.TrimPrefix(remove, path))
		}
	}
	if len(paths) == 0 {
		return value, nil
	}
	sort.Slice(paths, func(i, j int) bool {
		left, right := mustContinuationPointer(paths[i]), mustContinuationPointer(paths[j])
		for k := 0; k < min(len(left), len(right)); k++ {
			if left[k] == right[k] {
				continue
			}
			a, ea := strconv.Atoi(left[k])
			b, eb := strconv.Atoi(right[k])
			if ea == nil && eb == nil {
				return a > b
			}
			return left[k] > right[k]
		}
		return len(left) > len(right)
	})
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	mutations := make([]Mutation, len(paths))
	for i, path := range paths {
		mutations[i] = Mutation{Op: DeleteValue, Path: path}
	}
	return ApplyMutations(value, mutations, limits)
}

func (request *Request) inheritContinuations(source *Request) {
	request.continuationInput, request.continuationsAccepted = source.continuationInput, source.continuationsAccepted
}

func (input *continuationInput) containsRemoval(path string) bool {
	for remove := range input.removals {
		if path == "" || remove == path || strings.HasPrefix(remove, path+"/") {
			return true
		}
	}
	return false
}
