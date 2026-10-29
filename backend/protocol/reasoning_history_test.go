package protocol

import (
	"slices"
	"strings"
	"testing"
)

func TestReasoningHistoryContractAndLegacyBoundary(t *testing.T) {
	for _, value := range []Value{Value{raw: "null"}, StringValue(""), StringValue("recent"), Value{raw: "true"}, Value{raw: `{"keep":"all"}`}} {
		if err := ValidateReasoningIntent(&ReasoningIntent{HistoryRetention: value}); !hasIssueCode(err, InvalidInput) {
			t.Fatal("invalid history intent was treated as absence", value, err)
		}
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Reasoning: &ReasoningIntent{HistoryRetention: StringValue("all")}}
	target := Target{coreVersion: ProtocolCoreVersion, Capabilities: CapabilitySet{ReasoningHistoryRetentionCapability: true}}
	if issues := CheckRequest(request, target, DefaultLimits()); IssuesError(issues) != nil {
		t.Fatal("declared core2 history rejected", issues)
	}
	target.Capabilities = nil
	if issues := CheckRequest(request, target, DefaultLimits()); !hasIssueCode(IssuesError(issues), UnsupportedCapability) {
		t.Fatal("history bypassed target capability", issues)
	}
	request.SchemaVersion, target.coreVersion = SemanticSchemaVersion, SemanticSchemaVersion
	target.Capabilities = CapabilitySet{ReasoningHistoryRetentionCapability: true}
	if issues := CheckRequest(request, target, DefaultLimits()); !hasIssueCode(IssuesError(issues), InvalidInput) {
		t.Fatal("core1 accepted a field its encoders could ignore", issues)
	}
	for _, value := range []string{`"all"`, `null`} {
		raw := []byte(`{"schemaVersion":1,"reasoning":{"historyRetention":` + value + `}}`)
		if parsed, err := DecodeRequestContract(raw); parsed != nil || err == nil || !strings.Contains(err.Error(), "historyRetention requires core schemaVersion 2") {
			t.Fatal("legacy contract parser accepted a core2 field", parsed, err)
		}
	}
	if _, err := decodeRequestContract([]byte(`{"schemaVersion":2,"reasoning":{"historyRetention":"all"}}`), ProtocolCoreVersion); err != nil {
		t.Fatal("versioned core2 parser rejected history retention", err)
	}
	for _, codec := range []string{"openai-chat", "responses", "anthropic", "gemini"} {
		if _, err := ReasoningParameters(request, codec); !hasIssueCode(err, UnsupportedCapability) {
			t.Fatal("legacy generation renderer consumed history retention", codec, err)
		}
	}
	legacy, err := EncodeValue(ReasoningIntent{Mode: "enabled", Budget: controlValue(4096)})
	if err != nil || legacy.raw != `{"mode":"enabled","budget":4096}` {
		t.Fatal("absent history changed existing JSON", legacy.raw, err)
	}
}

func TestReasoningHistoryCoexistsWithLegacyGeneration(t *testing.T) {
	history := &ReasoningIntent{HistoryRetention: StringValue("all")}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Reasoning: history, Parameters: Object{"reasoning_effort": StringValue("high")}}
	if err := NormalizeReasoningParameters(request, "openai-chat", false); err != nil {
		t.Fatal(err)
	}
	if request.Reasoning.Mode != "enabled" || !request.Reasoning.Effort.IsString("high") || request.Reasoning.EffortScope != "reasoning" || !request.Reasoning.HistoryRetention.IsString("all") || len(request.Parameters) != 0 {
		t.Fatal("legacy generation and independent history were not combined", request)
	}
	if history.Mode != "" || !history.Effort.IsZero() || !history.HistoryRetention.IsString("all") {
		t.Fatal("normalization mutated the previous intent owner")
	}
	request.Parameters["reasoning_effort"] = StringValue("low")
	if err := NormalizeReasoningParameters(request, "openai-chat", false); !hasIssueCode(err, InvalidInput) {
		t.Fatal("history weakened canonical generation conflict checks", err)
	}
	request.Reasoning = &ReasoningIntent{}
	if err := NormalizeReasoningParameters(request, "openai-chat", false); !hasIssueCode(err, InvalidInput) {
		t.Fatal("an old empty canonical intent changed its conflict behavior", err)
	}
}

func TestReasoningHistorySurvivesGenerationCleanup(t *testing.T) {
	conversion := reasoningTestConversion(t, "responses", "openai-chat", "compatible")
	request := &Request{SchemaVersion: ProtocolCoreVersion, Reasoning: &ReasoningIntent{Mode: "disabled", Summary: StringValue("omitted"), HistoryRetention: StringValue("all")}}
	route := ConversionContext{Model: "plain-model", Reasoning: &ReasoningCapabilities{Kind: "none"}}
	err := conversion.reasoningControls(request, route, conversion.Policy.Rules[0], nil)
	if !hasIssueCode(err, UnsupportedCapability) || request.Reasoning == nil || request.Reasoning.Mode != "" || !request.Reasoning.HistoryRetention.IsString("all") {
		t.Fatal("generation cleanup dropped unresolved history", request.Reasoning, err)
	}
	request.Reasoning.HistoryRetention = Value{}
	if err := conversion.reasoningControls(request, route, conversion.Policy.Rules[0], nil); err != nil || request.Reasoning != nil {
		t.Fatal("resolved empty intent was not released", request.Reasoning, err)
	}
}

func TestPackageHistoryRetentionEvidence(t *testing.T) {
	document := verifiedTextPackage(t, "history-retention")
	document.Mapping.Capabilities[ReasoningHistoryRetentionCapability] = true
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		contract := document.Spec.WireContracts[direction]
		contract.Schema.Properties["history"] = ValueSchema{Type: StringType, Enum: []Value{StringValue("all")}}
		contract.Schema.Required = []string{"model", "prompt", "history"}
		document.Spec.WireContracts[direction] = contract
		mapping := document.Mapping.Directions[direction]
		if direction == DecodeRequest {
			mapping.Program.Fields["reasoning"] = objectExpression(map[string]Expression{"historyRetention": readExpression("input", "/history")})
		} else {
			mapping.Program.Fields["history"] = readExpression("input", "/reasoning/historyRetention")
		}
		mapping.Effects = append(mapping.Effects, ProtocolSemanticEffect{ID: "history-retention", Kind: "emit", Path: "/reasoning/historyRetention", Target: "/history"})
		document.Mapping.Directions[direction] = mapping
	}
	var expected Request
	if err := decodeContract(document.Mapping.Samples[1].Input.Bytes(), &expected); err != nil {
		t.Fatal(err)
	}
	expected.Reasoning = &ReasoningIntent{HistoryRetention: StringValue("all")}
	core := fixtureValue(t, expected)
	wire := Value{raw: `{"model":"m","prompt":"hello","history":"all"}`}
	document.Mapping.Samples[0].Input, document.Mapping.Samples[0].Expected = wire, core
	document.Mapping.Samples[1].Input, document.Mapping.Samples[1].Expected = core, wire
	for index := range 2 {
		document.Mapping.Samples[index].Capabilities = []Capability{ReasoningHistoryRetentionCapability}
	}
	compiled := compilePackageForTest(t, document)
	report := Verify(t.Context(), compiled)
	if !report.Passed || !slices.Contains(report.Covered, ReasoningHistoryRetentionCapability) {
		t.Fatal("new capability lacks executed package evidence", report)
	}
	if err := IssuesError(CanActivate(compiled, report)); err != nil {
		t.Fatal(err)
	}
	metrics, fits := measureDocumentMetrics(t.Context(), &expected, DefaultLimits())
	if !fits || metrics.bytes != core.Len() {
		t.Fatal("generated metering omitted history retention", metrics, core.Len())
	}
	delete(document.Mapping.Capabilities, ReasoningHistoryRetentionCapability)
	for index := range 2 {
		document.Mapping.Samples[index].Capabilities = nil
	}
	withoutCapability := compilePackageForTest(t, document)
	if _, err := withoutCapability.DecodeRequest(t.Context(), wire.Bytes(), EvaluationContext{}); !hasIssueCode(err, UnsupportedCapability) {
		t.Fatal("mapping bypassed the core capability check", err)
	}
}
