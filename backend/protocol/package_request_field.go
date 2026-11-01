package protocol

import (
	"fmt"
	"slices"
)

// Field dispositions clear only independent Value leaves. Source, permissions,
// resources and delivery intent are outside this projection's write authority.
func compileRequestFieldClear(path string) (func(*Request), error) {
	pointer, err := parsePointer(path)
	if err != nil {
		return nil, err
	}
	if len(pointer) == 2 && pointer[0] == "parameters" {
		name := pointer[1]
		return func(request *Request) { delete(request.Parameters, name) }, nil
	}
	if len(pointer) != 2 || pointer[0] != "reasoning" || !slices.Contains([]string{"effort", "budget", "summary", "historyRetention"}, pointer[1]) {
		return nil, fmt.Errorf("request_field requires a parameter or reasoning Value leaf")
	}
	var selectField func(*ReasoningIntent) *Value
	switch pointer[1] {
	case "effort":
		selectField = func(intent *ReasoningIntent) *Value { return &intent.Effort }
	case "budget":
		selectField = func(intent *ReasoningIntent) *Value { return &intent.Budget }
	case "summary":
		selectField = func(intent *ReasoningIntent) *Value { return &intent.Summary }
	case "historyRetention":
		selectField = func(intent *ReasoningIntent) *Value { return &intent.HistoryRetention }
	}
	return func(request *Request) {
		*selectField(request.Reasoning) = Value{}
		if pointer[1] == "effort" {
			request.Reasoning.EffortScope = ""
		}
		if *request.Reasoning == (ReasoningIntent{}) {
			request.Reasoning = nil
		}
	}, nil
}
