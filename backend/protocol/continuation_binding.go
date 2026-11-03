package protocol

import (
	"context"
	"fmt"
)

// SealedContinuation is authenticated host output. Private facts cannot be
// supplied by a protocol program or reconstructed from ordinary JSON.
type SealedContinuation struct {
	token, id string
	source    Identity
	scope     Scope
	keys      []string
	bytes     int
	persisted bool
}

// SealState seals the existing record format and retains only bounded proof metadata.
func (codec *ContinuationCodec) SealState(record ContinuationRecord, limit int) (*SealedContinuation, error) {
	token, err := codec.Seal(record, limit)
	if err != nil {
		return nil, err
	}
	state := &SealedContinuation{token: token, id: record.ID, source: record.Protocol, scope: record.Scope, bytes: len(token) + len(record.ID)}
	keys := make(map[string]bool, len(record.Node.Resources))
	for _, resource := range record.Node.Resources {
		if IsContinuationResource(resource) {
			key := record.Digest + ":" + ContinuationResourceKey(resource)
			if !keys[key] {
				keys[key] = true
				state.keys = append(state.keys, key)
				state.bytes += len(key)
			}
		}
	}
	return state, nil
}

// Token returns the authenticated envelope for the existing persistence interface.
func (state *SealedContinuation) Token() string { return state.token }

// MarkPersisted records the host's successful persistence write for this sealed entry.
func (state *SealedContinuation) MarkPersisted() { state.persisted = true }

// ContinuationBinding owns one response's carrier delivery and recovery facts.
// It shares immutable tokens; it does not retain the source response or history.
type ContinuationBinding struct {
	compiled                              *Compiled
	direction                             Direction
	source                                Identity
	scope                                 Scope
	plan                                  *compiledContinuationOutput
	entries                               []*SealedContinuation
	resources                             map[string]*SealedContinuation
	bytes, committed, batchEnd, confirmed int
	isRendering                           bool
	deliveryAborted                       bool
	emitted                               map[string]bool
	tokenView                             plannedOutput
}

// BindContinuation pins one target plan before resources are captured. A target
// without a carrier can still retain persistence evidence, never client delivery.
func (compiled *Compiled) BindContinuation(direction Direction, source Identity, scope Scope, allowClient bool) (*ContinuationBinding, error) {
	if compiled.packagePlan == nil || direction != EncodeResponse && direction != EncodeEvent {
		return nil, operationMetadataError("/continuation", "continuation binding requires a package output direction")
	}
	plan := compiled.continuationOutput(direction)
	if !allowClient {
		plan = nil
	}
	return &ContinuationBinding{compiled: compiled, direction: direction, source: source, scope: scope, plan: plan}, nil
}

// Append adds a sealed entry only to its original source protocol and scope.
func (binding *ContinuationBinding) Append(state *SealedContinuation) error {
	if binding.deliveryAborted {
		return operationMetadataError("/continuation", "aborted carrier output cannot accept another entry")
	}
	if state == nil || state.token == "" || state.source != binding.source || state.scope != binding.scope {
		return operationMetadataError("/continuation", "sealed state belongs to a different source or scope")
	}
	if binding.isRendering {
		return operationMetadataError("/continuation", "cannot append while a carrier batch awaits delivery")
	}
	limits := binding.compiled.limits
	if len(binding.entries) >= limits.StateItems {
		return resourceFailure("continuation", "items", limits.StateItems, len(binding.entries)+1)
	}
	if binding.bytes+state.bytes > limits.BufferBytes {
		return resourceFailure("continuation", "bytes", limits.BufferBytes, binding.bytes+state.bytes)
	}
	if binding.resources == nil {
		binding.resources = map[string]*SealedContinuation{}
	}
	for _, key := range state.keys {
		binding.resources[key] = state
	}
	binding.entries = append(binding.entries, state)
	binding.bytes += state.bytes
	return nil
}

// WithContinuation binds host-created recovery facts and the caller's fixed requirement.
func (route ConversionContext) WithContinuation(binding *ContinuationBinding, requireClient bool) ConversionContext {
	route.continuation, route.requireContinuation = binding, requireClient
	return route
}

// WithContinuation supplies only sealed carrier values to the selected output mapping.
func (options EvaluationContext) WithContinuation(binding *ContinuationBinding) EvaluationContext {
	options.continuation = binding
	return options
}

// HasPending reports client-planned tokens not yet confirmed as delivered.
func (binding *ContinuationBinding) HasPending() bool {
	return binding != nil && binding.plan != nil && binding.committed < len(binding.entries)
}

// Finish rejects a successful termination that never delivered its planned carriers.
func (binding *ContinuationBinding) Finish() error {
	if binding != nil && binding.deliveryAborted {
		return operationMetadataError("/continuation", "aborted carrier output cannot finish successfully")
	}
	if binding.HasPending() {
		return operationMetadataError("/continuation", "stream ended before its continuation carriers were delivered")
	}
	return nil
}

// CommitDelivery consumes one confirmed batch only after the host's writes and flush succeed.
func (binding *ContinuationBinding) CommitDelivery() error {
	if binding != nil && binding.deliveryAborted {
		return operationMetadataError("/continuation", "aborted carrier output cannot commit delivery")
	}
	if binding == nil || !binding.isRendering {
		return nil
	}
	if binding.confirmed != binding.batchEnd-binding.committed {
		return operationMetadataError("/continuation", "carrier output did not confirm every planned token")
	}
	binding.committed = binding.batchEnd
	binding.confirmed = 0
	binding.isRendering = false
	binding.emitted = nil
	binding.tokenView = plannedOutput{}
	return nil
}

// AbortDelivery revokes only the pending client delivery, preserving prior
// delivery and persistence facts. The failed response cannot render again.
func (binding *ContinuationBinding) AbortDelivery() {
	binding.deliveryAborted = true
	binding.isRendering = false
	binding.confirmed = 0
	binding.emitted = nil
	binding.tokenView = plannedOutput{}
}

// Release drops owned tokens and indexes after the response settles. Sealed
// persistence receipts and the committed delivery count remain unchanged.
func (binding *ContinuationBinding) Release() {
	binding.AbortDelivery()
	binding.entries, binding.resources = nil, nil
	binding.bytes = 0
}

func (binding *ContinuationBinding) check(compiled *Compiled, direction Direction, scope Scope) error {
	if binding.deliveryAborted {
		return operationMetadataError("/continuation", "aborted carrier output cannot resume")
	}
	if binding.compiled != compiled || binding.direction != direction || binding.scope != scope {
		return operationMetadataError("/continuation", "carrier plan belongs to another revision, direction or scope")
	}
	return nil
}

func (binding *ContinuationBinding) startBatch() error {
	batchEnd := len(binding.entries)
	items := make([]JSONOutput, 0, batchEnd-binding.committed)
	for _, state := range binding.entries[binding.committed:batchEnd] {
		items = append(items, JSONObject(map[string]JSONOutput{"id": JSONLeaf(StringValue(state.id)), "token": JSONLeaf(StringValue(state.token))}))
	}
	output := JSONOutput{node: &jsonOutputNode{items: items}}
	view := plannedOutput{output, normalizedOutputBytes(output)}
	if retained := binding.bytes + view.bytes; retained > binding.compiled.limits.BufferBytes {
		return resourceFailure("continuation", "bytes", binding.compiled.limits.BufferBytes, retained)
	}
	binding.batchEnd, binding.confirmed = batchEnd, 0
	binding.emitted = make(map[string]bool, batchEnd-binding.committed)
	for _, state := range binding.entries[binding.committed:batchEnd] {
		binding.emitted[state.token] = false
	}
	binding.tokenView = view
	binding.isRendering = true
	return nil
}

func (binding *ContinuationBinding) tokenOutput() plannedOutput { return binding.tokenView }

func (compiled *Compiled) encodeContinuation(ctx context.Context, wire Value, options EvaluationContext, direction Direction) (plannedOutput, bool, error) {
	binding := options.continuation
	if binding == nil {
		return plannedOutput{}, false, nil
	}
	if err := binding.check(compiled, direction, options.Scope); err != nil {
		return plannedOutput{}, false, err
	}
	if !binding.HasPending() {
		return plannedOutput{}, false, nil
	}
	if binding.isRendering {
		return plannedOutput{}, false, operationMetadataError("/continuation", "previous carrier batch has not completed delivery")
	}
	view := newInputView(wire)
	state := evaluation{ctx: ctx, input: wire, root: wire, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), runtime: bindEvaluationRuntime(compiled, direction, options), budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	if binding.plan.when != nil {
		value, err := binding.plan.when.evaluate(state)
		if err != nil {
			return plannedOutput{}, false, compiled.mappingError(direction, err)
		}
		matches, err := readBoolean(value)
		if err != nil {
			return plannedOutput{}, false, err
		}
		if !matches {
			return plannedOutput{}, false, nil
		}
	}
	if err := binding.startBatch(); err != nil {
		return plannedOutput{}, false, compiled.mappingError(direction, err)
	}
	output, err := binding.plan.program.build(state)
	if err == nil {
		err = checkPlannedOutputLimits(output, compiled.limits, "continuation_output")
	}
	if err != nil {
		return plannedOutput{}, false, compiled.mappingError(direction, err)
	}
	return output, true, nil
}

// EncodeContinuationResponse inserts sealed values without repeating final wire validation.
func (compiled *Compiled) EncodeContinuationResponse(ctx context.Context, wire Value, options EvaluationContext) (Value, error) {
	output, changed, err := compiled.encodeContinuation(ctx, wire, options, EncodeResponse)
	if err != nil || !changed {
		return wire, err
	}
	return output.output.Encode(ctx)
}

// EncodeContinuationFrame returns nil when this frame is not a carrier boundary.
// The caller retains the original frame and its established delivery schedule.
func (compiled *Compiled) EncodeContinuationFrame(ctx context.Context, wire Value, options EvaluationContext) ([]Value, error) {
	output, changed, err := compiled.encodeContinuation(ctx, wire, options, EncodeEvent)
	if err != nil || !changed {
		return nil, err
	}
	return encodeContinuationFrames(ctx, output)
}

// EncodeContinuationTransformedFrames pairs an expanded carrier batch with the
// same read-only carrier program applied before final wire rules. Pairing does
// not assume that the original frame is last. The common unchanged path shares
// the resulting frames and evaluates the carrier program only once.
func (compiled *Compiled) EncodeContinuationTransformedFrames(ctx context.Context, before, after Value, options EvaluationContext) ([]Value, []Value, error) {
	output, changed, err := compiled.encodeContinuation(ctx, after, options, EncodeEvent)
	if err != nil || !changed {
		return nil, nil, err
	}
	frames, err := encodeContinuationFrames(ctx, output)
	if err != nil {
		return nil, nil, err
	}
	if before.raw == after.raw {
		return frames, frames, nil
	}
	binding := options.continuation
	view := newInputView(before)
	state := evaluation{ctx: ctx, input: before, root: before, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), runtime: bindEvaluationRuntime(compiled, EncodeEvent, options), budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	if binding.plan.when != nil {
		selected, err := binding.plan.when.evaluate(state)
		if err != nil {
			return nil, nil, err
		}
		matches, err := readBoolean(selected)
		if err != nil {
			return nil, nil, err
		}
		if !matches {
			return nil, nil, operationMetadataError("/continuation", "wire edit changed the carrier boundary")
		}
	}
	original, err := binding.plan.program.build(state)
	if err != nil {
		return nil, nil, err
	}
	if err := checkPlannedOutputLimits(original, compiled.limits, "continuation_output"); err != nil {
		return nil, nil, err
	}
	beforeFrames, err := encodeContinuationFrames(ctx, original)
	if err != nil {
		return nil, nil, err
	}
	if len(beforeFrames) != len(frames) {
		return nil, nil, operationMetadataError("/continuation", "wire edit changed carrier batch cardinality")
	}
	return beforeFrames, frames, nil
}

func encodeContinuationFrames(ctx context.Context, output plannedOutput) ([]Value, error) {
	items, err := plannedArrayItems(output.output)
	if err != nil {
		return nil, err
	}
	frames := make([]Value, len(items))
	for i, item := range items {
		frames[i], err = item.Encode(ctx)
		if err != nil {
			return nil, err
		}
	}
	return frames, nil
}

func (binding *ContinuationBinding) observe(ctx context.Context, view *inputView) error {
	if binding == nil || !binding.isRendering {
		return nil
	}
	state := evaluation{ctx: ctx, input: view.value, root: view.value, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), budget: &evaluationBudget{limits: binding.compiled.limits, stepLimit: binding.compiled.limits.Nodes}}
	output, err := binding.plan.emitted.build(state)
	if err != nil {
		return binding.compiled.mappingError(binding.direction, err)
	}
	items, err := plannedArrayItems(output.output)
	if err != nil {
		return err
	}
	for _, item := range items {
		token, err := readPlannedString(item)
		if err != nil {
			return err
		}
		seen, exists := binding.emitted[token]
		if !exists || seen {
			return operationMetadataError("/continuation", "output confirmed an unknown or repeated carrier")
		}
		binding.emitted[token] = true
		binding.confirmed++
	}
	return nil
}

// ValidateContinuationResponse shares final shape validation and carrier observation.
func (compiled *Compiled) ValidateContinuationResponse(ctx context.Context, wire Value, options EvaluationContext) error {
	options = options.withInputView(wire)
	view := options.inputView
	if err := view.checkResources(ctx, wire, compiled.limits, "continuation_output"); err != nil {
		return resourceIssue(err, compiled.identity, EncodeResponse, "encode.response")
	}
	if err := compiled.validateWireInputView(ctx, EncodeResponse, view, compiled.limits, compiled.selectFinalWireContract(EncodeResponse)); err != nil {
		return err
	}
	return compiled.validateContinuationResponseView(ctx, view, options)
}

func (compiled *Compiled) validateContinuationResponseView(ctx context.Context, view *inputView, options EvaluationContext) error {
	binding := options.continuation
	if binding == nil {
		return nil
	}
	if err := binding.check(compiled, EncodeResponse, options.Scope); err != nil {
		return err
	}
	if err := binding.observe(ctx, view); err != nil {
		return err
	}
	if binding.HasPending() && (!binding.isRendering || binding.confirmed != binding.batchEnd-binding.committed) {
		return operationMetadataError("/continuation", "response omitted planned continuation carriers")
	}
	return nil
}

// ConsumeContinuation validates one actual output frame and observes carrier
// values in the same view; delivery advances only through CommitDelivery.
func (validation *WireStreamValidation) ConsumeContinuation(ctx context.Context, frame Value, options EvaluationContext) error {
	if validation.packageStream == nil {
		return fmt.Errorf("continuation output requires a package stream")
	}
	return validation.ConsumeTransformed(ctx, frame, frame, options)
}
