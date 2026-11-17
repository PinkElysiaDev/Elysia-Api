package protocol

import (
	"maps"
	"reflect"
)

// A verdict owns an immutable witness, not a promise that a public Request will
// remain immutable. Public callers can edit any nested slice/map between calls.
// Reuse therefore requires exact structural equality AND identical check inputs.
// A redundant walk is preferable to accepting a stale security-relevant pass.
// Only successful decode publishes a verdict; readers never update shared state.
type requestVerdict struct {
	snapshot *Request
	target   Target
	limits   Limits
}

func retainRequestVerdict(request *Request, target Target, limits Limits) {
	snapshot := cloneRequestSemantic(request)
	snapshot.verdict = nil
	target.Capabilities = maps.Clone(target.Capabilities)
	request.verdict = &requestVerdict{snapshot: snapshot, target: target, limits: limits}
}

func requestContextUnchanged(request *Request) bool {
	return request != nil && request.verdict != nil && equalRequestValidationInputs(request, request.verdict.snapshot)
}

func requestCheckUnchanged(request *Request, target Target, limits Limits) bool {
	if request == nil || request.verdict == nil {
		return false
	}
	verdict := request.verdict
	return verdict.limits == limits && verdict.target.Protocol == target.Protocol &&
		verdict.target.Direction == target.Direction && verdict.target.Scope == target.Scope &&
		maps.Equal(verdict.target.Capabilities, target.Capabilities) && equalRequestValidationInputs(request, verdict.snapshot)
}

var requestClientOutputField = func() int {
	field, _ := reflect.TypeFor[Request]().FieldByName("ClientOutput")
	return field.Index[0]
}()

func equalRequestValidationInputs(left, right *Request) bool {
	// CheckRequest and validateRequestContext never inspect delivery settings.
	// Default conversion edits CollectUsage on every request; that alone cannot
	// invalidate a content verdict. All other exported fields remain compared,
	// even those the validators do not currently read. Replay uses the STRICT
	// comparator below: a mapping can observe any delivery setting.
	leftValue, rightValue := reflect.ValueOf(left).Elem(), reflect.ValueOf(right).Elem()
	for _, field := range typedLimitPlans[reflect.TypeFor[*Request]()].element.fields {
		if field.index != requestClientOutputField && !equalCoreSnapshot(leftValue.Field(field.index), rightValue.Field(field.index), field.plan) {
			return false
		}
	}
	return true
}

func equalRequestSnapshot(left, right *Request) bool {
	return equalCoreSnapshot(reflect.ValueOf(left), reflect.ValueOf(right), typedLimitPlans[reflect.TypeFor[*Request]()])
}

// Core documents are closed, acyclic typed trees. Unlike DeepEqual this walk
// needs neither a visited-pointer map nor interfaces for scalar fields. It must
// not shortcut equal pointers: public mutable containers may alias a witness's
// private metadata, but only Values themselves are immutable. Unexported fields
// are execution receipts, not semantic input; Value.raw and Node's protected
// range association are compared explicitly. Replay checks continuation state
// separately and always rechecks native authorization/provenance.
func equalCoreSnapshot(left, right reflect.Value, plan *typedPlan) bool {
	if plan == nil || !plan.isSupported {
		return false
	}
	if plan.isJSON {
		return left.Field(0).String() == right.Field(0).String()
	}
	switch left.Kind() {
	case reflect.Pointer:
		if left.IsNil() || right.IsNil() {
			return left.IsNil() == right.IsNil()
		}
		return equalCoreSnapshot(left.Elem(), right.Elem(), plan.element)
	case reflect.Struct:
		if left.Type() == reflect.TypeFor[Node]() && left.Field(0).String() != right.Field(0).String() {
			return false
		}
		for _, field := range plan.fields {
			if !equalCoreSnapshot(left.Field(field.index), right.Field(field.index), field.plan) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if left.IsNil() != right.IsNil() || left.Len() != right.Len() {
			return false
		}
		for index := range left.Len() {
			if !equalCoreSnapshot(left.Index(index), right.Index(index), plan.element) {
				return false
			}
		}
		return true
	case reflect.Map:
		if left.IsNil() != right.IsNil() || left.Len() != right.Len() {
			return false
		}
		iterator := left.MapRange()
		for iterator.Next() {
			other := right.MapIndex(iterator.Key())
			if !other.IsValid() || !equalCoreSnapshot(iterator.Value(), other, plan.element) {
				return false
			}
		}
		return true
	case reflect.String:
		return left.String() == right.String()
	case reflect.Bool:
		return left.Bool() == right.Bool()
	case reflect.Int, reflect.Int64:
		return left.Int() == right.Int()
	default:
		// A newly introduced type cannot silently gain a cached validation pass.
		return false
	}
}
