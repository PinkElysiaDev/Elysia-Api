package protocol

import (
	"reflect"
	"testing"
)

func TestGeneratedSemanticCloneMatchesOriginal(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `9007199254740993`, ` {"n":1e100,"n":2} `, `"<>&\u2028"`, "\"\xff\""} {
		payload := parseTestValue(t, raw)
		for kind, plan := range typedLimitPlans {
			value := reflect.New(kind).Elem()
			for _, shouldFill := range []bool{false, true} {
				if shouldFill {
					fillMeterValue(value, payload, 0)
				}
				before := originalCloneSemantic(value, plan)
				got := cloneSemantic(value, plan)
				if !reflect.DeepEqual(got.Interface(), before.Interface()) {
					t.Fatalf("clone changed %s (filled %t)", kind, shouldFill)
				}
				// Value roots must stay addressable for the unchanged normalizer.
				if kind.Kind() == reflect.Struct && !got.CanSet() {
					t.Fatalf("clone lost root addressability: %s", kind)
				}
			}
		}
	}
}

// originalCloneSemantic owns all mutable containers, including nested Native pointers.
// Unlike a JSON round trip, copying an immutable Value never copies its bytes.
func originalCloneSemantic(value reflect.Value, plan *typedPlan) reflect.Value {
	if plan.isJSON {
		return value
	}
	switch plan.kind {
	case reflect.Pointer:
		if value.IsNil() {
			return value
		}
		result := reflect.New(value.Type().Elem())
		originalCloneSemanticInto(result.Elem(), value.Elem(), plan.element)
		return result
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		originalCloneSemanticInto(result, value, plan)
		return result
	case reflect.Slice:
		if value.IsNil() {
			return value
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := range value.Len() {
			originalCloneSemanticInto(result.Index(index), value.Index(index), plan.element)
		}
		return result
	case reflect.Map:
		if value.IsNil() {
			return value
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result.SetMapIndex(iter.Key(), originalCloneSemantic(iter.Value(), plan.element))
		}
		return result
	default:
		// Scalars are copied by the owning container's Set. A separate
		// addressable reflect.New holder adds no ownership or isolation.
		return value
	}
}

// Populate already-owned struct storage directly. Pointer destinations and slice
// elements need no additional temporary struct allocation before assignment.
func originalCloneSemanticInto(destination, value reflect.Value, plan *typedPlan) {
	if plan.kind != reflect.Struct || plan.isJSON {
		destination.Set(originalCloneSemantic(value, plan))
		return
	}
	destination.Set(value)
	for _, field := range plan.fields {
		originalCloneSemanticInto(destination.Field(field.index), value.Field(field.index), field.plan)
	}
}
