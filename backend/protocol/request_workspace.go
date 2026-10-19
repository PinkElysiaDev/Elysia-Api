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
// candidate projections. It is attached privately after successful decoding and
// disappears across an authored JSON boundary. Nothing is cached globally.
type requestEncodingWorkspace struct {
	compiled *Compiled
	identity Identity
	source   Provenance
	context  NativeRequestContext
	limits   Limits
	original Value
	baseline *Request
}

// CanReuseRequestEncoding reports whether a private gateway preflight result
// can be reused with identical request, revision, scope, context and budgets.
// It never authorizes skipping final wire or operation validation.
func (compiled *Compiled) CanReuseRequestEncoding() bool {
	return compiled.supportsRequestWorkspace()
}

func (compiled *Compiled) supportsRequestWorkspace() bool {
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		mapping := compiled.mappings[direction]
		if _, ok := mapping.module.(RequestBaselineModule); !ok || mapping.after != nil || mapping.initial != nil {
			return false
		}
		if direction == DecodeRequest && mapping.output != nil || direction == EncodeRequest && mapping.input != nil {
			return false
		}
	}
	return true
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
