package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDraftTrackingInputs(t *testing.T) {
	for _, codec := range []string{Chat, Responses} {
		compiled := testCompiled(t, codec)
		for _, fixture := range []struct{ name, parameters string }{
			{"absent", `{}`},
			{"user-only", `{"user":"current"}`},
			{"metadata-empty", `{"anthropic_metadata":{}}`},
			{"metadata-null", `{"anthropic_metadata":null}`},
			{"tracking-user", `{"anthropic_metadata":{"user_id":"tracking"}}`},
			{"tracking-null", `{"anthropic_metadata":{"user_id":null}}`},
			{"same-null", `{"user":null,"anthropic_metadata":{"user_id":null}}`},
			{"same-string-spelling", `{"user":"\\path\/user","anthropic_metadata":{"user_id":"\\path/user"}}`},
			{"same-unicode", `{"user":"\u0075ser","anthropic_metadata":{"user_id":"user"}}`},
			{"current-user-priority", `{"user":"current","anthropic_metadata":{}}`},
			{"conflicting-user", `{"user":"current","anthropic_metadata":{"user_id":"other"}}`},
			{"null-user-conflict", `{"user":null,"anthropic_metadata":{"user_id":"other"}}`},
			{"null-tracking-conflict", `{"user":"current","anthropic_metadata":{"user_id":null}}`},
			{"unknown-member", `{"anthropic_metadata":{"future":null}}`},
			{"unknown-before-conflict", `{"user":"current","anthropic_metadata":{"future":false,"user_id":"other"}}`},
			{"raw-string-equivalent", `{"user":"\u0075ser","anthropic_metadata":{"user_id":"user"}}`},
			{"typed-whitespace-null", `{"user": null ,"anthropic_metadata":{"user_id":null}}`},
		} {
			t.Run(codec+"/"+fixture.name, func(t *testing.T) {
				parameters, err := testValue(t, fixture.parameters).ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				if fixture.name == "typed-whitespace-null" {
					parameters["user"] = testValue(t, " null ")
				}
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: parameters}
				input, err := p.EncodeValue(request)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
				raw, issues := compiled.Execute(t.Context(), p.EncodeRequest, input, newDraftAuditOptions())
				if !parameters["anthropic_metadata"].IsZero() {
					requireDraftRejection(t, err, "/parameters")
					requireDraftRejection(t, p.IssuesError(issues), "/parameters")
				} else {
					if err != nil || p.IssuesError(issues) != nil {
						t.Fatal(err, issues)
					}
					for _, value := range []p.Value{wire, raw} {
						fields, _ := value.ReadObject()
						if fields["user"] != parameters["user"] {
							t.Fatal("canonical identity changed", value)
						}
					}
				}
				after, _ := p.EncodeValue(request)
				if input != after {
					t.Fatal("tracking rejection mutated the caller")
				}
			})
		}
	}
}
func TestDraftMediaURIInputs(t *testing.T) {
	for _, fixture := range []struct {
		codec, name, path, raw string
		isRejected             bool
	}{
		{Chat, "empty-mime", "/messages/0/content/0/image_url/url", `"data:;base64,AAAA"`, false},
		{Chat, "invalid-marker", "/messages/0/content/0/image_url/url", `"data:image/png;base64;charset=utf-8,AAAA"`, false},
		{Chat, "payload-comma", "/messages/0/content/0/image_url/url", `"data:image/png;base64,AAAA,tail"`, false},
		{Chat, "null", "/messages/0/content/0/image_url/url", `null`, true},
		{Chat, "missing", "/messages/0/content/0/image_url/url", "", false},
		{Responses, "base64", "/input/0/content/0/image_url", `"data:image/png;base64,AAAA"`, false},
	} {
		t.Run(fixture.codec+"/"+fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, fixture.codec)
			var sample p.Sample
			for _, candidate := range packageMapping(compiled).Samples {
				if candidate.ID == "image-decode" {
					sample = candidate
					break
				}
			}
			if sample.ID == "" {
				t.Fatal("original image sample missing")
			}
			mutation := p.Mutation{Op: p.DeleteValue, Path: fixture.path}
			if fixture.raw != "" {
				mutation.Op, mutation.Value = p.SetValue, testValue(t, fixture.raw)
			}
			input, err := p.ApplyMutations(sample.Input, []p.Mutation{mutation}, p.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			request, err := compiled.DecodeRequestValue(t.Context(), input, newDraftAuditOptions())
			if fixture.isRejected {
				requireDraftRejection(t, err, "")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			wire := encodeDraftRequest(t, compiled, request)
			actual := fixturePointerValue(t, wire.Bytes(), fixture.path)
			if fixture.raw == "" {
				if actual != nil {
					t.Fatal("absent URL acquired a value", wire)
				}
				return
			}
			if actual == nil {
				t.Fatal("image URL disappeared", wire)
			}
			sameJSON(t, []byte(*actual), fixture.raw)
		})
	}
}
