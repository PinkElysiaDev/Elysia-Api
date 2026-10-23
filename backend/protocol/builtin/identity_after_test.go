package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestIdentityAfterEncodingEligibility(t *testing.T) {
	for _, name := range []string{"absent", "identity", "explicit-input", "root", "required"} {
		t.Run(name, func(t *testing.T) {
			definition := testCompiled(t, Anthropic).Definition()
			for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
				mapping := definition.Directions[direction]
				after := &p.Expression{Op: "read"}
				switch name {
				case "absent":
					after = nil
				case "explicit-input":
					after.From = "input"
				case "root":
					after.From = "root"
				case "required":
					after.Required = true
				}
				mapping.After = after
				definition.Directions[direction] = mapping
			}
			compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := p.EncodeValue(definition)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.Compile(raw.Bytes())
			if compiled == nil {
				t.Fatal(issues)
			}
			want := name == "absent" || name == "identity" || name == "explicit-input"
			if compiled.CanReuseRequestEncoding() != want {
				t.Fatalf("encoding eligibility = %t, want %t", compiled.CanReuseRequestEncoding(), want)
			}
		})
	}
}
