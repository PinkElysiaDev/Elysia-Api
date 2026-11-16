package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
	"slices"
)

func object(fields p.Object) p.Value {
	for key, value := range fields {
		if value.IsZero() {
			delete(fields, key)
		}
	}
	value, err := p.EncodeValue(fields)
	if err != nil {
		panic(err)
	}
	return value
}
func array(items []p.Value) p.Value {
	if items == nil {
		items = []p.Value{}
	}
	value, err := p.EncodeValue(items)
	if err != nil {
		panic(err)
	}
	return value
}
func readArray(value p.Value) ([]p.Value, error) {
	if value.IsZero() {
		return nil, nil
	}
	if value.IsNull() {
		return nil, fmt.Errorf("expected array, got null")
	}
	var items []p.Value
	err := value.Decode(&items)
	return items, err
}
func collectUnknown(fields p.Object, known []string) p.Value {
	extra := p.Object{}
	for name, value := range fields {
		if !slices.Contains(known, name) {
			extra[name] = value
		}
	}
	if len(extra) == 0 {
		return p.Value{}
	}
	return object(extra)
}

func stringValue(value p.Value) (string, error) {
	if value.IsNull() {
		return "", fmt.Errorf("expected string, got null")
	}
	var text string
	err := value.Decode(&text)
	return text, err
}
