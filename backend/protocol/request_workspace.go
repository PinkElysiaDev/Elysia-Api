package protocol

import (
	"context"
	"maps"
	"reflect"
)

// requestEncodingWorkspace owns one immutable parsed baseline, never a set of
// candidate projections. Pure package plans preserve it; a semantic JSON boundary drops it. It is attached
// privately after successful decoding and is never cached globally.
type requestEncodingWorkspace struct {
	compiled     *Compiled
	identity     Identity
	source       Provenance
	context      NativeRequestContext
	limits       Limits
	original     Value
	baseline     *Request
	finished     *Request // Immutable successful decode; absent when authorization could edit it.
	continuation *continuationInput
}

// requestReplay belongs to one synchronous encode, including its baseline
// encoding. It never attaches reconstructed state to the caller's Native.
type requestReplay struct {
	compiled *Compiled
	options  EvaluationContext
	current  *Request
	prepared preparedRequestReplay
}

type preparedRequestReplay struct {
	compiled *Compiled
	original Value
	baseline *Request
	options  EvaluationContext
}

func newRequestReplay(compiled *Compiled, options EvaluationContext) *requestReplay {
	options.requestReplay = nil
	return &requestReplay{compiled: compiled, options: options}
}

func (replay *requestReplay) canReuseOutput(ctx context.Context) bool {
	prepared := replay.prepared
	plan := replay.compiled.mappings[EncodeRequest].outputPlan
	// Equal semantic input is insufficient for stateful or delivery-observing
	// programs. They retain their independent baseline execution even when the
	// ordinary request document happens to match byte for byte.
	if plan == nil || plan.hasStreamWrites || plan.hasOutputStreamRead || plan.hasEncodingStateRead ||
		replay.options.State != nil || replay.options.packageTransaction != nil || replay.options.outputStream != nil {
		return false
	}
	for _, effect := range replay.compiled.packagePlan.effects[EncodeRequest] {
		if effect.kind == "warn" {
			return false
		} // Preserve baseline warning multiplicity and order.
	}
	if prepared.baseline == nil || replay.current == nil || replay.current.continuationInput != nil || prepared.baseline.continuationInput != nil ||
		replay.current.continuationsAccepted != prepared.baseline.continuationsAccepted ||
		prepared.options.Scope != replay.options.Scope || prepared.options.InterleavedThinking != replay.options.InterleavedThinking ||
		!maps.Equal(prepared.options.Values, replay.options.Values) {
		return false
	}
	leftInput, leftOutput, leftWork := replay.compiled.mappingLimits(ctx, EncodeRequest)
	rightInput, rightOutput, rightWork := prepared.compiled.mappingLimits(ctx, EncodeRequest)
	return leftInput == rightInput && leftOutput == rightOutput && leftWork == rightWork &&
		replay.compiled.wireLimits(ctx, EncodeRequest) == prepared.compiled.wireLimits(ctx, EncodeRequest) &&
		equalRequestSnapshot(replay.current, prepared.baseline)
}

func (replay *requestReplay) load(ctx context.Context, native *Native) (preparedRequestReplay, error) {
	if replay.prepared.baseline != nil {
		return replay.prepared, nil
	}
	prepared, err := replay.compiled.prepareRequestReplay(ctx, native, replay.options)
	if err != nil {
		return preparedRequestReplay{}, err
	}
	replay.prepared = prepared
	return prepared, nil
}

func (compiled *Compiled) prepareRequestReplay(ctx context.Context, native *Native, options EvaluationContext) (preparedRequestReplay, error) {
	// Replay must not repeat routing/authorization. Validate against the selected
	// target before restoring the source decoder's independent context.
	options.ResolveRequestScope = nil
	snapshotLimits := requestLimits(ctx, DefaultLimits(), false)
	original, err := ReplayNative(*native, compiled.target(EncodeRequest, options), nil, snapshotLimits)
	if err != nil {
		return preparedRequestReplay{}, err
	}
	replay := *compiled
	replay.limits, replay.explicitLimits = snapshotLimits, true
	request := compiled.requestReplayBaseline(native, snapshotLimits)
	if source := native.RequestContext; source != nil {
		options.Scope, options.Values, options.InterleavedThinking = source.Scope, maps.Clone(source.Values), source.InterleavedThinking
	}
	if request != nil && native.requestWorkspace.finished != nil &&
		native.requestWorkspace.finished.Native.Source == native.Source &&
		compiled.nativeLimits(ctx, EncodeRequest) == replay.nativeLimits(ctx, EncodeRequest) {
		// The workspace owns this exact successful decode, under identical
		// immutable wire/source/context/limits. No callback or candidate has ever
		// owned it. Reuse it read-only rather than finishing a fresh clone.
		request = native.requestWorkspace.finished
		err = replay.runtimeError(DecodeRequest, ctx.Err())
	} else if request == nil {
		request, err = replay.decodeRequestValue(ctx, original, options, &native.Source.Scope)
	} else {
		decoded := request
		if err = ctx.Err(); err == nil {
			request, err = replay.finishDecodedRequest(ctx, original, cloneRequestSemantic(request), options, &native.Source.Scope)
		}
		if err == nil {
			replay.attachRequestWorkspace(ctx, request, decoded)
		}
		err = replay.runtimeError(DecodeRequest, err)
	}
	if err != nil {
		return preparedRequestReplay{}, err
	}
	selection := request.continuationInput
	if selection != nil {
		if options.continuationInput == nil || options.continuationInput.input != selection.input || options.continuationInput.compiled != compiled {
			return preparedRequestReplay{}, operationMetadataError("/continuation", "native carrier replay lacks the authenticated source revision")
		}
		original, err = selection.strip(ctx, original, "", snapshotLimits)
		if err != nil {
			return preparedRequestReplay{}, err
		}
		options.continuationInput = selection
	}
	options.Scope = native.Source.Scope
	return preparedRequestReplay{compiled: &replay, original: original, baseline: request, options: options}, nil
}

func (compiled *Compiled) supportsRequestWorkspace() bool {
	return compiled.mappings[DecodeRequest].hasCoreOutputPlan && compiled.mappings[EncodeRequest].hasCoreInputPlan
}

func (compiled *Compiled) attachRequestWorkspace(ctx context.Context, request, baseline *Request) {
	native := request.Native
	if native == nil || !compiled.supportsRequestWorkspace() {
		return
	}
	sourceContext := *native.RequestContext
	sourceContext.Values = maps.Clone(sourceContext.Values)
	native.requestWorkspace = &requestEncodingWorkspace{
		compiled: compiled,
		identity: compiled.identity, source: native.Source, context: sourceContext,
		limits: compiled.wireLimits(ctx, DecodeRequest), original: native.Value, baseline: baseline,
		continuation: request.continuationInput,
	}
}

func (compiled *Compiled) requestReplayBaseline(native *Native, limits Limits) *Request {
	workspace := native.requestWorkspace
	if workspace == nil || workspace.compiled != compiled || !compiled.supportsRequestWorkspace() || workspace.identity != compiled.identity || workspace.source != native.Source || workspace.original != native.Value || workspace.limits != limits {
		return nil
	}
	source := native.RequestContext
	if source == nil || source.Scope != workspace.context.Scope || source.InterleavedThinking != workspace.context.InterleavedThinking || !maps.Equal(source.Values, workspace.context.Values) {
		return nil
	}

	return workspace.baseline
}

func cloneRequestSemantic(request *Request) *Request {
	value := reflect.ValueOf(request)
	return cloneSemantic(value, typedLimitPlans[value.Type()]).Interface().(*Request)
}
