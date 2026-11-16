package protocol

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func scopeTestPackage(t *testing.T, predicate Expression) ProtocolPackage {
	t.Helper()
	document := verifiedTextPackage(t, "scope-fixture")
	document.Mapping.VerificationScopes = []ProtocolVerificationScope{{
		ID: "neutral-nodes", Direction: EncodeRequest, Capabilities: []Capability{TextCapability}, Transports: []Transport{HTTPJSON},
		Checks: []ProtocolScopeCheck{{ID: "node-check", Scope: "node", Path: "/payload/mime", When: predicate, Reason: "requires a known MIME"}},
	}}
	return document
}

func requireScopeIssue(t *testing.T, err error, code IssueCode, path string) {
	t.Helper()
	var failure *ConversionError
	if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue ConversionIssue) bool { return issue.Code == code && issue.Path == path }) {
		t.Fatal("unexpected request scope diagnostic", code, path, err)
	}
}

func TestVerificationScopeRejectsPrivateReferences(t *testing.T) {
	for _, pointer := range []string{"", "/native", "/source/protocol/family", "/attributes/wire:vendor", "/resources", "/children/0/payload", "/id"} {
		for _, from := range []string{"input", "root"} {
			t.Run(from+pointer, func(t *testing.T) {
				document := scopeTestPackage(t, Expression{Op: "ref", Ref: "scope-outer"})
				document.Mapping.Expressions = map[string]Expression{
					"scope-outer":   {Op: "ref", Ref: "scope-private"},
					"scope-private": {Op: "exists", Source: expressionPointer(readExpression(from, pointer))},
				}
				if _, err := document.compileContract(DefaultLimits()); err == nil {
					t.Fatal("reference exposed a non-neutral owner", from, pointer)
				}
			})
		}
	}
	for _, from := range []string{"context", "runtime", "binding", "state", "item"} {
		t.Run(from, func(t *testing.T) {
			document := scopeTestPackage(t, Expression{Op: "exists", Source: expressionPointer(readExpression(from, "/payload"))})
			if _, err := document.compileContract(DefaultLimits()); err == nil {
				t.Fatal("scope gained nonlocal read permission", from)
			}
		})
	}
	for _, operation := range []string{"with_input", "let", "map"} {
		t.Run(operation, func(t *testing.T) {
			predicate := Expression{Op: operation, Source: expressionPointer(readExpression("input", "/payload")), Body: expressionPointer(literalExpression(true))}
			if operation == "let" {
				predicate.Name = "local"
			}
			document := scopeTestPackage(t, predicate)
			if _, err := document.compileContract(DefaultLimits()); err == nil {
				t.Fatal("scope admitted input replacement or iteration", operation)
			}
		})
	}
}

func TestVerificationScopeDeclarationValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ProtocolPackage)
	}{
		{"duplicate-scope", func(document *ProtocolPackage) {
			document.Mapping.VerificationScopes = append(document.Mapping.VerificationScopes, document.Mapping.VerificationScopes[0])
		}},
		{"duplicate-check", func(document *ProtocolPackage) {
			scope := &document.Mapping.VerificationScopes[0]
			scope.Checks = append(scope.Checks, scope.Checks[0])
		}},
		{"duplicate-capability", func(document *ProtocolPackage) {
			document.Mapping.VerificationScopes[0].Capabilities = []Capability{TextCapability, TextCapability}
		}},
		{"duplicate-transport", func(document *ProtocolPackage) {
			document.Mapping.VerificationScopes[0].Transports = []Transport{HTTPJSON, HTTPJSON}
		}},
		{"undeclared-capability", func(document *ProtocolPackage) {
			document.Mapping.VerificationScopes[0].Capabilities = []Capability{ImagesCapability}
		}},
		{"wrong-direction", func(document *ProtocolPackage) { document.Mapping.VerificationScopes[0].Direction = DecodeRequest }},
		{"unsupported-transport", func(document *ProtocolPackage) { document.Mapping.VerificationScopes[0].Transports = []Transport{SSE} }},
		{"empty-checks", func(document *ProtocolPackage) { document.Mapping.VerificationScopes[0].Checks = nil }},
		{"nonboolean", func(document *ProtocolPackage) {
			document.Mapping.VerificationScopes[0].Checks[0].When = literalExpression("false")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := scopeTestPackage(t, literalExpression(false))
			test.change(&document)
			if _, err := document.compileContract(DefaultLimits()); err == nil {
				t.Fatal("invalid verification declaration compiled")
			}
		})
	}
}

func TestVerificationScopeTraversesCompleteRequest(t *testing.T) {
	isImage := Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression("image")}}
	missingMIME := Expression{Op: "not", Source: expressionPointer(Expression{Op: "present", Source: expressionPointer(readExpression("root", "/payload/mime"))})}
	compiled := compilePackageForTest(t, scopeTestPackage(t, Expression{Op: "if", When: &isImage, Then: &missingMIME, Otherwise: expressionPointer(literalExpression(false))}))
	identity := compiled.scopeIdentity(compiled.packagePlan.verificationScopes[0])
	valid := Node{Kind: ImageNode, Payload: Value{raw: `{"mime":"image/png","data":"YQ=="}`}}
	invalid := Node{Kind: ImageNode, Payload: Value{raw: `{"url":"https://example.invalid/image.png"}`}}
	for _, test := range []struct {
		name  string
		nodes []Node
		path  string
	}{
		{"valid-before-invalid", []Node{valid, invalid}, "/content/1/payload/mime"},
		{"invalid-before-valid", []Node{invalid, valid}, "/content/0/payload/mime"},
		{"nested-tool-result", []Node{{Kind: MessageNode, Children: []Node{{Kind: ToolResultNode, Children: []Node{{Kind: ToolResultNode, Children: []Node{valid, invalid}}}}}}}, "/content/0/children/0/children/0/children/1/payload/mime"},
		{"reasoning-content", []Node{{Kind: ReasoningNode, ReasoningContent: []Node{valid, invalid}}}, "/content/0/reasoningContent/1/payload/mime"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: test.nodes}
			before := fixtureValue(t, request)
			requireScopeIssue(t, compiled.CheckRequestScope(t.Context(), request, &identity, HTTPJSON), UnsupportedCapability, test.path)
			if fixtureValue(t, request) != before {
				t.Fatal("scope changed its request")
			}
		})
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: []Node{valid, {Kind: ToolResultNode, Payload: Value{raw: `{"kind":"image","payload":{"url":"opaque"}}`}}}}
	if err := compiled.CheckRequestScope(t.Context(), request, &identity, HTTPJSON); err != nil {
		t.Fatal("scope traversed an opaque payload", err)
	}
	if err := compiled.CheckRequestScope(t.Context(), &Request{Content: []Node{invalid}}, nil, HTTPJSON); err != nil {
		t.Fatal("unscoped evidence acquired a request restriction", err)
	}
}

func TestVerificationScopeRuntimeFailuresRemainErrors(t *testing.T) {
	compiled := compilePackageForTest(t, scopeTestPackage(t, literalExpression(false)))
	identity := compiled.scopeIdentity(compiled.packagePlan.verificationScopes[0])
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := compiled.CheckRequestScope(canceled, request, &identity, HTTPJSON); !errors.Is(err, context.Canceled) {
		t.Fatal("scope swallowed cancellation", err)
	}
	for _, metric := range []string{"bytes", "steps", "depth"} {
		t.Run(metric, func(t *testing.T) {
			bounded := *compiled
			large := request.Clone()
			ctx := t.Context()
			switch metric {
			case "bytes":
				bounded.limits.BufferBytes = 16
				var err error
				ctx, err = WithRequestBodyLimit(ctx, 16)
				if err != nil {
					t.Fatal(err)
				}
				large.Content[0].Payload = StringValue(strings.Repeat("x", 1024))
			case "steps":
				bounded.limits.Nodes = 1
				large.Content = make([]Node, 32)
				for index := range large.Content {
					large.Content[index] = Node{Kind: TextNode, Payload: StringValue("x")}
				}
			case "depth":
				for range semanticLimits(DefaultLimits()).Depth + 1 {
					large.Content = []Node{{Kind: MessageNode, Children: large.Content}}
				}
			}
			err := bounded.CheckRequestScope(ctx, large, &identity, HTTPJSON)
			var limit *ResourceLimitError
			var outside *requestScopeMismatch
			if !errors.As(err, &limit) || limit.Metric != metric || errors.As(err, &outside) {
				t.Fatal("scope converted resource failure to exclusion", metric, err)
			}
		})
	}
	invalid := compilePackageForTest(t, scopeTestPackage(t, readExpression("input", "/payload")))
	invalidIdentity := invalid.scopeIdentity(invalid.packagePlan.verificationScopes[0])
	err := invalid.CheckRequestScope(t.Context(), request, &invalidIdentity, HTTPJSON)
	var outside *requestScopeMismatch
	var compatibility *ConversionError
	if err == nil || errors.As(err, &outside) || errors.As(err, &compatibility) || !strings.Contains(err.Error(), "boolean") {
		t.Fatal("nonboolean actual predicate became an exclusion", err)
	}
}

func TestVerificationScopeIdentityAndEvidence(t *testing.T) {
	document := scopeTestPackage(t, Expression{Op: "ref", Ref: "neutral-predicate"})
	document.Mapping.Expressions = map[string]Expression{"neutral-predicate": literalExpression(false)}
	compiled := compilePackageForTest(t, document)
	identity := compiled.scopeIdentity(compiled.packagePlan.verificationScopes[0])
	document.Mapping.Expressions["neutral-predicate"] = literalExpression(true)
	changedReference := compilePackageForTest(t, document)
	if changedReference.Hash() == compiled.Hash() || changedReference.SamplesHash() == compiled.SamplesHash() || changedReference.scopeIdentity(changedReference.packagePlan.verificationScopes[0]).Hash == identity.Hash {
		t.Fatal("scope identity omitted its referenced predicate")
	}
	document.Mapping.VerificationScopes[0].Checks[0].Reason = "changed scope declaration"
	changedDeclaration := compilePackageForTest(t, document)
	if changedDeclaration.SamplesHash() == changedReference.SamplesHash() {
		t.Fatal("sample evidence omitted its scope declaration")
	}
	for _, alteration := range []string{"id", "hash", "revision", "boundary", "transport"} {
		t.Run(alteration, func(t *testing.T) {
			stale, transport := identity, HTTPJSON
			switch alteration {
			case "id":
				stale.ID = "unknown"
			case "hash":
				stale.Hash = "stale"
			case "revision":
				stale.OwnerRevision = "stale"
			case "boundary":
				stale.Boundary = DecodeRequest
			case "transport":
				transport = SSE
			}
			requireScopeIssue(t, compiled.CheckRequestScope(t.Context(), &Request{}, &stale, transport), VerificationRequired, "/binding/combinations")
		})
	}
	contract := CapabilitySet{TextCapability: true}
	report := CombinationReport{RequestScope: &identity, Transport: HTTPJSON, IsRestricted: true, SourceHash: compiled.Hash(), TargetHash: compiled.Hash(), CompilerVersion: CompilerVersion, SourceSamplesHash: compiled.SamplesHash(), TargetSamplesHash: compiled.SamplesHash(), BindingHash: CapabilityContractHash(contract), Capabilities: contract, Kind: OfflineVerification}
	original := report
	original.RequestScope, original.Transport, original.IsRestricted = nil, "", false
	if !compiled.HasRequestScopeEvidence(compiled, contract, []CombinationReport{original, report}) {
		t.Fatal("completed failed scope evidence was treated as missing")
	}
	if compiled.HasRequestScopeEvidence(compiled, contract, []CombinationReport{report}) {
		t.Fatal("scope-only evidence replaced the complete original report")
	}
	for _, alteration := range []string{"scope", "source-samples", "target-samples", "binding", "transport", "kind"} {
		stale := report
		switch alteration {
		case "scope":
			stale.RequestScope = nil
		case "source-samples":
			stale.SourceSamplesHash = "stale"
		case "target-samples":
			stale.TargetSamplesHash = "stale"
		case "binding":
			stale.BindingHash = "stale"
		case "transport":
			stale.Transport = SSE
		case "kind":
			stale.Kind = UpstreamVerification
		}
		if compiled.HasRequestScopeEvidence(compiled, contract, []CombinationReport{original, stale}) {
			t.Fatal("stale scope evidence counted as complete", alteration)
		}
	}
}

func TestVerificationScopeResponseImageCannotProveRequestCoverage(t *testing.T) {
	document := scopeTestPackage(t, literalExpression(false))
	document.Mapping.Capabilities[ImagesCapability] = true
	document.Mapping.VerificationScopes[0].Capabilities = []Capability{ImagesCapability}
	decode := document.Mapping.Directions[DecodeResponse]
	decode.Program.Fields["content"] = Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{
		"kind": literalExpression("message"), "role": literalExpression("assistant"),
		"children": {Op: "array", Items: []Expression{objectExpression(map[string]Expression{
			"kind": literalExpression("image"), "payload": objectExpression(map[string]Expression{"url": readExpression("input", "/reply")}),
		})}},
	})}}
	decode.Effects[0].Path = "/content/0/children/0/payload/url"
	document.Mapping.Directions[DecodeResponse] = decode
	encode := document.Mapping.Directions[EncodeResponse]
	encode.Program.Fields["reply"] = readExpression("input", "/content/0/children/0/payload/url")
	encode.Effects[0].Path = "/content/0/children/0/payload/url"
	document.Mapping.Directions[EncodeResponse] = encode
	response := Response{SchemaVersion: ProtocolCoreVersion, Status: StringValue("completed"), Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: ImageNode, Payload: Value{raw: `{"url":"https://example.invalid/image.png"}`}}}}}}
	core := fixtureValue(t, response)
	wire := Value{raw: `{"reply":"https://example.invalid/image.png"}`}
	for index := range document.Mapping.Samples {
		sample := &document.Mapping.Samples[index]
		switch sample.Direction {
		case DecodeResponse:
			sample.Input, sample.Expected = wire, core
		case EncodeResponse:
			sample.Input, sample.Expected = core, wire
		}
	}
	compiled := compilePackageForTest(t, document)
	reports := VerifyBindingProfiles(t.Context(), compiled, compiled, CapabilitySet{TextCapability: true, ImagesCapability: true})
	hasScope, hasImageResponse, hasRequestCoverageFailure := false, false, false
	for _, report := range reports {
		if report.RequestScope == nil {
			continue
		}
		hasScope = true
		if report.Passed {
			t.Fatal("successful image responses authorized requests with no image fixture", report)
		}
		for _, check := range report.Checks {
			hasImageResponse = hasImageResponse || check.Direction == EncodeResponse && check.Passed && slices.Contains(check.Capabilities, ImagesCapability)
		}
		for _, issue := range report.Issues {
			hasRequestCoverageFailure = hasRequestCoverageFailure || issue.Code == IncompleteCoverage && issue.Direction == EncodeRequest && strings.Contains(issue.Path, "/verificationScopes/")
		}
	}
	if !reports[0].Passed || !hasScope || !hasImageResponse || !hasRequestCoverageFailure {
		t.Fatal("response-image counterexample did not reach scoped request coverage", reports)
	}
}

func TestVerificationScopeHonorsRequestBodyBudget(t *testing.T) {
	compiled := compilePackageForTest(t, scopeTestPackage(t, literalExpression(false)))
	identity := compiled.scopeIdentity(compiled.packagePlan.verificationScopes[0])
	ctx, err := WithRequestBodyLimit(t.Context(), 16)
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: []Node{{Kind: TextNode, Payload: StringValue(strings.Repeat("x", 1024))}}}
	err = compiled.CheckRequestScope(ctx, request, &identity, HTTPJSON)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Metric != "bytes" {
		t.Fatal("scope ignored the pinned request budget", err)
	}
}

func TestVerificationScopePredicateFailuresAreNotSkipped(t *testing.T) {
	ingress := compilePackageForTest(t, verifiedTextPackage(t, "scope-ingress"))
	upstream := compilePackageForTest(t, scopeTestPackage(t, readExpression("input", "/payload")))
	reports := VerifyBindingProfiles(t.Context(), ingress, upstream, CapabilitySet{TextCapability: true})
	hasFailedScope := false
	for _, report := range reports {
		if report.RequestScope == nil {
			continue
		}
		hasFailedScope = true
		if report.Passed {
			t.Fatal("nonboolean predicate authorized scoped requests", report)
		}
		for _, check := range report.Checks {
			if check.Direction == EncodeRequest && (check.Passed || check.Skipped || check.Rejected) {
				t.Fatal("predicate execution failure was reclassified", check)
			}
		}
	}
	if !hasFailedScope || !reports[0].Passed || reports[0].RequestScope != nil {
		t.Fatal("scoped failure replaced independent unscoped evidence", reports)
	}
}
