package protocol

import (
	"context"
	"maps"
	"reflect"
)

// RequestBaselineModule opts a stateless typed adapter into request-local replay
// reuse. Its decoder must be pure (including diagnostics) and its typed result
// must implement exactly the same semantic contract as Convert.
type RequestBaselineModule interface {
	TypedModule
	RequestBaselineSafe()
}

// requestEncodingWorkspace owns one immutable parsed baseline, never a set of
// candidate projections. Pure package plans and supported identity module
// after-mappings preserve it; a semantic JSON boundary drops it. It is attached
// privately after successful decoding and is never cached globally.
type requestEncodingWorkspace struct {
	compiled     *Compiled
	identity     Identity
	source       Provenance
	context      NativeRequestContext
	limits       Limits
	original     Value
	baseline     *Request
	continuation *continuationInput
}

// requestReplay belongs to one synchronous encode, including its baseline
// encoding. It never attaches reconstructed state to the caller's Native.
type requestReplay struct {
	compiled *Compiled
	options  EvaluationContext
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
	if request == nil {
		request, err = replay.decodeRequestValue(ctx, original, options, &native.Source.Scope)
	} else {
		decoded := request
		if err = ctx.Err(); err == nil {
			request, err = replay.finishDecodedRequest(ctx, original, cloneRequestSemantic(request), options, &native.Source.Scope)
		}
		if err == nil && replay.packagePlan != nil {
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

// CanReuseRequestEncoding reports whether a private gateway preflight result
// can be reused with identical request, revision, scope, context and budgets.
// It never authorizes skipping final wire or operation validation.
func (compiled *Compiled) CanReuseRequestEncoding() bool {
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		mapping := compiled.mappings[direction]
		if !compiled.hasPureRequestMapping(direction) || mapping.after != nil && !mapping.after.isInputIdentity() {
			return false
		}
	}
	return true
}

func (compiled *Compiled) hasPureRequestMapping(direction Direction) bool {
	mapping := compiled.mappings[direction]
	if _, ok := mapping.module.(RequestBaselineModule); !ok || mapping.initial != nil {
		return false
	}
	return direction == DecodeRequest && mapping.output == nil || direction == EncodeRequest && mapping.input == nil
}

func (compiled *Compiled) supportsRequestWorkspace() bool {
	if compiled.packagePlan != nil {
		return compiled.mappings[DecodeRequest].hasCoreOutputPlan && compiled.mappings[EncodeRequest].hasCoreInputPlan
	}
	// Both request boundaries now execute identity checks without a JSON round
	// trip. Reuse still depends on the exact source, context and effective limits.
	return compiled.hasTypedRequestMapping(DecodeRequest) && compiled.hasTypedRequestMapping(EncodeRequest)
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
