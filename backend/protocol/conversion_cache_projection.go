package protocol

import (
	"context"
	"fmt"
)

func (c *CompiledConversion) projectCacheIntentGroup(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, plan *compiledRequestProjection, sink *DiagnosticSink, budget *evaluationBudget) error {
	if !hasRequestCacheIntents(request) {
		return nil
	}
	return requestCaches(request, func(intents *[]CacheIntent, at string) error {
		kept := 0
		for index, intent := range *intents {
			shouldDrop := false
			for _, effect := range plan.effects {
				isMatch, err := matchCacheDisposition(ctx, intent, effect, budget)
				if err != nil {
					return err
				}
				if !isMatch {
					continue
				}
				path := fmt.Sprintf("%s/%d", at, index)
				if intent.Kind == "resource" || intent.Resource != nil {
					return c.requestProjectionError(rule, route, path, UnsupportedCapability, "cache resources require an explicit target representation and cannot be omitted by cache disposition")
				}
				switch intent.Kind {
				case "key", "retention", "breakpoint", "mode", "options.ttl", "prewarm":
				default:
					return c.requestProjectionError(rule, route, path+"/kind", InvalidInput, "unknown cache intent cannot be omitted by cache disposition")
				}
				if plan.spec.Native && request.Native != nil && CanPreserveNative(request.Native.Source, Target{Protocol: plan.spec.Target, Direction: EncodeRequest}) {
					return c.requestProjectionError(rule, route, path, UnsupportedNative, "cache disposition cannot prove that same-wire native replay will not restore the omitted field")
				}
				if effect.kind == "default" {
					c.normalized(rule, route, path, effect.reason, sink)
				} else if err := c.issueSeverity(rule, ConversionRequest, route, path, effect.reason, sink, true, effect.severity); err != nil {
					return err
				}
				shouldDrop = true
				break
			}
			if !shouldDrop {
				(*intents)[kept] = intent
				kept++
			}
		}
		if kept != len(*intents) {
			clear((*intents)[kept:])
			*intents = (*intents)[:kept]
		}
		return nil
	})
}
