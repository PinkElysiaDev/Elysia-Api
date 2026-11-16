package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// ProtocolVerificationScope declares a separately verified range of core requests.
// Checks observe every content node immediately before target request encoding.
type ProtocolVerificationScope struct {
	ID           string               `json:"id"`
	Direction    Direction            `json:"direction"`
	Capabilities []Capability         `json:"capabilities"`
	Transports   []Transport          `json:"transports"`
	Checks       []ProtocolScopeCheck `json:"checks"`
}

// ProtocolScopeCheck excludes a request when its read-only node predicate is true.
type ProtocolScopeCheck struct {
	ID     string     `json:"id"`
	Scope  string     `json:"scope"`
	Path   string     `json:"path"`
	When   Expression `json:"when"`
	Reason string     `json:"reason"`
}

// VerificationScopeIdentity binds a report to its target-owned request boundary.
type VerificationScopeIdentity struct {
	ID            string    `json:"id"`
	Hash          string    `json:"hash"`
	OwnerRevision string    `json:"ownerRevision"`
	Boundary      Direction `json:"boundary"`
}

type compiledVerificationScope struct {
	id           string
	hash         string
	capabilities []Capability
	transports   []Transport
	checks       []compiledScopeCheck
}

type compiledScopeCheck struct {
	path, reason string
	when         *compiledOutputPlan
}

type requestScopeVerificationKey struct{}

type requestScopeMismatch struct {
	identity VerificationScopeIdentity
	path     string
	reason   string
}

func (mismatch *requestScopeMismatch) Error() string {
	return fmt.Sprintf("request is outside verified scope %q at %s: %s", mismatch.identity.ID, mismatch.path, mismatch.reason)
}

func (compiled *Compiled) compileVerificationScopes(declarations []ProtocolVerificationScope, expressions *expressionCompiler) error {
	seen := make(map[string]bool, len(declarations))
	for index, declaration := range declarations {
		path := fmt.Sprintf("/mapping/verificationScopes/%d", index)
		if !definitionIdentifier.MatchString(declaration.ID) || seen[declaration.ID] {
			return fmt.Errorf("%s/id: requires a unique scope identifier", path)
		}
		seen[declaration.ID] = true
		if declaration.Direction != EncodeRequest || !compiled.Supports(EncodeRequest) || !canProfileHTTP(compiled) {
			return fmt.Errorf("%s/direction: scopes require an HTTP generation encode_request mapping", path)
		}
		if len(declaration.Capabilities) == 0 || len(declaration.Transports) == 0 || len(declaration.Checks) == 0 {
			return fmt.Errorf("%s: capabilities, transports and checks must be nonempty", path)
		}
		if len(declaration.Checks) > compiled.limits.Nodes || len(declarations) > compiled.limits.Nodes {
			return fmt.Errorf("%s: verification scope exceeds node limit", path)
		}
		for index, capability := range declaration.Capabilities {
			if !compiled.capabilities[capability] || slices.Contains(declaration.Capabilities[:index], capability) {
				return fmt.Errorf("%s/capabilities: requires unique declared capabilities", path)
			}
		}
		for index, transport := range declaration.Transports {
			if transport != HTTPJSON && transport != SSE && transport != NDJSON || slices.Contains(declaration.Transports[:index], transport) || !hasGenerationTransport(compiled, transport) {
				return fmt.Errorf("%s/transports: requires unique generation transports", path)
			}
		}
		scope, err := compileVerificationScope(declaration, expressions, path)
		if err != nil {
			return err
		}
		compiled.packagePlan.verificationScopes = append(compiled.packagePlan.verificationScopes, scope)
	}
	return nil
}

func hasGenerationTransport(compiled *Compiled, transport Transport) bool {
	for _, operation := range compiled.operations {
		if operation.Kind == "generate" && operation.Transport == transport {
			return true
		}
	}
	return false
}

func compileVerificationScope(declaration ProtocolVerificationScope, expressions *expressionCompiler, path string) (compiledVerificationScope, error) {
	scope := compiledVerificationScope{id: declaration.ID, capabilities: slices.Clone(declaration.Capabilities), transports: slices.Clone(declaration.Transports)}
	parentReferences := expressions.mappingReferences
	references := map[string]Expression{}
	expressions.mappingReferences = references
	defer func() { expressions.mappingReferences = parentReferences }()
	seen := make(map[string]bool, len(declaration.Checks))
	for index, check := range declaration.Checks {
		at := fmt.Sprintf("%s/checks/%d", path, index)
		if !definitionIdentifier.MatchString(check.ID) || seen[check.ID] || check.Scope != nodeScope || strings.TrimSpace(check.Reason) == "" {
			return scope, fmt.Errorf("%s: requires a unique check id, node scope and reason", at)
		}
		seen[check.ID] = true
		if _, err := parsePointer(check.Path); err != nil {
			return scope, fmt.Errorf("%s/path: %w", at, err)
		}
		plan, err := expressions.compilePlan(check.When, at+"/when", expressionScope{coreInput: reflect.TypeFor[Node]()}, 1)
		if err != nil {
			return scope, err
		}
		if !isAssignable(plan.expression.result, BooleanType) {
			return scope, fmt.Errorf("%s/when: scope condition must produce a boolean", at)
		}
		if err := checkScopeExpression(plan.expression, map[*compiledExpression]bool{}); err != nil {
			return scope, err
		}
		scope.checks = append(scope.checks, compiledScopeCheck{check.Path, check.Reason, plan})
	}
	identity, err := EncodeValue(struct {
		CoreVersion int                       `json:"coreVersion"`
		Scope       ProtocolVerificationScope `json:"scope"`
		Expressions map[string]Expression     `json:"expressions"`
	}{ProtocolCoreVersion, declaration, references})
	if err != nil {
		return scope, err
	}
	scope.hash = hashValue(identity)
	return scope, nil
}

// Scope predicates cannot inspect provenance or change input scope. An ancestor
// read would expose hidden owners too, so reads select explicit semantic fields.
func checkScopeExpression(expression *compiledExpression, seen map[*compiledExpression]bool) error {
	if expression == nil || seen[expression] {
		return nil
	}
	seen[expression] = true
	switch expression.op {
	case "read":
		if (expression.from != "input" && expression.from != "root") || len(expression.pointer) == 0 || !slices.Contains([]string{"kind", "role", "payload", "status", "reasoningForm"}, expression.pointer[0]) {
			return fmt.Errorf("%s: scope reads require explicit neutral node fields", expression.path)
		}
	case "literal", "if", "choose", "enum", "exists", "present", "type", "is_integer", "is_integral", "not", "equal", "equal_text", "all", "any", "length", "has_prefix", "contains", "has_control", "matches_charset":
	default:
		return fmt.Errorf("%s: operation %q is not a read-only scope predicate", expression.path, expression.op)
	}
	for _, child := range []*compiledExpression{expression.source, expression.body, expression.when, expression.then, expression.otherwise} {
		if err := checkScopeExpression(child, seen); err != nil {
			return err
		}
	}
	for _, child := range expression.items {
		if err := checkScopeExpression(child, seen); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(expression.cases) {
		if err := checkScopeExpression(expression.cases[name], seen); err != nil {
			return err
		}
	}
	return nil
}

func (compiled *Compiled) scopeIdentity(scope compiledVerificationScope) VerificationScopeIdentity {
	return VerificationScopeIdentity{ID: scope.id, Hash: scope.hash, OwnerRevision: compiled.hash, Boundary: EncodeRequest}
}

func (compiled *Compiled) checkVerificationScope(ctx context.Context, request *Request, scope compiledVerificationScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	inputLimits, _, workLimits := compiled.mappingLimits(ctx, EncodeRequest)
	if err := checkSemanticValue(request, inputLimits); err != nil {
		return err
	}
	state := evaluation{ctx: ctx, valueReader: &valueReader{}, budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	return visitControlNodes(state, request, func(node *Node, path string) error {
		read := func(pointer []string) (JSONOutput, error) { return readCoreNode(*node, pointer, nil) }
		local := state
		local.inputReader, local.rootReader = read, read
		for _, check := range scope.checks {
			condition, err := check.when.evaluate(local)
			if err != nil {
				return err
			}
			isOutside, err := readBoolean(condition)
			if err != nil {
				return fmt.Errorf("scope %q condition must produce a boolean: %w", scope.id, err)
			}
			if isOutside {
				return &requestScopeMismatch{compiled.scopeIdentity(scope), path + check.path, check.reason}
			}
		}
		return nil
	})
}

// CheckRequestScope checks the complete prepared request against pinned evidence.
// Evaluation failures remain errors; they never mean that a fixture was excluded.
func (compiled *Compiled) CheckRequestScope(ctx context.Context, request *Request, identity *VerificationScopeIdentity, transport Transport) error {
	if identity == nil {
		return nil
	}
	for _, scope := range compiled.packagePlan.verificationScopes {
		if compiled.scopeIdentity(scope) != *identity || !slices.Contains(scope.transports, transport) {
			continue
		}
		err := compiled.checkVerificationScope(ctx, request, scope)
		var outside *requestScopeMismatch
		if errors.As(err, &outside) {
			return &ConversionError{Issues: []ConversionIssue{{Code: UnsupportedCapability, Severity: SeverityError, Protocol: compiled.identity, Direction: EncodeRequest, Stage: "binding", Path: outside.path, Reason: outside.Error()}}}
		}
		return err
	}
	return &ConversionError{Issues: []ConversionIssue{{Code: VerificationRequired, Severity: SeverityError, Protocol: compiled.identity, Direction: EncodeRequest, Stage: "binding", Path: "/binding/combinations", Reason: "request scope evidence does not match the pinned target revision and transport"}}}
}

func supportsScopeCapabilities(capabilities CapabilitySet, scope compiledVerificationScope) bool {
	return !slices.ContainsFunc(scope.capabilities, func(capability Capability) bool { return !capabilities[capability] })
}

// HasRequestScopeEvidence requires the original full report and every applicable
// range's replay. Failed reports are current evidence, not permission.
func (compiled *Compiled) HasRequestScopeEvidence(ingress *Compiled, capabilities CapabilitySet, reports []CombinationReport) bool {
	contractHash := CapabilityContractHash(capabilities)
	isCurrent := func(report CombinationReport) bool {
		return report.SourceHash == ingress.hash && report.TargetHash == compiled.hash && report.CompilerVersion == CompilerVersion && report.SourceSamplesHash == ingress.samplesHash && report.TargetSamplesHash == compiled.samplesHash && report.BindingHash == contractHash && report.Kind == OfflineVerification
	}
	if !slices.ContainsFunc(reports, func(report CombinationReport) bool {
		return isCurrent(report) && report.RequestScope == nil && !report.IsRestricted && !report.VisibleReasoningOnly && report.Transport == "" && CapabilityContractHash(report.Capabilities) == contractHash
	}) {
		return false
	}
	for _, scope := range compiled.packagePlan.verificationScopes {
		if !supportsScopeCapabilities(capabilities, scope) || !supportsScopeCapabilities(ingress.capabilities, scope) {
			continue
		}
		identity := compiled.scopeIdentity(scope)
		for _, transport := range scope.transports {
			if !hasGenerationTransport(ingress, transport) {
				continue
			}
			if !slices.ContainsFunc(reports, func(report CombinationReport) bool {
				return isCurrent(report) && report.RequestScope != nil && *report.RequestScope == identity && report.Transport == transport
			}) {
				return false
			}
		}
	}
	return true
}
