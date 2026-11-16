package builtin

import (
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func assertVisibleThinkingProfiles(t *testing.T, upstream *p.Compiled, reports []p.CombinationReport) {
	t.Helper()
	var found bool
	for _, report := range reports {
		if !report.Passed || !report.Capabilities[p.ReasoningCapability] {
			continue
		}
		found = true
		if !report.VisibleReasoningOnly {
			continue
		}
		if !report.IsRestricted {
			t.Fatal("limited proof claims full contract")
		}
		data, _ := sonic.ConfigStd.Marshal(report)
		var restored p.CombinationReport
		if err := sonic.ConfigStd.Unmarshal(data, &restored); err != nil || !restored.VisibleReasoningOnly {
			t.Fatal("persisted restriction lost", err)
		}
		plain := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{{Kind: p.ReasoningNode, Payload: p.StringValue("visible")}}}
		if issues := p.CheckCombinationRequest(plain, restored, upstream.Identity()); len(issues) > 0 {
			t.Fatal(issues)
		}
		for _, form := range []p.ReasoningForm{p.SummaryReasoning, p.StructuredReasoning} {
			unsupported := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{{Kind: p.MessageNode, Children: []p.Node{{Kind: p.ReasoningNode, ReasoningForm: form, Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("summary")}}}}}}}
			if issues := p.CheckCombinationRequest(unsupported, restored, upstream.Identity()); len(issues) != 1 || issues[0].Path != "/content/0/children/0/reasoningForm" {
				t.Fatal("profile accepted unsupported reasoning", issues)
			}
		}
		for _, check := range report.Checks {
			if check.Skipped && check.Passed {
				t.Fatal("excluded case counted as coverage")
			}
		}
	}
	if !found {
		for _, report := range reports {
			for _, check := range report.Checks {
				if !check.Passed && !check.Skipped {
					t.Logf("profile %s: %+v", report.Fidelity, check)
				}
			}
		}
		t.Fatal("visible history blocked")
	}
}

func TestBuiltinVerifiedProfilesKeepTextRoutesAndFullDiagnostics(t *testing.T) {
	t.Parallel()
	var adapters []*p.Compiled
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		adapters = append(adapters, shippedProjectionProtocol(t, name))
	}
	for _, ingress := range adapters {
		for _, upstream := range adapters {
			t.Run(ingress.Identity().DefinitionID+"/"+upstream.Identity().DefinitionID, func(t *testing.T) {
				t.Parallel()
				contract := packageMapping(upstream).Capabilities
				reports := p.VerifyBindingProfiles(t.Context(), ingress, upstream, contract)
				assertVisibleThinkingProfiles(t, upstream, reports)
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
				if packageMapping(ingress).Capabilities[p.CacheKeysCapability] && contract[p.CacheKeysCapability] && !hasCache {
					t.Fatal("joint cache key/retention evidence split into unsupported fragments")
				}
			})
		}
	}
}

func TestStrictProfilesKeepJSONSeparateFromStreamLayout(t *testing.T) {
	ingress, upstream := testCompiled(t, Responses), testCompiled(t, Gemini)
	policy := p.DefaultConversionPolicy(ingress, upstream)
	policy.Mode = "strict"
	policy.Continuation.ClientCarrier = false
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	contract := packageMapping(upstream).Capabilities
	reports := p.VerifyBindingProfiles(t.Context(), ingress, upstream, contract, conversion)
	if len(reports) < 2 || reports[0].Passed || reports[0].Transport != "" || reports[0].IsRestricted {
		t.Fatal("complete contract failure was replaced by a narrower proof")
	}
	hasJSON := false
	hasRejectedStream := false
	for _, issue := range reports[0].Issues {
		hasRejectedStream = hasRejectedStream || (issue.RuleID == "event-output-layout" && issue.Fidelity == "rejected")
	}
	for _, report := range reports {
		if !report.Passed {
			continue
		}
		if report.Transport != p.HTTPJSON || !report.IsRestricted || report.BindingHash != p.CapabilityContractHash(contract) || report.PolicyHash != conversion.Hash || report.Capabilities[p.SignaturesCapability] {
			t.Fatal("JSON proof expanded its transport, state or revision boundary", report)
		}
		for _, check := range report.Checks {
			if check.Passed && check.Direction == p.EncodeEvent {
				t.Fatal("JSON proof counted a stream fixture")
			}
		}
		encoded, err := sonic.ConfigStd.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		var restored p.CombinationReport
		if err := sonic.ConfigStd.Unmarshal(encoded, &restored); err != nil || restored.Transport != p.HTTPJSON || !restored.IsRestricted {
			t.Fatal("persisted transport restriction lost", err)
		}
		hasJSON = hasJSON || report.Capabilities[p.TextCapability] && report.Capabilities[p.UsageCapability]
	}
	if !hasJSON || !hasRejectedStream {
		t.Fatal("missing independent JSON proof or original stream rejection")
	}
}
