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
	operation, err := (p.EvaluationContext{}).WithOperation("shape-projection", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "event-shape", "compatible"))
			if err != nil {
				t.Fatal(err)
			}
			route := (p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "model"}).WithOperation(operation)
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
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("model"),
				Content:    []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}},
				Parameters: p.Object{"max_output_tokens": testValue(t, `10`)}}
			for _, mode := range []string{"compatible", "strict"} {
				policy := declaredRulePolicy(t, source, target, "responses-storage", mode)
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				for _, raw := range []string{`false`, `true`, `null`} {
					t.Run(mode+"/"+raw, func(t *testing.T) {
						input := request.Clone()
						intent := &p.ResponsesStorageIntent{SourceCodec: "responses", Raw: testValue(t, raw), Requested: raw == `true`, Effective: raw == `true`, RequestedUnknown: raw == `null`, EffectiveUnknown: raw == `null`}
						input.ClientOutput = &p.ClientOutput{ResponsesStorage: intent}
						before, _ := p.EncodeValue(input)
						sink := &p.DiagnosticSink{}
						projected, err := conversion.Request(t.Context(), input, route, sink)
						after, _ := p.EncodeValue(input)
						if before != after {
							t.Fatal("storage projection mutated caller intent")
						}
						if mode == "strict" && raw != `false` {
							var failure *p.ConversionError
							if !errors.As(err, &failure) || failure.Issues[0].RuleID != "responses-storage" || failure.Issues[0].Code != p.ConversionRejected {
								t.Fatal("strict storage loss did not reject with its declared rule", err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						want := *intent
						want.Effective, want.EffectiveUnknown = false, false
						if projected.ClientOutput.ResponsesStorage == intent || *projected.ClientOutput.ResponsesStorage != want {
							t.Fatal("target changed source facts or retained a mutable caller owner", projected.ClientOutput)
						}
						issues := sink.Issues()
						if raw == `false` {
							if len(issues) != 0 {
								t.Fatal("stateless intent was treated as lossy", issues)
							}
						} else if len(issues) != 1 || issues[0].Code != p.ConversionDegraded || issues[0].RuleID != "responses-storage" || issues[0].PolicyHash != conversion.Hash || issues[0].Path != "/clientOutput/responsesStorage" {
							t.Fatal("storage loss did not retain diagnostic attribution", issues)
						}
						wire, err := target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
						if err != nil {
							t.Fatal("resolved storage intent did not encode", err)
						}
						var body p.Object
						if err := testValue(t, string(wire)).Decode(&body); err != nil || !body["store"].IsZero() {
							t.Fatal("target invented a response storage field", string(wire), err)
						}
					})
				}
			}
			policy := declaredRulePolicy(t, source, target, "responses-storage", "compatible")
			policy.Rules[0].Enabled = false
			disabled, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			for _, intent := range []p.ResponsesStorageIntent{{Requested: true, Effective: true}, {RequestedUnknown: true, EffectiveUnknown: true}, {EffectiveUnknown: true}} {
				input := request.Clone()
				input.ClientOutput = &p.ClientOutput{ResponsesStorage: &intent}
				projected, err := disabled.Request(t.Context(), input, route, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := target.EncodeRequest(t.Context(), projected, p.EvaluationContext{}); err == nil {
					t.Fatal("encoder silently resolved storage after its rule was disabled", intent)
				}
			}
			policy.Rules[0].Enabled = true
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := conversion.Request(t.Context(), request, route, nil)
			if err != nil || projected.ClientOutput.ResponsesStorage != nil {
				t.Fatal("undeclared storage intent was fabricated", projected, err)
			}
			for _, raw := range []string{`false`, `true`, `null`} {
				input := request.Clone()
				input.Parameters["store"] = testValue(t, raw)
				if _, err := conversion.Request(t.Context(), input, route, nil); err == nil {
					t.Fatal("legacy storage owner was silently interpreted", raw)
				}
				if _, err := target.EncodeRequest(t.Context(), input, p.EvaluationContext{}); err == nil {
					t.Fatal("legacy storage owner bypassed the single-owner encoding boundary", raw)
				}
			}
		})
	}
}

func TestPackageStatelessStorageSource(t *testing.T) {
	for _, fixture := range []struct{ name, codec, wireOwner, wire string }{
		{"anthropic", "anthropic", "wire:claude", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}],"store":{"n":9007199254740993,"null":null}}`},
		{"google-generate-content", "gemini", "wire:gemini", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"store":{"n":9007199254740993,"null":null}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, fixture.name)
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}, Values: p.Object{"model": p.StringValue("m")}}
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), options)
			if err != nil {
				t.Fatal(err)
			}
			want := p.ResponsesStorageIntent{SourceCodec: fixture.codec}
			if request.ClientOutput == nil || request.ClientOutput.ResponsesStorage == nil || *request.ClientOutput.ResponsesStorage != want || !request.Parameters["store"].IsZero() {
				t.Fatal("source storage owner does not match the API contract", request.ClientOutput, request.Parameters)
			}
			var extension p.Object
			if err := request.Parameters[fixture.wireOwner].Decode(&extension); err != nil || extension["store"].IsZero() {
				t.Fatal("unknown wire store was mistaken for a supported storage instruction", request.Parameters, err)
			}
			native, err := compiled.EncodeRequest(t.Context(), request, options)
			if err != nil || string(native) != fixture.wire {
				t.Fatal("source owner changed unchanged native bytes", string(native), err)
			}
			request.Native = nil
			rebuilt, err := compiled.EncodeRequest(t.Context(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			var body p.Object
			if err := testValue(t, string(rebuilt)).Decode(&body); err != nil || body["store"] != extension["store"] {
				t.Fatal("reconstruction lost an unknown native extension", string(rebuilt), err)
			}
		})
	}
}
