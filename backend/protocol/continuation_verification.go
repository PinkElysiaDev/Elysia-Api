package protocol

import (
	"context"
	"slices"
	"strconv"
	"time"
)

// prepareContinuationEvidence uses the production seal/open/restore path. The
// returned binding still requires actual carrier rendering and final validation.
func prepareContinuationEvidence(ctx context.Context, conversion *CompiledConversion, target *Compiled, phase ConversionPhase, nodes []Node, route ConversionContext) (ConversionContext, error) {
	evidence, err := newContinuationEvidence(conversion, target, phase, route)
	if err != nil || evidence == nil {
		return route, err
	}
	if err := evidence.capture(ctx, nodes); err != nil {
		return route, err
	}
	return route.WithContinuation(evidence.binding, route.requireContinuation), nil
}

type continuationEvidence struct {
	conversion *CompiledConversion
	phase      ConversionPhase
	route      ConversionContext
	codec      *ContinuationCodec
	binding    *ContinuationBinding
}

func newContinuationEvidence(conversion *CompiledConversion, target *Compiled, phase ConversionPhase, route ConversionContext) (*continuationEvidence, error) {
	if target.packagePlan == nil || conversion == nil || conversion.Policy.Continuation == nil || !conversion.HasContinuationRecovery(phase, route) {
		return nil, nil
	}
	settings := conversion.Policy.Continuation
	binding, err := target.BindContinuation(controlDirection(phase, false), route.Source, route.Scope, settings.ClientCarrier)
	if err != nil {
		return nil, err
	}
	codec, err := NewContinuationCodec(randBytesForPreview())
	if err != nil {
		return nil, err
	}
	return &continuationEvidence{conversion, phase, route, codec, binding}, nil
}

func (evidence *continuationEvidence) capture(ctx context.Context, nodes []Node) error {
	for _, node := range nodes {
		needsRecovery, err := evidence.conversion.NeedsContinuationRecovery(ctx, evidence.phase, node, evidence.route)
		if err != nil {
			return err
		}
		if needsRecovery {
			if err := evidence.captureNode(node); err != nil {
				return err
			}
		}
		if err := evidence.capture(ctx, node.Children); err != nil {
			return err
		}
		if err := evidence.capture(ctx, node.ReasoningContent); err != nil {
			return err
		}
	}
	return nil
}

func (evidence *continuationEvidence) captureNode(node Node) error {
	digest := ContinuationNodeDigest(node)
	hasEvidence := true
	for _, resource := range node.Resources {
		if IsContinuationResource(resource) && evidence.binding.resources[digest+":"+ContinuationResourceKey(resource)] == nil {
			hasEvidence = false
		}
	}
	if hasEvidence {
		return nil
	}
	route := evidence.route
	record := ContinuationRecord{Version: 1, ID: "offline-" + strconv.Itoa(len(evidence.binding.entries)), Owner: "offline", Protocol: route.Source, Scope: route.Scope, Digest: digest, Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	limit := evidence.conversion.Policy.Continuation.RecordBytes
	sealed, err := evidence.codec.SealState(record, limit)
	if err != nil {
		return err
	}
	opened, err := evidence.codec.Open(sealed.Token(), limit)
	if err != nil {
		return err
	}
	restored := node
	restored.Resources = slices.DeleteFunc(slices.Clone(node.Resources), IsContinuationResource)
	if err := RestoreContinuation(&restored, opened, record.Owner, route.Scope, route.Source); err != nil {
		return err
	}
	return evidence.binding.Append(sealed)
}
