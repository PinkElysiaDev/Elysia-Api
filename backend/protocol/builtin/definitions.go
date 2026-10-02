package builtin

import (
	"embed"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

//go:embed definitions/*.json
var definitionsFS embed.FS

// Definitions returns the shipped, editable protocol definitions. Their sample
// expectations are static artifacts; verification never generates its own oracle.
func Definitions() ([]p.Value, error) {
	entries, err := definitionsFS.ReadDir("definitions")
	if err != nil {
		return nil, err
	}
	definitions := make([]p.Value, 0, len(entries))
	for _, entry := range entries {
		raw, err := definitionsFS.ReadFile("definitions/" + entry.Name())
		if err != nil {
			return nil, err
		}
		value, err := p.ParseValue(raw)
		if err != nil {
			return nil, fmt.Errorf("builtin %s: %w", entry.Name(), err)
		}
		definitions = append(definitions, value)
	}
	return definitions, nil
}
