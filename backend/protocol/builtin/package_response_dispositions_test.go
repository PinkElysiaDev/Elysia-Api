package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func responseWithMessageEnvelope(t *testing.T) *p.Response {
	t.Helper()
	return &p.Response{
		SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("response"), Model: p.StringValue("model"), Status: p.StringValue("completed"),
		Attributes: p.Object{"created_at": testValue(t, `1`), "finishReason": p.StringValue("stop")},
		Content: []p.Node{{Kind: p.MessageNode, ID: p.StringValue("message"), Status: p.StringValue("completed"), Role: p.StringValue("assistant"), Children: []p.Node{
			{Kind: p.TextNode, Payload: p.StringValue("hello")},
			{Kind: p.ToolCallNode, CallID: p.StringValue("local-call"), Name: p.StringValue("lookup"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{}`)}},
		}}},
		Usage: &p.Usage{Input: &p.Counter{Count: 2, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}},
	}
}

func messageLeaves(t *testing.T, response *p.Response) []p.Node {
	t.Helper()
	if len(response.Content) != 1 || response.Content[0].Kind != p.MessageNode || !response.Content[0].Role.IsString("assistant") {
		t.Fatal("stream requires its declared assistant message container", response)
	}
	return response.Content[0].Children
}

func TestPackageDeclaredResponseShapeDispositions(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			policy := declaredRulePolicy(t, target, source, "response-shape", "compatible")
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			input := responseWithMessageEnvelope(t)
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			sink := &p.DiagnosticSink{}
			result, err := conversion.Response(t.Context(), input, route, sink)
			if err != nil {
				t.Fatal(err)
			}
			message := result.Content[0]
			if !result.Attributes["created_at"].IsZero() || !message.ID.IsZero() || !message.Status.IsZero() || !result.ID.IsString("response") || !result.Status.IsString("completed") || !message.Children[1].CallID.IsString("local-call") {
				t.Fatal("projection changed a representable identity or terminal", result)
			}
			if input.Attributes["created_at"].IsZero() || input.Content[0].ID.IsZero() || input.Content[0].Status.IsZero() {
				t.Fatal("projection mutated its source")
			}
			paths := map[string]p.IssueCode{}
			for _, issue := range sink.Issues() {
				if issue.RuleID != "response-shape" || issue.PolicyHash != conversion.Hash {
					t.Fatal("target disposition lost its rule attribution", issue)
				}
				paths[issue.Path] = issue.Code
			}
			if paths["/attributes/created_at"] != p.ConversionDegraded || paths["/content/0/id"] != p.ConversionDegraded || paths["/content/0/status"] != p.ConversionNormalized {
				t.Fatal("response loss and equivalent lifecycle were conflated", paths)
			}
			if _, err := target.EncodeResponse(t.Context(), result, p.EvaluationContext{}); err != nil {
				t.Fatal("projected response did not encode", err)
			}
			policy.Mode = "strict"
			strict, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			var failure *p.ConversionError
			if _, err := strict.Response(t.Context(), input, route, nil); !errors.As(err, &failure) || failure.Issues[0].RuleID != "response-shape" {
				t.Fatal("strict conversion silently lost an envelope field", err)
			}
			for _, field := range []string{"timestamp", "message-id", "message-status", "wire-owner"} {
				unprojected := responseWithMessageEnvelope(t)
				delete(unprojected.Attributes, "created_at")
				unprojected.Content[0].ID, unprojected.Content[0].Status = p.Value{}, p.Value{}
				switch field {
				case "timestamp":
					unprojected.Attributes["created_at"] = testValue(t, `1`)
				case "message-id":
					unprojected.Content[0].ID = p.StringValue("message")
				case "message-status":
					unprojected.Content[0].Status = p.StringValue("completed")
				case "wire-owner":
					unprojected.Attributes["wire:claude"] = testValue(t, `{"vendor":null}`)
				}
				if _, err := target.EncodeResponse(t.Context(), unprojected, p.EvaluationContext{}); err == nil {
					t.Fatal("encoding bypassed the required policy", field)
				}
			}
			input = responseWithMessageEnvelope(t)
			delete(input.Attributes, "created_at")
			input.Content[0].ID, input.Content[0].Status = p.Value{}, p.StringValue("incomplete")
			if _, err := conversion.Response(t.Context(), input, route, nil); err == nil {
				t.Fatal("message completion conflicting with the response was discarded")
			}
		})
	}
}

func TestPackageDeclaredStreamShapeDispositions(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "event-shape", "compatible"))
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			event := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("stream-message"),
				Item: &p.Node{Kind: p.MessageNode, ID: p.StringValue("message"), Status: p.StringValue("in_progress"), Role: p.StringValue("assistant")}}
			result, err := conversion.Event(t.Context(), event, route, nil)
			if err != nil || !result.Item.ID.IsZero() || !result.Item.Status.IsZero() || !result.ItemID.IsString("stream-message") || result.Type != p.ItemStarted {
				t.Fatal("message projection changed stream correlation or lifecycle", result, err)
			}
			start := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted,
				Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, Attributes: p.Object{"created_at": testValue(t, `1`)}}}
			result, err = conversion.Event(t.Context(), start, route, nil)
			if err != nil || !result.Response.Attributes["created_at"].IsZero() || start.Response.Attributes["created_at"].IsZero() {
				t.Fatal("stream attribute projection lost ownership", result, err)
			}
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			defer options.State.Release()
			streamStart := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, ResponseID: p.StringValue("response"),
				Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("response"), Model: p.StringValue("model"), Status: p.StringValue("in_progress")},
				Usage:    &p.Usage{Input: &p.Counter{Origin: p.ObservedCount}, Output: &p.Counter{Origin: p.ObservedCount}}}
			if _, err := target.EncodeFrames(t.Context(), streamStart, options); err != nil {
				t.Fatal(err)
			}
			parent := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("first-message"), Item: &p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant")}}
			if _, err := target.EncodeFrames(t.Context(), parent, options); err != nil {
				t.Fatal("single logical parent did not register", err)
			}
			parent.ItemID = p.StringValue("second-message")
			if _, err := target.EncodeFrames(t.Context(), parent, options); err == nil {
				t.Fatal("target silently merged independent message containers")
			}
			end := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("first-message")}
			if _, err := target.EncodeFrames(t.Context(), end, options); err != nil {
				t.Fatal(err)
			}
			late := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("late"), ParentID: end.ItemID, Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("late")}}
			if _, err := target.EncodeFrames(t.Context(), late, options); err == nil {
				t.Fatal("target accepted a child after its message completed")
			}
		})
	}
}

func TestPackageDeclaredStatelessStorage(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(declaredRulePolicy(t, source, target, "responses-storage", "strict"))
			if err != nil {
				t.Fatal(err)
			}
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("model"),
				Content:    []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}},
				Parameters: p.Object{"max_output_tokens": testValue(t, `10`), "store": testValue(t, `false`)}}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, route, sink)
			if err != nil || !projected.Parameters["store"].IsZero() || request.Parameters["store"].IsZero() {
				t.Fatal("explicit stateless intent was lost or mutated its caller", projected, err)
			}
			if issues := sink.Issues(); len(issues) != 1 || issues[0].Code != p.ConversionNormalized || issues[0].RuleID != "responses-storage" {
				t.Fatal("equivalent stateless delivery was treated as lossy", issues)
			}
			if _, err := target.EncodeRequest(t.Context(), projected, p.EvaluationContext{}); err != nil {
				t.Fatal("declared stateless projection did not encode", err)
			}
			if _, err := target.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
				t.Fatal("encoding bypassed the disabled storage disposition")
			}
			for _, unsupported := range []string{`true`, `null`} {
				request.Parameters["store"] = testValue(t, unsupported)
				if _, err := conversion.Request(t.Context(), request, route, nil); err == nil {
					t.Fatal("retrieval storage was silently discarded", unsupported)
				}
			}
			delete(request.Parameters, "store")
			if projected, err := conversion.Request(t.Context(), request, route, nil); err != nil || !projected.Parameters["store"].IsZero() {
				t.Fatal("absent intent was replaced by a default", projected, err)
			}
		})
	}
}
