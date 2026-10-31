package protocol

import "strings"

const frameUsageCounterBytes = 8

type frameUsageObservation struct {
	source, target Identity
	scope          Scope
	byResponse     map[string]*Usage
	unassigned     *Usage
	onlyResponse   string
}

// WithFrameUsage binds an immutable numeric snapshot of this source frame when
// an enabled event rule requests it. A bound frame without counters is distinct
// from missing frame context. Attribution, billing and unknown raw objects are
// excluded; source events and their accounting remain unchanged.
func (conversion *CompiledConversion) WithFrameUsage(route ConversionContext, events []Event) (ConversionContext, error) {
	route.frameUsage = nil
	if conversion == nil || !conversion.needsFrameUsage {
		return route, nil
	}
	if len(events) > conversion.limits.Nodes {
		return route, resourceFailure("frame_usage", "events", conversion.limits.Nodes, len(events))
	}
	facts := &frameUsageObservation{source: route.Source, target: route.Target, scope: route.Scope}
	budget := frameUsageBudget{limits: conversion.limits, nodes: len(events)}
	var identity Value
	hasCounters, hasMultipleResponses := false, false
	for _, event := range events {
		if err := budget.observe(event); err != nil {
			return route, err
		}
		hasCounters = hasCounters || hasFrameEventCounters(event)
		id := frameUsageIdentityValue(event)
		if !id.IsZero() && !id.IsNull() {
			if identity.IsZero() {
				identity = id
			} else if !hasMultipleResponses {
				hasMultipleResponses = !equalValues(identity, id)
			}
		}
	}
	if !hasCounters {
		route.frameUsage = facts
		return route, nil
	}
	if !hasMultipleResponses && !identity.IsZero() {
		id, err := readString(identity)
		if err != nil {
			return route, err
		}
		facts.onlyResponse = strings.Clone(id)
	}
	// Decide whether unassigned counters have one unambiguous owner before
	// merging. Their position in the original frame still controls precedence.
	for _, event := range events {
		if !hasFrameEventCounters(event) {
			continue
		}
		id, err := frameUsageIdentity(event)
		if err != nil {
			return route, err
		}
		if id == "" {
			id = facts.onlyResponse
		}
		var usage *Usage
		if id == "" {
			usage = facts.unassigned
		} else {
			usage = facts.byResponse[id]
			if usage == nil && len(facts.byResponse) >= conversion.limits.StateItems {
				return route, resourceFailure("frame_usage", "responses", conversion.limits.StateItems, len(facts.byResponse)+1)
			}
		}
		usage = mergeFrameUsage(usage, event.Usage)
		if event.Response != nil {
			usage = mergeFrameUsage(usage, event.Response.Usage)
		}
		if id == "" {
			facts.unassigned = usage
			continue
		}
		if facts.byResponse == nil {
			facts.byResponse = make(map[string]*Usage)
		}
		facts.byResponse[strings.Clone(id)] = usage
	}
	for _, usage := range facts.byResponse {
		ownFrameUsage(usage)
	}
	if facts.unassigned != nil {
		ownFrameUsage(facts.unassigned)
	}
	route.frameUsage = facts
	return route, nil
}

func frameUsageIdentity(event Event) (string, error) {
	id := frameUsageIdentityValue(event)
	if id.IsZero() || id.IsNull() {
		return "", nil
	}
	return readString(id)
}

func frameUsageIdentityValue(event Event) Value {
	id := event.ResponseID
	if (id.IsZero() || id.IsNull()) && event.Response != nil {
		id = event.Response.ID
	}
	return id
}

// Snapshot bounds count only the numeric observation being copied. Source
// shape and lifecycle validation remain at their existing stream boundary.
type frameUsageBudget struct {
	limits       Limits
	nodes, bytes int
}

func (budget *frameUsageBudget) observe(event Event) error {
	if err := budget.reserve(0, frameUsageIdentityValue(event).Len()); err != nil {
		return err
	}
	if err := budget.usage(event.Usage); err != nil {
		return err
	}
	if event.Response != nil {
		return budget.usage(event.Response.Usage)
	}
	return nil
}

func (budget *frameUsageBudget) usage(usage *Usage) error {
	if usage == nil {
		return nil
	}
	if err := budget.reserve(len(usage.Details), 0); err != nil {
		return err
	}
	for _, counter := range []*Counter{usage.Input, usage.Output, usage.Total, usage.CacheRead, usage.CacheCreation} {
		if counter != nil {
			if err := budget.reserve(1, frameUsageCounterBytes+len(counter.Origin)); err != nil {
				return err
			}
		}
	}
	for name, counter := range usage.Details {
		if err := budget.reserve(0, len(name)+frameUsageCounterBytes+len(counter.Origin)); err != nil {
			return err
		}
	}
	return nil
}

func (budget *frameUsageBudget) reserve(nodes, bytes int) error {
	if nodes > budget.limits.Nodes-budget.nodes {
		return resourceFailure("frame_usage", "nodes", budget.limits.Nodes, budget.nodes+nodes)
	}
	if bytes > budget.limits.BufferBytes-budget.bytes {
		return resourceFailure("frame_usage", "bytes", budget.limits.BufferBytes, budget.bytes+bytes)
	}
	budget.nodes += nodes
	budget.bytes += bytes
	return nil
}

func mergeFrameUsage(current, update *Usage) *Usage {
	if !hasFrameUsageCounters(update) {
		return current
	}
	numeric := Usage{Input: update.Input, Output: update.Output, Total: update.Total, CacheRead: update.CacheRead, CacheCreation: update.CacheCreation, Details: update.Details}
	return MergeUsage(current, &numeric)
}

func hasFrameEventCounters(event Event) bool {
	return hasFrameUsageCounters(event.Usage) || event.Response != nil && hasFrameUsageCounters(event.Response.Usage)
}

func hasFrameUsageCounters(usage *Usage) bool {
	return usage != nil && (usage.Input != nil || usage.Output != nil || usage.Total != nil || usage.CacheRead != nil || usage.CacheCreation != nil || len(usage.Details) != 0)
}

func ownFrameUsage(usage *Usage) {
	for _, counter := range []*Counter{usage.Input, usage.Output, usage.Total, usage.CacheRead, usage.CacheCreation} {
		if counter != nil {
			counter.Origin = CounterOrigin(strings.Clone(string(counter.Origin)))
		}
	}
	if len(usage.Details) != 0 {
		details := make(map[string]Counter, len(usage.Details))
		for name, counter := range usage.Details {
			counter.Origin = CounterOrigin(strings.Clone(string(counter.Origin)))
			details[strings.Clone(name)] = counter
		}
		usage.Details = details
	}
}

func (facts *frameUsageObservation) forEvent(event Event) (*Usage, error) {
	if facts.byResponse == nil && facts.unassigned == nil {
		return nil, nil
	}
	id, err := frameUsageIdentity(event)
	if err != nil {
		return nil, err
	}
	if id == "" {
		if facts.onlyResponse != "" {
			return facts.byResponse[facts.onlyResponse], nil
		}
		return facts.unassigned, nil
	}
	return facts.byResponse[id], nil
}

func validFrameUsagePointer(pointer []string) bool {
	if len(pointer) == 0 {
		return true
	}
	switch pointer[0] {
	case "details":
		if len(pointer) <= 2 {
			return true
		}
		pointer = pointer[2:]
	case "input", "output", "total", "cacheRead", "cacheCreation":
		pointer = pointer[1:]
	default:
		return false
	}
	return len(pointer) == 0 || len(pointer) == 1 && (pointer[0] == "count" || pointer[0] == "origin")
}
