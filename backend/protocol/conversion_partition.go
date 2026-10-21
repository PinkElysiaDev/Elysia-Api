package protocol

import "reflect"

// requestPartition summarizes the normalized, untouched portion of one owned
// Request across a contiguous run of explicitly header/tool-local rules. It
// stores numbers only, never slices, semantic nodes, policy verdicts or payloads.
// Any other action/JSON boundary invalidates it before executing the mutation.
type requestPartition struct {
	fixed  semanticMetrics
	fields int
}

var requestPartitionFields = func() (result struct{ fixed, mutable []typedField }) {
	typeOf := reflect.TypeFor[Request]()
	plan := typedLimitPlans[reflect.TypeFor[*Request]()].element
	for _, field := range plan.fields {
		switch typeOf.Field(field.index).Name {
		case "Parameters", "ClientOutput", "Tools":
			result.mutable = append(result.mutable, field)
		default:
			result.fixed = append(result.fixed, field)
		}
	}
	return
}()

func headerLocalRequestRule(action string) bool {
	switch action {
	case "stream_options", "responses_include", "responses_storage", "gemini_safety_settings":
		return true
	}
	return false
}

// Summary setup is not amortized by one-off actions. Only an unconditional
// contiguous run of at least four local rules opts in; normal short/default
// runs retain the existing path. Selection is linear once at compilation, not
// a per-request lookahead or an authored option that can assert cache validity.
func markRequestPartitions(rules []ConversionRule) {
	for start := 0; start < len(rules); {
		end := start
		for end < len(rules) && headerLocalRequestRule(rules[end].Action) && rules[end].Match == (ConversionMatch{}) {
			end++
		}
		if end-start >= 4 {
			for i := start; i < end; i++ {
				rules[i].partitionRequest = true
			}
		}
		start = end + 1
	}
}

func measureRequestFields(meter *typedMeter, value reflect.Value, fields []typedField, count *int) bool {
	for _, field := range fields {
		child := value.Field(field.index)
		if field.omitZero && child.IsZero() || field.omitEmpty && isEmptyJSONField(child) {
			continue
		}
		if *count > 0 {
			meter.bytes++
		}
		*count += 1
		meter.bytes++ // colon
		if !meter.scalar(field.keyBytes) || !meter.visit(field.plan, child) {
			return false
		}
	}
	return true
}

func (state *conversionState) prepareRequestPartition(phase ConversionPhase, action string) {
	if phase != ConversionRequest || !headerLocalRequestRule(action) {
		state.requestPartition = nil
		return
	}
	if state.requestPartition != nil || !state.requestNormalized {
		return
	}
	request := state.document.(*Request)
	// Tiny requests keep the direct generated walk, avoiding summary setup.
	if len(request.Content) < 64 && (request.Native == nil || request.Native.Value.Len() < 64<<10) {
		return
	}
	meter := typedMeter{limits: state.limits, cache: state.cache}
	fixed := *request
	fixed.Parameters, fixed.ClientOutput, fixed.Tools = nil, nil, nil
	// Use the generated walker for the one full immutable-part traversal too.
	// Only root omission counting needs reflection; it never descends into history.
	if !meter.visitSemantic(&fixed) || !meter.fits() {
		return
	}
	count := 0
	value := reflect.ValueOf(request).Elem()
	for _, field := range requestPartitionFields.fixed {
		child := value.Field(field.index)
		if field.omitZero && child.IsZero() || field.omitEmpty && isEmptyJSONField(child) {
			continue
		}
		count++
	}
	state.requestPartition = &requestPartition{fixed: semanticMetrics{bytes: meter.bytes, nodes: meter.nodes, depth: meter.maxDepth}, fields: count}
}

func (state *conversionState) checkRequestPartition() bool {
	partition := state.requestPartition
	if partition == nil || partition.fixed.depth > min(state.limits.Depth, typedMeterTraversalDepth) {
		return false
	}
	meter := typedMeter{limits: state.limits, cache: state.cache, bytes: partition.fixed.bytes, nodes: partition.fixed.nodes, depth: 1, maxDepth: partition.fixed.depth}
	count := partition.fields
	// The fixed summary already includes the root's opening/closing tokens.
	return meter.fits() && measureRequestFields(&meter, reflect.ValueOf(state.document).Elem(), requestPartitionFields.mutable, &count) && meter.fits()
}

func (state *conversionState) normalizeRequestPartition() {
	value := reflect.ValueOf(state.document).Elem()
	for _, field := range requestPartitionFields.mutable {
		child := value.Field(field.index)
		if field.omitZero && child.IsZero() || field.omitEmpty && isEmptyJSONField(child) {
			child.SetZero()
		} else {
			state.normalize(child, field.plan)
		}
	}
}
