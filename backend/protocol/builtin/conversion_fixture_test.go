package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

// Legacy fixture callers omitted the host's pinned route. Package policies bind
// their target identity; use that declaration when constructing the test host.
func fixtureConversionContext(t testing.TB, conversion *p.CompiledConversion, phase p.ConversionPhase, source p.Identity) p.ConversionContext {
	t.Helper()
	for _, rule := range conversion.Policy.Rules {
		if rule.Phase != phase {
			continue
		}
		fields, err := rule.Value.ReadObject()
		if err != nil {
			continue
		}
		if core := fields["core"]; !core.IsZero() {
			fields, err = core.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
		}
		if fields["target"].IsZero() {
			continue
		}
		var target p.Identity
		if err := fields["target"].Decode(&target); err != nil {
			t.Fatal(err)
		}
		options, err := (p.EvaluationContext{}).WithOperation("conversion-fixture", 1)
		if err != nil {
			t.Fatal(err)
		}
		return (p.ConversionContext{Source: source, Target: target, Model: "m"}).WithOperation(options)
	}
	t.Fatal("fixture conversion has no pinned target", phase)
	return p.ConversionContext{}
}
