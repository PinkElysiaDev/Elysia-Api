package protocol

import (
	"strings"
	"testing"
)

func tokenCountingPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := verifiedTextPackage(t, "counting")
	document.Spec.Documents["count-result"] = WireContractSpec{Schema: &ValueSchema{Type: ObjectType, Required: []string{"tokens"}, Properties: map[string]ValueSchema{"tokens": {Type: IntegerType}}}}
	request := document.Mapping.Directions[DecodeRequest].Document
	document.Spec.Operations["count"] = ProtocolOperationSpec{Kind: "count_tokens", Method: "POST", Path: "/count", Transport: HTTPJSON,
		Request: request, Response: "count-result", Auth: Credential{Location: "none"}}
	encoder := document.Mapping.Directions[EncodeRequest]
	encoder.Document = request
	document.Mapping.Operations["count"] = ProtocolOperationMapping{CountTokens: &ProtocolTokenCountMapping{
		Request:        map[Direction]ProtocolDirectionMapping{DecodeRequest: document.Mapping.Directions[DecodeRequest], EncodeRequest: encoder},
		DecodeResponse: objectExpression(map[string]Expression{"inputTokens": readExpression("input", "/tokens")}),
		EncodeResponse: objectExpression(map[string]Expression{"tokens": readExpression("input", "/inputTokens")}),
	}}
	for _, sample := range document.Mapping.Samples[:2] {
		sample.ID, sample.Operation = "count."+sample.ID, "count"
		document.Mapping.TokenCountSamples = append(document.Mapping.TokenCountSamples, sample)
	}
	document.Mapping.TokenCountSamples = append(document.Mapping.TokenCountSamples,
		Sample{ID: "count.decode", Operation: "count", Direction: DecodeResponse, Input: Value{raw: `{"tokens":9007199254740993}`}, Expected: Value{raw: `{"inputTokens":9007199254740993}`}},
		Sample{ID: "count.encode", Operation: "count", Direction: EncodeResponse, Input: Value{raw: `{"inputTokens":9007199254740993}`}, Expected: Value{raw: `{"tokens":9007199254740993}`}},
	)
	return document
}

func TestTokenCountingEvidenceAndPreview(t *testing.T) {
	document := tokenCountingPackage(t)
	compiled := compilePackageForTest(t, document)
	report := Verify(t.Context(), compiled)
	if !report.Passed || len(report.Checks) != 8 {
		t.Fatal(report)
	}
	request, ok := compiled.TokenCountRequestProtocol("count")
	if !ok || request.Hash() != compiled.Hash() || request.Identity() != compiled.Identity() {
		t.Fatal("count request lost its pinned package revision")
	}
	if _, ok := compiled.TokenCountRequestProtocol("generate"); ok {
		t.Fatal("generation became an implicit count operation")
	}
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{compiler: compiler}
	for _, sample := range document.Mapping.TokenCountSamples {
		preview := service.PreviewWorkflow(t.Context(), PreviewInput{Definition: fixtureValue(t, document), Mode: "count_tokens", Operation: "count", Direction: sample.Direction, Input: sample.Input})
		expected := sample.Expected
		output := preview.Output
		if sample.Direction == DecodeRequest {
			var request Request
			if err := output.Decode(&request); err != nil || request.SchemaVersion != ProtocolCoreVersion {
				t.Fatal("preview lost the request contract", preview)
			}
			output, err = comparableSemantic(&request)
			if err != nil {
				t.Fatal(err)
			}
			expected, err = comparableExpected(compiled, sample)
			if err != nil {
				t.Fatal(err)
			}
		}
		if IssuesError(preview.Issues) != nil || !equalValues(output, expected) {
			t.Fatal(sample.ID, preview)
		}
	}
	document.Mapping.TokenCountSamples = document.Mapping.TokenCountSamples[:3]
	changed := compilePackageForTest(t, document)
	if Verify(t.Context(), changed).Passed || changed.SamplesHash() == compiled.SamplesHash() || IssuesError(CanActivate(changed, report)) == nil {
		t.Fatal("missing count evidence reused generation verification")
	}
}

func TestTokenCountingOnlyAndInvalidResults(t *testing.T) {
	document := tokenCountingPackage(t)
	delete(document.Spec.Operations, "generate")
	delete(document.Mapping.Operations, "generate")
	document.Mapping.Directions, document.Mapping.Samples = nil, nil
	compiled := compilePackageForTest(t, document)
	report := Verify(t.Context(), compiled)
	if !report.Passed || compiled.Supports(DecodeRequest) || len(report.Covered) != 1 {
		t.Fatal("count evidence claimed generation", report)
	}
	for _, raw := range []string{`{}`, `{"tokens":null}`, `{"tokens":-1}`, `{"tokens":1.5}`, `{"tokens":9223372036854775808}`, `{"tokens":1,"unknown":2}`} {
		if _, err := compiled.DecodeTokenCount(t.Context(), "count", Value{raw: raw}); err == nil {
			t.Fatal("invalid provider count accepted", raw)
		}
	}
	result, err := compiled.DecodeTokenCount(t.Context(), "count", Value{raw: `{"tokens":0}`})
	if err != nil || result.InputTokens != 0 {
		t.Fatal("zero is a valid observed count", result, err)
	}
}

func TestTokenCountingRejectsInvalidContracts(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*ProtocolPackage)
	}{
		{"missing-result", func(d *ProtocolPackage) {
			op := d.Spec.Operations["count"]
			op.Response = ""
			d.Spec.Operations["count"] = op
		}},
		{"stream", func(d *ProtocolPackage) {
			op := d.Spec.Operations["count"]
			op.Transport = SSE
			d.Spec.Operations["count"] = op
		}},
		{"missing-encoder", func(d *ProtocolPackage) { delete(d.Mapping.Operations["count"].CountTokens.Request, EncodeRequest) }},
		{"wrong-kind", func(d *ProtocolPackage) {
			op := d.Spec.Operations["count"]
			op.Kind = "generate"
			d.Spec.Operations["count"] = op
		}},
		{"mixed-binding", func(d *ProtocolPackage) {
			op := d.Mapping.Operations["count"]
			op.Request = DecodeRequest
			d.Mapping.Operations["count"] = op
		}},
		{"unproved-emit", func(d *ProtocolPackage) {
			request := d.Mapping.Operations["count"].CountTokens.Request
			entry := request[EncodeRequest]
			entry.Effects = append(entry.Effects, ProtocolSemanticEffect{ID: "false-proof", Kind: "emit", Path: "/model", Target: "/prompt"})
			request[EncodeRequest] = entry
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := tokenCountingPackage(t)
			fixture.edit(&document)
			compiler, err := NewCompiler(DefaultLimits(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
			if len(issues) == 0 {
				t.Fatal("invalid counting contract accepted")
			}
			if fixture.name == "unproved-emit" && !strings.Contains(issues[0].Reason, "provable") {
				t.Fatal(issues)
			}
		})
	}
}
