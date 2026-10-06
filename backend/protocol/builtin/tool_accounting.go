package builtin

import (
	"encoding/json"
	"fmt"
	"strconv"

	p "github.com/elysia-api/backend/protocol"
)

var hostedToolKinds = map[string]string{
	"web_search_call": "web_search", "file_search_call": "file_search", "image_generation_call": "image_generation",
	"code_interpreter_call": "code_interpreter", "computer_call": "computer_use", "computer_use": "computer_use",
}

// ToolAccounting observes known native calls without interpreting or executing
// their payload. The source identity is supplied by the pinned decoder, never
// guessed from a type field. Unknown protocols use their declared usage mapping.
type ToolAccounting struct {
	items  map[int]string
	counts map[string]int
	limit  int
}

// NewToolAccounting bounds correlation storage independently of stream length.
func NewToolAccounting(limit int) *ToolAccounting {
	return &ToolAccounting{items: map[int]string{}, counts: map[string]int{}, limit: limit}
}

// Observe correlates completed items with the final response snapshot so a
// usage tail or repeated snapshot cannot charge a hosted call twice.
func (accounting *ToolAccounting) Observe(identity p.Identity, wire []byte) error {
	if identity.Family != "openai_responses" || identity.WireVersion != "v2" {
		return nil
	}
	var fields p.Object
	err := json.Unmarshal(wire, &fields)
	if err != nil {
		return err
	}
	if kind := fields["type"]; kind == p.StringValue("response.output_item.done") {
		var index int
		if err := fields["output_index"].Decode(&index); err != nil || fields["output_index"].IsNull() {
			index = -1
		}
		return accounting.observeItem(index, fields["item"])
	}
	if kind := fields["type"]; kind == p.StringValue("response.completed") || kind == p.StringValue("response.incomplete") {
		fields, err = fields["response"].ReadObject()
		if err != nil {
			return err
		}
	}
	items, err := readArray(fields["output"])
	if err != nil {
		return err
	}
	for index, item := range items {
		if err := accounting.observeItem(index, item); err != nil {
			return err
		}
	}
	return nil
}

func (accounting *ToolAccounting) observeItem(index int, item p.Value) error {
	fields, err := item.ReadObject()
	if err != nil {
		return err
	}
	typeName, err := optionalString(fields["type"])
	if err != nil {
		return err
	}
	kind, exists := hostedToolKinds[typeName]
	if !exists {
		return nil
	}
	if index < 0 {
		return fmt.Errorf("hosted call accounting requires its output index")
	}
	if previous, exists := accounting.items[index]; exists {
		if previous != kind {
			return unsupported("/output/"+strconv.Itoa(index), "hosted call changed kind after completion")
		}
		return nil
	}
	if len(accounting.items) >= accounting.limit {
		return &p.ConversionError{Issues: []p.ConversionIssue{{Code: p.LimitExceeded, Severity: p.SeverityError, Stage: "accounting", Path: "/output", Reason: "hosted call correlation limit exceeded"}}}
	}
	accounting.items[index] = kind
	accounting.counts[kind]++
	return nil
}

// Count returns observed invocation count; configured tool definitions do not count.
func (accounting *ToolAccounting) Count(kind string) int { return accounting.counts[kind] }
