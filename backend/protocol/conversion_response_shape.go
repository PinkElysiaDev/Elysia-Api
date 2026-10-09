package protocol

import (
	"crypto/rand"
	"fmt"
	"time"
)

// DeliveryState is owned by one attempt, never by a shared compiled policy.
// Tests/previews can inject a deterministic ID and timestamp.
type DeliveryState struct {
	ID       Value
	Created  Value
	chatText *chatTextProjection
}

func NewDeliveryState() *DeliveryState {
	created, _ := EncodeValue(time.Now().Unix())
	return &DeliveryState{ID: StringValue("resp_" + rand.Text()), Created: created}
}

func (c *CompiledConversion) responseShape(r *Response, phase ConversionPhase, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var codec string
	_ = rule.Value.Decode(&codec)
	if codec == "responses" {
		var output []Node
		for _, n := range r.Content {
			if !n.Attributes["choiceIndex"].IsZero() {
				return streamIssue(UnsupportedCapability, "/content", "multiple candidate answers cannot become a single Responses output sequence")
			}
			if n.Kind != MessageNode || len(n.Attributes) > 0 || len(n.Metadata) > 0 || len(n.Cache) > 0 || len(n.Resources) > 0 {
				output = append(output, n)
				continue
			}
			var children []Node
			flush := func() {
				if len(children) > 0 {
					copy := n
					copy.Children = children
					output = append(output, copy)
					children = nil
				}
			}
			for _, child := range n.Children {
				if child.Kind == ToolCallNode || child.Kind == ReasoningNode {
					flush()
					output = append(output, child)
				} else {
					children = append(children, child)
				}
			}
			flush()
		}
		r.Content = output
		return nil
	}
	if codec != "openai-chat" && !r.Attributes["created_at"].IsZero() {
		if err := c.issue(rule, phase, route, "/attributes/created_at", "target has no response creation timestamp", sink, true); err != nil {
			return err
		}
		delete(r.Attributes, "created_at")
	}
	var flatten func([]Node, string) ([]Node, error)
	flatten = func(nodes []Node, base string) ([]Node, error) {
		out := make([]Node, 0, len(nodes))
		for i, n := range nodes {
			at := fmt.Sprintf("%s/%d", base, i)
			if n.ReasoningForm == StructuredReasoning {
				// A stream starts before the provider chooses to send summary or
				// visible text. There is no text to relabel at an empty start.
				if phase == ConversionEvent && len(n.Children) == 0 && len(n.ReasoningContent) == 0 {
					n.ReasoningForm, n.ReasoningContent = "", nil
				} else {
					n = CanonicalReasoning(n)
				}
			}
			var err error
			n.Children, err = flatten(n.Children, at+"/children")
			if err != nil {
				return nil, err
			}
			if (n.Kind == ToolCallNode || n.Kind == ReasoningNode) && (!n.ID.IsZero() || !n.Status.IsZero()) {
				if err := c.issue(rule, phase, route, at, "target cannot express this separate output item ID/status; tool call identity is retained", sink, true); err != nil {
					return nil, err
				}
				n.ID, n.Status = Value{}, Value{}
			}
			if n.Kind == MessageNode {
				if !n.Attributes["choiceIndex"].IsZero() {
					out = append(out, n)
					continue
				}
				if !n.ID.IsZero() || !n.Status.IsZero() {
					if err := c.issue(rule, phase, route, at, "target has no per-message response item identity/status", sink, true); err != nil {
						return nil, err
					}
					n.ID, n.Status = Value{}, Value{}
				}
				if len(nodes) > 1 && n.Role == StringValue("assistant") && len(n.Attributes) == 0 && len(n.Cache) == 0 && len(n.Resources) == 0 && len(n.Metadata) == 0 {
					if err := c.issue(rule, phase, route, at, "target merges ordered message boundaries within one assistant turn", sink, true); err != nil {
						return nil, err
					}
					out = append(out, n.Children...)
					continue
				}
			}
			out = append(out, n)
		}
		return out, nil
	}
	var err error
	r.Content, err = flatten(r.Content, "/content")
	if err == nil && codec == "openai-chat" && phase == ConversionResponse {
		for i := range r.Content {
			if r.Content[i].Kind == MessageNode {
				r.Content[i].Children, err = c.chatContentShape(r.Content[i].Children, phase, route, rule, sink)
				if err != nil {
					return err
				}
			}
		}
		if len(r.Content) > 0 && r.Content[0].Kind != MessageNode {
			r.Content, err = c.chatContentShape(r.Content, phase, route, rule, sink)
		}
	}
	return err
}

func prepareDelivery(r *Response, route ConversionContext, codec string) {
	delivery := route.Delivery
	if delivery == nil {
		delivery = NewDeliveryState()
	}
	if r.ID.IsZero() || r.ID.IsNull() {
		r.ID = delivery.ID
	}
	if r.Model.IsZero() || r.Model.IsNull() {
		model := route.Model
		if model == "" {
			model = route.Scope.Model
		}
		if model == "" {
			model = "unknown"
		}
		r.Model = StringValue(model)
	}
	if codec == "openai-chat" || codec == "responses" {
		if r.Attributes == nil {
			r.Attributes = Object{}
		}
		if r.Attributes["created_at"].IsZero() || r.Attributes["created_at"].IsNull() {
			r.Attributes["created_at"] = delivery.Created
		}
	}
	if codec != "responses" {
		return
	}
	var responseID string
	_ = r.ID.Decode(&responseID)
	serial := 0
	var visit func([]Node)
	visit = func(nodes []Node) {
		for i := range nodes {
			n := &nodes[i]
			if n.Kind == MessageNode || n.Kind == ToolCallNode || n.Kind == ReasoningNode {
				if n.ID.IsZero() || n.ID.IsNull() {
					n.ID = StringValue(fmt.Sprintf("%s_item_%d", responseID, serial))
				}
				serial++
				if n.Status.IsZero() {
					n.Status = r.Status
					if n.Status.IsZero() {
						n.Status = StringValue("completed")
					}
				}
			}
			visit(n.Children)
		}
	}
	visit(r.Content)
}

func (c *CompiledConversion) responseValue(action string, phase ConversionPhase, input Value, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (Value, error) {
	var codec string
	_ = rule.Value.Decode(&codec)
	apply := func(r *Response) error {
		if r == nil {
			return nil
		}
		if !r.Error.IsZero() && !r.Error.IsNull() {
			return nil
		}
		if action == "response_shape" {
			return c.responseShape(r, phase, route, rule, sink)
		}
		prepareDelivery(r, route, codec)
		return nil
	}
	if phase == ConversionResponse {
		var r Response
		if err := input.Decode(&r); err != nil {
			return Value{}, err
		}
		if err := apply(&r); err != nil {
			return Value{}, err
		}
		return EncodeValue(r)
	}
	var e Event
	if err := input.Decode(&e); err != nil {
		return Value{}, err
	}
	if action == "response_shape" && codec == "openai-chat" {
		if err := c.chatEventShape(&e, route, rule, sink); err != nil {
			return Value{}, err
		}
	}
	if action == "response_shape" && e.Item != nil {
		r := Response{Content: []Node{*e.Item}}
		if err := c.responseShape(&r, phase, route, rule, sink); err != nil {
			return Value{}, err
		}
		if len(r.Content) != 1 {
			return Value{}, streamIssue(UnsupportedCapability, "/item", "stream item projection must retain one node association")
		}
		e.Item = &r.Content[0]
	}
	if e.Type == ResponseStarted || e.Type == ResponseFinished {
		if e.Response == nil {
			e.Response = &Response{SchemaVersion: SemanticSchemaVersion, ID: e.ResponseID}
		}
		if err := apply(e.Response); err != nil {
			return Value{}, err
		}
		e.ResponseID = e.Response.ID
	}
	return EncodeValue(e)
}

func copyObject(o Object) Object {
	copy := Object{}
	for k, v := range o {
		copy[k] = v
	}
	return copy
}
func (c *CompiledConversion) hasDeliveryRule(route ConversionContext) bool {
	if c == nil {
		return false
	}
	for _, r := range c.Policy.Rules {
		if r.Enabled && r.Phase == ConversionEvent && r.Action == "response_envelope" && r.Match.matches(route, Value{}) {
			return true
		}
	}
	return false
}

func (c *CompiledConversion) deliveryCodec(route ConversionContext) string {
	for _, r := range c.Policy.Rules {
		if r.Enabled && r.Phase == ConversionEvent && r.Action == "response_envelope" && r.Match.matches(route, Value{}) {
			var codec string
			_ = r.Value.Decode(&codec)
			return codec
		}
	}
	return ""
}
