// Package builtin contains the shipped declarative protocol packages.
// Every mapping connects its own wire contract to the shared semantic core.
package builtin

import (
	"embed"
	"fmt"
	"strconv"

	p "github.com/elysia-api/backend/protocol"
)

const mappingSeparator = `,"mapping":`

//go:embed packages/*/spec.json packages/*/mapping.json
var packagesFS embed.FS

// Packages returns the shipped Spec/Mapping pairs as complete authored packages.
// Their static samples still require verification before a revision is activated.
func Packages() ([]p.Value, error) {
	entries, err := packagesFS.ReadDir("packages")
	if err != nil {
		return nil, err
	}
	packages := make([]p.Value, 0, len(entries))
	for _, entry := range entries {
		value, err := readPackage(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("builtin %s: %w", entry.Name(), err)
		}
		packages = append(packages, value)
	}
	return packages, nil
}

func readPackage(name string) (p.Value, error) {
	spec, err := packagesFS.ReadFile("packages/" + name + "/spec.json")
	if err != nil {
		return p.Value{}, err
	}
	mapping, err := packagesFS.ReadFile("packages/" + name + "/mapping.json")
	if err != nil {
		return p.Value{}, err
	}
	// Preserve the authored source bytes so compilation meters the real package.
	prefix := `{"schemaVersion":` + strconv.Itoa(p.ProtocolPackageSchemaVersion) + `,"spec":`
	raw := make([]byte, 0, len(prefix)+len(spec)+len(mappingSeparator)+len(mapping)+1)
	raw = append(raw, prefix...)
	raw = append(raw, spec...)
	raw = append(raw, mappingSeparator...)
	raw = append(raw, mapping...)
	raw = append(raw, '}')
	return p.ParseValue(raw)
}
