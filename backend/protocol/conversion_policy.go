package protocol

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"slices"
	"sort"
)

type ConversionPhase string

const (
	ConversionIngress     ConversionPhase = "ingress"
	ConversionRequest     ConversionPhase = "request"
	ConversionResponse    ConversionPhase = "response"
	ConversionEvent       ConversionPhase = "event"
	ConversionSourceEvent ConversionPhase = "source_event"
	ConversionWire        ConversionPhase = "wire"
)

// ConversionPolicy is separate from the immutable wire definition. Rules never
// grant model capabilities, credentials, provenance or executable code.
type ConversionPolicy struct {
	SchemaVersion int                      `json:"schemaVersion"`
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Mode          string                   `json:"mode,omitempty"`
	Limits        *Limits                  `json:"limits,omitempty"`
	Rules         []ConversionRule         `json:"rules"`
	Continuation  *ContinuationSettings    `json:"continuation,omitempty"`
	Usage         *ConversionUsageSettings `json:"usage,omitempty"`
}

type ContinuationSettings struct {
	ClientCarrier    bool  `json:"clientCarrier"`
	Persist          bool  `json:"persist"`
	RetentionSeconds int64 `json:"retentionSeconds"`
	TurnsPerSession  int   `json:"turnsPerSession"`
	MaxBytes         int64 `json:"maxBytes"`
	RecordBytes      int   `json:"recordBytes"`
}

type ConversionUsageSettings struct {
	DefaultIncludeUsage  bool `json:"defaultIncludeUsage"`
	CollectUpstreamUsage bool `json:"collectUpstreamUsage"`
}

type ConversionRule struct {
	ID         string          `json:"id"`
	Order      int             `json:"order"`
	Enabled    bool            `json:"enabled"`
	Phase      ConversionPhase `json:"phase"`
	Match      ConversionMatch `json:"match"`
	Action     string          `json:"action"`
	Path       string          `json:"path,omitempty"`
	Value      Value           `json:"value,omitzero"`
	Expression *Expression     `json:"expression,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

type ConversionMatch struct {
	SourceID     string    `json:"sourceId,omitempty"`
	TargetID     string    `json:"targetId,omitempty"`
	SourceFamily string    `json:"sourceFamily,omitempty"`
	TargetFamily string    `json:"targetFamily,omitempty"`
	SourceWire   string    `json:"sourceWire,omitempty"`
	TargetWire   string    `json:"targetWire,omitempty"`
	Model        string    `json:"model,omitempty"`
	Operation    string    `json:"operation,omitempty"`
	Transport    Transport `json:"transport,omitempty"`
	NodeKind     NodeKind  `json:"nodeKind,omitempty"`
	Path         string    `json:"path,omitempty"`
	Present      *bool     `json:"present,omitempty"`
}

type ConversionSelection struct {
	PolicyID     string            `json:"policyId,omitempty"`
	RevisionHash string            `json:"revisionHash,omitempty"`
	Overrides    *ConversionPolicy `json:"overrides,omitempty"`
}

type ConversionContext struct {
	ResponseModel       *ResponseModelOrigin   `json:"responseModel,omitempty"`
	InterleavedThinking bool                   `json:"interleavedThinking,omitempty"`
	Delivery            *DeliveryState         `json:"-"`
	Scope               Scope                  `json:"-"`
	Recoverable         map[string]bool        `json:"-"`
	Source              Identity               `json:"source"`
	Target              Identity               `json:"target"`
	Model               string                 `json:"model"`
	Operation           string                 `json:"operation"`
	Transport           Transport              `json:"transport"`
	Reasoning           *ReasoningCapabilities `json:"reasoning,omitempty"`
}

type CompiledConversion struct {
	VerifiedProviderRules map[string]bool    `json:"-"`
	VerificationContext   *ConversionContext `json:"-"`
	Policy                ConversionPolicy   `json:"policy"`
	Hash                  string             `json:"hash"`
	Origins               map[string]string  `json:"origins"`
	RuleRevisions         map[string]string  `json:"ruleRevisions"`
	expressions           map[string]*compiledExpression
	limits                Limits
}

// ResolveConversion applies complete rule replacement by stable ID; no implicit
// concatenation or duplicate execution. Equal order in a phase is ambiguous.
func ResolveConversion(layers ...ConversionPolicy) (*CompiledConversion, error) {
	effective := ConversionPolicy{SchemaVersion: 1, ID: "resolved", Name: "Resolved", Mode: "compatible", Rules: []ConversionRule{}}
	rules := map[string]ConversionRule{}
	origins := map[string]string{}
	revisions := map[string]string{}
	for _, layer := range layers {
		if layer.SchemaVersion != 1 {
			return nil, fmt.Errorf("conversion schemaVersion must be 1")
		}
		if layer.Mode != "" {
			effective.Mode = layer.Mode
		}
		if layer.Limits != nil {
			l := *layer.Limits
			effective.Limits = &l
		}
		if layer.Continuation != nil {
			v := *layer.Continuation
			effective.Continuation = &v
		}
		if layer.Usage != nil {
			v := *layer.Usage
			effective.Usage = &v
		}
		layerValue, _ := EncodeValue(layer)
		seen := map[string]bool{}
		for _, rule := range layer.Rules {
			if seen[rule.ID] {
				return nil, fmt.Errorf("duplicate conversion rule %q", rule.ID)
			}
			seen[rule.ID] = true
			rules[rule.ID] = rule
			origins[rule.ID] = layer.ID
			revisions[rule.ID] = hashValue(layerValue)
		}
	}
	for _, rule := range rules {
		effective.Rules = append(effective.Rules, rule)
	}
	sort.Slice(effective.Rules, func(i, j int) bool {
		if effective.Rules[i].Order == effective.Rules[j].Order {
			return effective.Rules[i].ID < effective.Rules[j].ID
		}
		return effective.Rules[i].Order < effective.Rules[j].Order
	})
	c, err := CompileConversion(effective)
	if err != nil {
		return nil, err
	}
	c.Origins = origins
	c.RuleRevisions = revisions
	return c, nil
}

func CompileConversion(p ConversionPolicy) (*CompiledConversion, error) {
	if p.SchemaVersion != 1 || !definitionIdentifier.MatchString(p.ID) {
		return nil, fmt.Errorf("invalid conversion policy schema or id")
	}
	if p.Mode == "" {
		p.Mode = "compatible"
	}
	if p.Mode != "compatible" && p.Mode != "strict" {
		return nil, fmt.Errorf("conversion mode must be compatible or strict")
	}
	limits := DefaultLimits()
	if p.Limits != nil {
		limits = *p.Limits
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	cap := DefaultLimits()
	if limits.Depth > cap.Depth || limits.Nodes > cap.Nodes || limits.Mutations > cap.Mutations || limits.StateItems > cap.StateItems || limits.BufferBytes > cap.BufferBytes {
		return nil, fmt.Errorf("conversion limits cannot exceed engine limits")
	}
	if len(p.Rules) > limits.Mutations {
		return nil, fmt.Errorf("conversion rule limit exceeded")
	}
	if s := p.Continuation; s != nil && (s.RetentionSeconds <= 0 || s.RetentionSeconds > 365*24*3600 || s.TurnsPerSession <= 0 || s.MaxBytes <= 0 || s.RecordBytes <= 0 || s.RecordBytes > limits.BufferBytes) {
		return nil, fmt.Errorf("invalid continuation retention or limits")
	}
	c := &CompiledConversion{Policy: p, limits: limits, expressions: map[string]*compiledExpression{}, Origins: map[string]string{}}
	compiler := &expressionCompiler{limits: limits, references: map[string]Expression{}, resolving: map[string]bool{}, used: map[string]bool{}}
	actions := map[string]conversionAction{}
	for _, action := range conversionActionCatalog() {
		actions[action.name] = action
	}
	seen, orders := map[string]bool{}, map[string]string{}
	for _, r := range p.Rules {
		if !definitionIdentifier.MatchString(r.ID) || seen[r.ID] {
			return nil, fmt.Errorf("invalid or duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if !slices.Contains(conversionPhases(), r.Phase) {
			return nil, fmt.Errorf("unknown conversion phase %q", r.Phase)
		}
		key := fmt.Sprintf("%s:%d", r.Phase, r.Order)
		if r.Enabled && orders[key] != "" {
			return nil, fmt.Errorf("ambiguous conversion rule order %s: %q and %q", key, orders[key], r.ID)
		}
		if r.Enabled {
			orders[key] = r.ID
		}
		action, exists := actions[r.Action]
		if !exists {
			return nil, fmt.Errorf("unknown conversion action %q", r.Action)
		}
		if err := action.validate(r); err != nil {
			return nil, err
		}
		if r.Match.NodeKind != "" {
			if r.Phase == ConversionWire {
				return nil, fmt.Errorf("node conditions require a semantic phase")
			}
			if !slices.Contains([]NodeKind{MessageNode, TextNode, ImageNode, AudioNode, VideoNode, DocumentNode, ReasoningNode, RefusalNode, ToolCallNode, ToolResultNode, OpaqueNode}, r.Match.NodeKind) {
				return nil, fmt.Errorf("unknown conversion node kind %q", r.Match.NodeKind)
			}
		}
		if r.Match.Transport != "" && !slices.Contains(TransportCatalog(), r.Match.Transport) {
			return nil, fmt.Errorf("unknown conversion transport %q", r.Match.Transport)
		}
		for _, ptr := range []string{r.Path, r.Match.Path} {
			if _, err := parsePointer(ptr); err != nil {
				return nil, err
			}
		}
		if r.Match.Model != "" {
			if _, err := path.Match(r.Match.Model, ""); err != nil {
				return nil, err
			}
		}
		if r.Action == "set" && r.Value.IsZero() {
			return nil, fmt.Errorf("set rule %s requires value", r.ID)
		}
		if r.Action == "transform" {
			if r.Expression == nil {
				return nil, fmt.Errorf("transform rule %s requires expression", r.ID)
			}
			e, err := compiler.compile(*r.Expression, "/rules/"+r.ID+"/expression", expressionScope{}, 1)
			if err != nil {
				return nil, err
			}
			c.expressions[r.ID] = e
		}
	}
	sort.SliceStable(c.Policy.Rules, func(i, j int) bool { return c.Policy.Rules[i].Order < c.Policy.Rules[j].Order })
	value, err := EncodeValue(c.Policy)
	if err != nil {
		return nil, err
	}
	c.Hash = hashValue(value)
	return c, nil
}

func (m ConversionMatch) matches(ctx ConversionContext, input Value) bool {
	for _, pair := range [][2]string{{m.SourceID, ctx.Source.DefinitionID}, {m.TargetID, ctx.Target.DefinitionID}, {m.SourceFamily, ctx.Source.Family}, {m.TargetFamily, ctx.Target.Family}, {m.SourceWire, ctx.Source.WireVersion}, {m.TargetWire, ctx.Target.WireVersion}, {m.Operation, ctx.Operation}, {string(m.Transport), string(ctx.Transport)}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	if m.Model != "" {
		ok, _ := path.Match(m.Model, ctx.Model)
		if !ok {
			return false
		}
	}
	if m.Present != nil {
		ptr, _ := parsePointer(m.Path)
		v, err := readValuePointer(input, ptr)
		if err != nil || (!v.IsZero()) != *m.Present {
			return false
		}
	}
	return true
}

func (m ConversionMatch) MatchesContext(ctx ConversionContext) bool { return m.matches(ctx, Value{}) }

func (c *CompiledConversion) SignatureProjectionEnabled(phase ConversionPhase, route ConversionContext) bool {
	if c == nil {
		return false
	}
	for _, r := range c.Policy.Rules {
		match := r.Match
		match.Present = nil
		match.Path = ""
		if r.Enabled && r.Phase == phase && r.Action == "signatures" && match.matches(route, Value{}) {
			return true
		}
	}
	return false
}

func (c *CompiledConversion) issue(rule ConversionRule, phase ConversionPhase, ctx ConversionContext, at, reason string, sink *DiagnosticSink, lossy bool) error {
	return c.issueSeverity(rule, phase, ctx, at, reason, sink, lossy, SeverityWarning)
}

func (c *CompiledConversion) issueSeverity(rule ConversionRule, phase ConversionPhase, ctx ConversionContext, at, reason string, sink *DiagnosticSink, lossy bool, severity Severity) error {
	if rule.Action == "reject" || (lossy && c.Policy.Mode == "strict") {
		severity = SeverityError
	}
	fidelity := "preserved"
	if lossy {
		fidelity = "lossy_compatible"
	} else if rule.Action == "signatures" {
		fidelity = "recoverable_wrapped"
	}
	issue := ConversionIssue{Fidelity: fidelity, Code: ConversionDegraded, Severity: severity, Protocol: ctx.Target, Stage: "conversion." + string(phase), Path: at, Reason: reason, RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID]}
	if phase == ConversionRequest {
		issue.Direction = EncodeRequest
	}
	if phase == ConversionSourceEvent {
		issue.Protocol, issue.Direction = ctx.Source, DecodeEvent
	}
	if rule.Action == "chat_response_model" {
		issue.Protocol, issue.Direction = ctx.Source, DecodeResponse
	}
	if severity == SeverityError {
		issue.Code = ConversionRejected
		issue.Fidelity = "rejected"
		failure := IssuesError([]ConversionIssue{issue})
		if rule.Action != "reject" && phase == ConversionRequest {
			return candidateFailure(failure)
		}
		return failure
	}
	sink.Add(issue)
	return nil
}

// ConversionHash returns the resolved policy hash, or empty for no policy.
func ConversionHash(c *CompiledConversion) string {
	if c == nil {
		return ""
	}
	return c.Hash
}

func (c *CompiledConversion) HasPhase(phase ConversionPhase) bool {
	if c == nil {
		return false
	}
	for _, r := range c.Policy.Rules {
		if r.Enabled && r.Phase == phase {
			return true
		}
	}
	return false
}

func (c *CompiledConversion) Wire(ctx context.Context, body []byte, route ConversionContext, sink *DiagnosticSink) ([]byte, error) {
	if !c.HasPhase(ConversionWire) {
		return body, nil
	}
	value, err := ParseValue(body)
	if err != nil {
		return nil, err
	}
	value, err = c.ApplyValue(ctx, ConversionWire, value, route, sink)
	return value.Bytes(), err
}

func (c *CompiledConversion) WithVerificationContext(route ConversionContext) *CompiledConversion {
	// Observed response fields belong to a single attempt, never a binding proof.
	route.ResponseModel = nil
	if route.Reasoning != nil {
		capability := *route.Reasoning
		capability.Levels = slices.Clone(capability.Levels)
		capability.DisabledLevels = slices.Clone(capability.DisabledLevels)
		capability.BetweenToolsLevels = slices.Clone(capability.BetweenToolsLevels)
		route.Reasoning = &capability
	}
	copy := *c
	copy.VerificationContext = &route
	return &copy
}
func (c *CompiledConversion) ContextHash() string {
	if c == nil || c.VerificationContext == nil {
		return ""
	}
	value, _ := EncodeValue(c.VerificationContext)
	return hashValue(value)
}
func (c *CompiledConversion) VerificationRoute(source, target Identity, transport Transport) ConversionContext {
	created, _ := EncodeValue(1)
	route := ConversionContext{Source: source, Target: target, Transport: transport, Delivery: &DeliveryState{ID: StringValue("response_verification"), Created: created}}
	if c != nil && c.VerificationContext != nil {
		route.Reasoning = c.VerificationContext.Reasoning
		route.InterleavedThinking = c.VerificationContext.InterleavedThinking
		route.Model = c.VerificationContext.Model
		route.Operation = c.VerificationContext.Operation
		route.Transport = c.VerificationContext.Transport
	}
	return route
}
func (c *CompiledConversion) ContextDependent() bool {
	for _, r := range c.Policy.Rules {
		if r.Enabled && (r.Action == "reasoning_controls" || r.Action == "provider_signature" || r.Match.Model != "" || r.Match.Operation != "" || r.Match.Transport != "") {
			return true
		}
	}
	return false
}

// Compatibility values are explicit configuration, never engine defaults.
func ProviderSignatureValue(value Value) (Value, error) {
	var plain string
	if value.Decode(&plain) == nil && plain != "" {
		return StringValue(plain), nil
	}
	var input struct {
		Value    string `json:"value"`
		Encoding string `json:"encoding"`
	}
	if err := decodeContract(value.Bytes(), &input); err != nil || input.Value == "" {
		return Value{}, fmt.Errorf("provider_signature requires a nonempty value and raw/base64 encoding")
	}
	switch input.Encoding {
	case "raw":
		return StringValue(input.Value), nil
	case "base64":
		return StringValue(base64.StdEncoding.EncodeToString([]byte(input.Value))), nil
	}
	return Value{}, fmt.Errorf("provider_signature encoding must be raw or base64")
}
