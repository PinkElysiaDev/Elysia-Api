package builtin

import (
	"encoding/json"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestVisibleThinkingProfilesKeepSummaryBoundary(t *testing.T) {
	for _, from := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, to := range []string{Chat, Responses, Anthropic, Gemini} {
			t.Run(from+"/"+to, func(t *testing.T) {
				a, b := shippedProjectionProtocol(t, from), shippedProjectionProtocol(t, to)
				reports := p.VerifyBindingProfiles(t.Context(), a, b, b.Definition().Capabilities)
				var found bool
				for _, report := range reports {
					if !report.Passed || !report.Capabilities[p.ReasoningCapability] {
						continue
					}
					found = true
					if report.VisibleReasoningOnly {
						if !report.IsRestricted {
							t.Fatal("limited proof claims full contract")
						}
						data, _ := json.Marshal(report)
						var restored p.CombinationReport
						if err := json.Unmarshal(data, &restored); err != nil || !restored.VisibleReasoningOnly {
							t.Fatal("persisted restriction lost", err)
						}
						plain := &p.Request{Content: []p.Node{{Kind: p.ReasoningNode, Payload: p.StringValue("visible")}}}
						if issues := p.CheckCombinationRequest(plain, restored, b.Identity()); len(issues) > 0 {
							t.Fatal(issues)
						}
						for _, form := range []p.ReasoningForm{p.SummaryReasoning, p.StructuredReasoning} {
							unsupported := &p.Request{Content: []p.Node{{Kind: p.MessageNode, Children: []p.Node{{Kind: p.ReasoningNode, ReasoningForm: form, Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("summary")}}}}}}}
							if issues := p.CheckCombinationRequest(unsupported, restored, b.Identity()); len(issues) != 1 || issues[0].Path != "/content/0/children/0/reasoningForm" {
								t.Fatal("profile accepted unsupported reasoning", issues)
							}
						}
						for _, check := range report.Checks {
							if check.Skipped && check.Passed {
								t.Fatal("excluded case counted as coverage")
							}
						}
					}
				}
				if !found {
					t.Fatal("visible history blocked", reports)
				}
			})
		}
	}
}

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
