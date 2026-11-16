package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strconv"
	"strings"
	"testing"
)

func fixturePointerValue(t *testing.T, raw []byte, pointer string) *string {
	t.Helper()
	value, err := p.ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		if value.IsObject() {
			fields, err := value.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			value = fields[token]
		} else {
			var items []p.Value
			index, err := strconv.Atoi(token)
			if err != nil || value.Decode(&items) != nil || index < 0 || index >= len(items) {
				t.Fatalf("invalid migration snapshot pointer %s", pointer)
			}
			value = items[index]
		}
	}
	if value.IsZero() {
		return nil
	}
	text := value.JSONString()
	return &text
}
