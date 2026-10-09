package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestBuiltinVerifiedProfilesKeepTextRoutesAndFullDiagnostics(t *testing.T) {
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	var adapters []*p.Compiled
	for _, definition := range definitions {
		compiled, issues := compiler.Compile(definition.Bytes())
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		adapters = append(adapters, compiled)
	}
	for _, ingress := range adapters {
		for _, upstream := range adapters {
			t.Run(ingress.Identity().DefinitionID+"/"+upstream.Identity().DefinitionID, func(t *testing.T) {
				contract := upstream.Definition().Capabilities
				reports := p.VerifyBindingProfiles(t.Context(), ingress, upstream, contract)
				if reports[0].IsRestricted || reports[0].BindingHash != p.CapabilityContractHash(contract) {
					t.Fatal("full contract evidence missing")
				}
				isSameWire := ingress.Identity().Family == upstream.Identity().Family
				if reports[0].Passed != isSameWire {
					t.Fatal("cross-family full support was claimed", reports[0])
				}
				hasText, hasCache := false, false
				for _, report := range reports {
					if !report.Passed {
						continue
					}
					if report.BindingHash != reports[0].BindingHash {
						t.Fatal("profile does not bind its parent contract")
					}
					hasText = hasText || (report.Capabilities[p.TextCapability] && report.Capabilities[p.UsageCapability])
					hasCache = hasCache || (report.Capabilities[p.CacheKeysCapability] && report.Capabilities[p.CacheRetentionCapability])
					for capability, supported := range report.Capabilities {
						if supported && !contract[capability] {
							t.Fatal("profile expanded model contract", capability)
						}
					}
					if !isSameWire && report.Capabilities[p.NativeExtensionsCapability] {
						t.Fatal("foreign native extensions claimed")
					}
				}
				if !hasText {
					t.Fatal("text/usage route blocked by unrelated capabilities", reports)
				}
				if ingress.Definition().Capabilities[p.CacheKeysCapability] && contract[p.CacheKeysCapability] && !hasCache {
					t.Fatal("joint cache key/retention evidence split into unsupported fragments")
				}
			})
		}
	}
}
