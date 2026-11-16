package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestShippedPackageLoaderUsesAuthoredSources(t *testing.T) {
	packages, err := Packages()
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{
		"openai-chat-completions": "openai-chat",
		"openai-responses":        "openai-responses",
		"anthropic-messages":      "anthropic",
		"google-generate-content": "google-generate-content",
	}
	if len(packages) != len(wanted) {
		t.Fatal("unexpected default package count", len(packages))
	}
	for _, raw := range packages {
		compiled, issues := compiler.Compile(raw.Bytes())
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		document, ok := compiled.Package()
		if !ok || document.SchemaVersion != p.ProtocolPackageSchemaVersion || document.Mapping.CoreVersion != p.ProtocolCoreVersion {
			t.Fatal("loader did not return a current package", compiled.Identity())
		}
		name, ok := wanted[compiled.Identity().DefinitionID]
		if !ok {
			t.Fatal("duplicate or unknown default package", compiled.Identity())
		}
		delete(wanted, compiled.Identity().DefinitionID)
		spec, err := packagesFS.ReadFile("packages/" + name + "/spec.json")
		if err != nil {
			t.Fatal(err)
		}
		mapping, err := packagesFS.ReadFile("packages/" + name + "/mapping.json")
		if err != nil {
			t.Fatal(err)
		}
		paired, issues := compiler.CompileSources(spec, mapping)
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		if compiled.Identity() != paired.Identity() || compiled.Hash() != paired.Hash() || compiled.ExecutionHash() != paired.ExecutionHash() || compiled.SamplesHash() != paired.SamplesHash() {
			t.Fatal("default loader diverges from the authored Spec/Mapping pair", name)
		}
	}
}
