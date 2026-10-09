package protocol

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

type ConversionPhase string

const (
	ConversionIngress  ConversionPhase = "ingress"
	ConversionRequest  ConversionPhase = "request"
	ConversionResponse ConversionPhase = "response"
	ConversionEvent    ConversionPhase = "event"
	ConversionWire     ConversionPhase = "wire"
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
	Delivery    *DeliveryState  `json:"-"`
	Scope       Scope           `json:"-"`
	Recoverable map[string]bool `json:"-"`
	Source      Identity        `json:"source"`
	Target      Identity        `json:"target"`
	Model       string          `json:"model"`
	Operation   string          `json:"operation"`
	Transport   Transport       `json:"transport"`
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
	seen, orders := map[string]bool{}, map[string]string{}
	for _, r := range p.Rules {
		if !definitionIdentifier.MatchString(r.ID) || seen[r.ID] {
			return nil, fmt.Errorf("invalid or duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if !slices.Contains([]ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent, ConversionWire}, r.Phase) {
			return nil, fmt.Errorf("unknown conversion phase %q", r.Phase)
		}
		key := fmt.Sprintf("%s:%d", r.Phase, r.Order)
		if r.Enabled && orders[key] != "" {
			return nil, fmt.Errorf("ambiguous conversion rule order %s: %q and %q", key, orders[key], r.ID)
		}
		if r.Enabled {
			orders[key] = r.ID
		}
		if !slices.Contains([]string{"set", "remove", "transform", "warn", "reject", "signatures", "stream_options", "tool_result_object", "tool_result_text", "system_instruction_hoist", "buffer_node", "provider_signature", "responses_include", "responses_context", "usage_projection", "response_metadata", "response_shape", "response_envelope", "responses_storage", "anthropic_usage_envelope"}, r.Action) {
			return nil, fmt.Errorf("unknown conversion action %q", r.Action)
		}
		if (r.Action == "response_metadata" || r.Action == "response_shape" || r.Action == "response_envelope" || r.Action == "usage_projection" || r.Action == "anthropic_usage_envelope") && r.Phase != ConversionResponse && r.Phase != ConversionEvent && !(r.Action == "response_metadata" && r.Phase == ConversionRequest) {
			return nil, fmt.Errorf("%s requires response or event phase", r.Action)
		}
		if r.Action == "response_metadata" || r.Action == "response_shape" || r.Action == "response_envelope" || r.Action == "usage_projection" || r.Action == "responses_include" || r.Action == "responses_context" {
			var codec string
			if r.Value.Decode(&codec) != nil || !knownConversionCodec(codec) {
				return nil, fmt.Errorf("%s requires a known target codec in value", r.Action)
			}
		}
		if r.Action == "responses_storage" {
			var opts responsesStorageOptions
			if r.Value.Decode(&opts) != nil || !knownConversionCodec(opts.TargetCodec) || (opts.OnUnsupported != "degrade" && opts.OnUnsupported != "reject") {
				return nil, fmt.Errorf("responses_storage requires targetCodec and onUnsupported: degrade or reject")
			}
		}
		if r.Action == "system_instruction_hoist" && r.Phase != ConversionRequest {
			return nil, fmt.Errorf("system_instruction_hoist requires request phase")
		}
		if r.Action == "provider_signature" {
			if r.Phase != ConversionRequest || r.Match.NodeKind != ToolCallNode || r.Match.TargetFamily == "" {
				return nil, fmt.Errorf("provider_signature requires a request tool_call rule with an explicit target family")
			}
			if _, err := ProviderSignatureValue(r.Value); err != nil {
				return nil, err
			}
		}
		if (r.Action == "tool_result_object" || r.Action == "tool_result_text") && (r.Phase != ConversionRequest || r.Match.NodeKind != ToolResultNode) {
			return nil, fmt.Errorf("tool_result_object requires request tool_result nodes")
		}
		if r.Action == "buffer_node" && r.Phase != ConversionEvent {
			return nil, fmt.Errorf("buffer_node requires event phase")
		}
		if (r.Action == "stream_options" || r.Action == "responses_include" || r.Action == "responses_context" || r.Action == "responses_storage") && r.Phase != ConversionRequest {
			return nil, fmt.Errorf("%s requires request phase", r.Action)
		}
		if (r.Action == "stream_options" || r.Action == "responses_include" || r.Action == "responses_context" || r.Action == "usage_projection" || r.Action == "responses_storage" || r.Action == "anthropic_usage_envelope") && r.Match.NodeKind != "" {
			return nil, fmt.Errorf("%s matches the complete semantic value, not an individual node", r.Action)
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
		if r.Action == "signatures" && !slices.Contains([]ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent}, r.Phase) {
			return nil, fmt.Errorf("signatures requires semantic phase")
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
	severity := SeverityWarning
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
	if severity == SeverityError {
		issue.Code = ConversionRejected
		issue.Fidelity = "rejected"
		return IssuesError([]ConversionIssue{issue})
	}
	sink.Add(issue)
	return nil
}

// ApplyValue executes bounded semantic or wire rules. Protected state cannot be
// minted through expressions; the separately authenticated continuation module
// is the only restoration authority.
func (c *CompiledConversion) ApplyValue(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink) (Value, error) {
	if !slices.Contains([]ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent, ConversionWire}, phase) {
		return Value{}, streamIssue(InvalidInput, "/phase", "unknown conversion phase")
	}
	if c == nil {
		return input, nil
	}
	if err := checkValueLimits(input, c.limits); err != nil {
		return Value{}, err
	}
	if err := validateContextValue(phase, input); err != nil {
		return Value{}, err
	}
	value := input
	for _, rule := range c.Policy.Rules {
		if !rule.Enabled || rule.Phase != phase {
			continue
		}
		var err error
		switch {
		case rule.Action == "system_instruction_hoist" || rule.Action == "stream_options" || rule.Action == "responses_include" || rule.Action == "responses_context" || rule.Action == "responses_storage":
			if !rule.Match.matches(route, value) {
				continue
			}
			var req Request
			if err = decodeContract(value.Bytes(), &req); err == nil {
				if rule.Action == "system_instruction_hoist" {
					err = c.hoistSystem(&req, route, rule, sink)
				} else if rule.Action == "responses_storage" {
					err = c.responsesStorage(&req, route, rule, sink)
				} else if rule.Action == "responses_include" {
					err = c.responsesInclude(&req, route, rule, sink)
				} else if rule.Action == "responses_context" {
					err = c.responsesContext(&req, route, rule, sink)
				} else {
					err = c.streamOptions(&req, route, rule, sink)
				}
				if err == nil {
					value, err = EncodeValue(req)
				}
			}
		case rule.Action == "anthropic_usage_envelope":
			if rule.Match.matches(route, value) {
				value, err = c.envelopeUsageValue(phase, value, route, rule, sink)
			}
		case rule.Action == "response_shape" || rule.Action == "response_envelope":
			if rule.Match.matches(route, value) {
				value, err = c.responseValue(rule.Action, phase, value, route, rule, sink)
			}
		case rule.Action == "response_metadata":
			if rule.Match.matches(route, value) {
				value, err = c.metadataValue(phase, value, route, rule, sink)
			}
		case rule.Action == "usage_projection":
			if rule.Match.matches(route, value) {
				value, err = c.projectUsageValue(phase, value, route, rule, sink)
			}
		case rule.Action == "signatures" || rule.Match.NodeKind != "":
			value, err = c.applyNodeRule(ctx, phase, value, route, sink, rule)
		default:
			if rule.Match.matches(route, value) {
				value, err = c.applyRule(ctx, phase, value, route, sink, rule)
			}
		}
		if err != nil {
			return Value{}, err
		}
		if err = checkValueLimits(value, c.limits); err != nil {
			return Value{}, err
		}
	}
	if err := validateContextValue(phase, value); err != nil {
		return Value{}, err
	}
	return value, nil
}

func (c *CompiledConversion) applyRule(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink, rule ConversionRule) (Value, error) {
	value := input
	var err error
	switch rule.Action {
	case "warn", "reject":
		err = c.issue(rule, phase, route, rule.Path, rule.Reason, sink, false)
	case "set", "remove":
		op := SetValue
		if rule.Action == "remove" {
			ptr, _ := parsePointer(rule.Path)
			old, e := readValuePointer(value, ptr)
			if e != nil {
				return Value{}, e
			}
			if old.IsZero() {
				return value, nil
			}
			op = DeleteValue
		}
		value, err = ApplyMutations(value, []Mutation{{Op: op, Path: rule.Path, Value: rule.Value}}, c.limits)
		if err == nil && hashValue(value) != hashValue(input) {
			err = c.issue(rule, phase, route, rule.Path, "explicit field conversion: "+rule.Action, sink, true)
		}
	case "transform":
		contextValue, _ := EncodeValue(route)
		value, err = c.expressions[rule.ID].evaluate(evaluation{ctx: ctx, input: value, root: value, context: contextValue, budget: &evaluationBudget{limits: c.limits}})
		if err == nil && hashValue(value) != hashValue(input) {
			err = c.issue(rule, phase, route, rule.Path, "explicit expression conversion changed semantic or wire fields", sink, true)
		}
	}
	if err == nil {
		err = protectConversionProvenance(input, value)
	}
	return value, err
}

// Only semantic node locations are traversed; tool arguments and arbitrary user
// JSON cannot masquerade as nodes or influence the restoration authority.
func (c *CompiledConversion) applyNodeRule(ctx context.Context, phase ConversionPhase, input Value, route ConversionContext, sink *DiagnosticSink, rule ConversionRule) (Value, error) {
	var visit func([]Node, string) error
	visit = func(nodes []Node, base string) error {
		for i := range nodes {
			n := &nodes[i]
			at := fmt.Sprintf("%s/%d", base, i)
			v, _ := EncodeValue(n)
			if (rule.Match.NodeKind == "" || rule.Match.NodeKind == n.Kind) && rule.Match.matches(route, v) {
				if rule.Action == "signatures" {
					foreign := route.Source.Family != route.Target.Family || route.Source.WireVersion != route.Target.WireVersion
					restored := n.Source != nil && n.Source.Protocol.Family == route.Target.Family && n.Source.Protocol.WireVersion == route.Target.WireVersion
					if foreign && !restored {
						kept := make([]Resource, 0, len(n.Resources))
						for j, res := range n.Resources {
							if res.Kind != "signature" {
								kept = append(kept, res)
								continue
							}
							recoverable := route.Recoverable[ContinuationNodeDigest(*n)] || route.Recoverable[SignatureRecoveryKey(res)]
							reason := "foreign signature has no target representation; recovery unavailable"
							if recoverable {
								reason = "foreign signature preserved in authenticated continuation state"
							}
							if err := c.issue(rule, phase, route, fmt.Sprintf("%s/resources/%d", at, j), reason, sink, !recoverable); err != nil {
								return err
							}
						}
						n.Resources = kept
					}
				} else if rule.Action == "provider_signature" && !HasSignature(*n) {
					if !c.VerifiedProviderRules[rule.ID] {
						return streamIssue(VerificationRequired, at+"/resources", "provider signature compatibility value requires verification for this policy, protocol revision, account and model")
					}
					signature, err := ProviderSignatureValue(rule.Value)
					if err != nil {
						return err
					}
					if err := c.issue(rule, phase, route, at+"/resources", "upstream-verified compatibility signature substituted for missing original state", sink, true); err != nil {
						return err
					}
					n.Resources = append(n.Resources, Resource{Kind: "signature", ID: signature, Scope: route.Scope})
					n.Source = &Provenance{Protocol: route.Target, Direction: DecodeResponse, Scope: route.Scope}
					n.Native = nil
				} else if rule.Action == "tool_result_text" {
					if n.Payload.IsObject() {
						if err := c.issue(rule, phase, route, at+"/payload", "object tool result serialized as JSON text; payload type changes", sink, true); err != nil {
							return err
						}
						n.Payload = StringValue(string(n.Payload.Bytes()))
					}
				} else if rule.Action == "tool_result_object" && !n.Payload.IsObject() {
					var text string
					if err := n.Payload.Decode(&text); err != nil {
						return fmt.Errorf("tool_result_object requires text or an object at %s/payload", at)
					}
					key := "result"
					if !rule.Value.IsZero() {
						if err := rule.Value.Decode(&key); err != nil || key == "" {
							return fmt.Errorf("tool_result_object requires a nonempty field name")
						}
					}
					if err := c.issue(rule, phase, route, at+"/payload", "text tool result wrapped in the configured object field", sink, true); err != nil {
						return err
					}
					n.Payload, _ = EncodeValue(Object{key: StringValue(text)})
				} else {
					out, err := c.applyRule(ctx, phase, v, route, sink, rule)
					if err != nil {
						return err
					}
					var changed Node
					if err := decodeContract(out.Bytes(), &changed); err != nil {
						return err
					}
					*n = changed
				}
			}
			if err := visit(n.Children, at+"/children"); err != nil {
				return err
			}
			if err := visit(n.ReasoningContent, at+"/reasoningContent"); err != nil {
				return err
			}
		}
		return nil
	}
	switch phase {
	case ConversionIngress, ConversionRequest:
		var r Request
		if err := decodeContract(input.Bytes(), &r); err != nil {
			return Value{}, err
		}
		if err := visit(r.Content, "/content"); err != nil {
			return Value{}, err
		}
		return EncodeValue(r)
	case ConversionResponse:
		var r Response
		if err := decodeContract(input.Bytes(), &r); err != nil {
			return Value{}, err
		}
		if err := visit(r.Content, "/content"); err != nil {
			return Value{}, err
		}
		return EncodeValue(r)
	case ConversionEvent:
		var e Event
		if err := decodeContract(input.Bytes(), &e); err != nil {
			return Value{}, err
		}
		if e.Item != nil {
			nodes := []Node{*e.Item}
			if err := visit(nodes, "/item"); err != nil {
				return Value{}, err
			}
			e.Item = &nodes[0]
		}
		if e.Response != nil {
			if err := visit(e.Response.Content, "/response/content"); err != nil {
				return Value{}, err
			}
		}
		return EncodeValue(e)
	}
	return Value{}, fmt.Errorf("node rules require a semantic phase")
}

func protectConversionProvenance(before, after Value) error {
	allowed := map[string]int{}
	var collect func(Value, bool) error
	collect = func(v Value, checking bool) error {
		if v.IsObject() {
			fields, err := v.ReadObject()
			if err != nil {
				return err
			}
			for key, child := range fields {
				if key == "source" || key == "native" || key == "scope" || key == "resources" || key == "thoughtSignature" || key == "signature" || key == "encrypted_content" || key == "elysia_continuation" {
					id := key + ":" + hashValue(child)
					if key == "resources" {
						owner := Object{}
						for field, data := range fields {
							if field != "source" && field != "native" && field != "resources" && field != "children" {
								owner[field] = data
							}
						}
						identity, _ := EncodeValue(owner)
						id += ":" + hashValue(identity)
					}
					if checking && allowed[id] == 0 {
						return fmt.Errorf("conversion cannot create or alter protected %s", key)
					}
					if !checking {
						allowed[id]++
					} else {
						allowed[id]--
					}
					continue
				}
				if err := collect(child, checking); err != nil {
					return err
				}
			}
		} else if strings.HasPrefix(strings.TrimSpace(v.raw), "[") {
			items, err := v.readArray()
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := collect(item, checking); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(before, false); err != nil {
		return err
	}
	return collect(after, true)
}

func (c *CompiledConversion) Request(ctx context.Context, request *Request, route ConversionContext, sink *DiagnosticSink) (*Request, error) {
	v, err := EncodeValue(request)
	if err != nil {
		return nil, err
	}
	v, err = c.ApplyValue(ctx, ConversionIngress, v, route, sink)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionRequest, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Request
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	if c.Policy.Usage != nil {
		if result.ClientOutput == nil {
			result.ClientOutput = &ClientOutput{}
		}
		result.ClientOutput.CollectUsage = c.Policy.Usage.CollectUpstreamUsage
	}
	return &result, nil
}

func (c *CompiledConversion) Response(ctx context.Context, response *Response, route ConversionContext, sink *DiagnosticSink) (*Response, error) {
	v, err := EncodeValue(response)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionResponse, v, route, sink)
	}
	if err != nil {
		return nil, err
	}
	var result Response
	if err = decodeContract(v.Bytes(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *CompiledConversion) Event(ctx context.Context, event Event, route ConversionContext, sink *DiagnosticSink) (Event, error) {
	v, err := EncodeValue(event)
	if err == nil {
		v, err = c.ApplyValue(ctx, ConversionEvent, v, route, sink)
	}
	if err != nil {
		return Event{}, err
	}
	var result Event
	err = decodeContract(v.Bytes(), &result)
	return result, err
}

func (c *CompiledConversion) streamOptions(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	if raw, exists := request.Parameters["stream_options"]; exists && !raw.IsZero() {
		legacy, err := ParseClientOutput(raw)
		if err != nil {
			return err
		}
		if request.ClientOutput != nil && !request.ClientOutput.RawStreamOptions.IsZero() && !equalValues(request.ClientOutput.RawStreamOptions, raw) {
			return streamIssue(InvalidInput, "/stream_options", "conflicting legacy and typed client output preferences")
		}
		if request.ClientOutput != nil {
			legacy.CollectUsage = request.ClientOutput.CollectUsage
			legacy.RawResponsesInclude = request.ClientOutput.RawResponsesInclude
		}
		request.ClientOutput = legacy
		delete(request.Parameters, "stream_options")
	}
	output := request.ClientOutput
	if output == nil {
		if c.Policy.Usage == nil || !c.Policy.Usage.DefaultIncludeUsage {
			return nil
		}
		output = &ClientOutput{}
		request.ClientOutput = output
	}
	if output.IncludeUsage == nil && c.Policy.Usage != nil && c.Policy.Usage.DefaultIncludeUsage {
		v := true
		output.IncludeUsage = &v
	}
	if output.RawStreamOptions.IsZero() {
		return nil
	}
	stream := false
	_ = request.Parameters["stream"].Decode(&stream)
	if !stream {
		if err := c.issue(rule, ConversionRequest, route, "/stream_options", "stream options ignored for non-streaming request", sink, true); err != nil {
			return err
		}
		output.RawStreamOptions = Value{}
		return nil
	}
	if route.Source.Family != route.Target.Family || route.Source.WireVersion != route.Target.WireVersion {
		if output.RawStreamOptions.IsObject() {
			fields, _ := output.RawStreamOptions.ReadObject()
			for _, key := range sortedKeys(fields) {
				if key != "include_usage" {
					if err := c.issue(rule, ConversionRequest, route, "/stream_options/"+escapePointer(key), "client stream extension has no configured target equivalent", sink, true); err != nil {
						return err
					}
				}
			}
		}
		output.RawStreamOptions = Value{}
	}
	return nil
}

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
		route.Model = c.VerificationContext.Model
		route.Operation = c.VerificationContext.Operation
		route.Transport = c.VerificationContext.Transport
	}
	return route
}
func (c *CompiledConversion) ContextDependent() bool {
	for _, r := range c.Policy.Rules {
		if r.Enabled && (r.Action == "provider_signature" || r.Match.Model != "" || r.Match.Operation != "" || r.Match.Transport != "") {
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
