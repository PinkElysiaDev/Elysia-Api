package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDraftResponsesExtensions(t *testing.T) {
	const base = `{ "id":"r","model":"m","object":"response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"future":{"n":9007199254740993,"null":null,"values":[1e2,"<"]} }`
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, extra string }{
		{"unknown", ""},
		{"metadata", `,"service_tier":"default","metadata":{"trace":"synthetic","n":9007199254740993},"store":false,"temperature":0.25,"tools":[],"reasoning":{},"text":{},"user":"synthetic-user"`},
		{"null-metadata", `,"service_tier":null,"metadata":null,"tools":null,"store":null`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raw := strings.TrimSuffix(base, " }") + fixture.extra + " }"
			response, err := compiled.DecodeResponse(t.Context(), []byte(raw), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"native", "changed-model", "rebuilt"} {
				t.Run(mode, func(t *testing.T) {
					candidate := *response
					expected := raw
					if mode == "rebuilt" {
						candidate.Native = nil
					}
					if mode == "changed-model" {
						candidate.Model = p.StringValue("routed")
						expected = strings.Replace(expected, `"model":"m"`, `"model":"routed"`, 1)
					}
					before, _ := p.EncodeValue(candidate)
					wire, err := compiled.EncodeResponse(t.Context(), &candidate, newDraftAuditOptions())
					if err != nil {
						t.Fatal(err)
					}
					sameJSON(t, wire, expected)
					if mode == "native" && string(wire) != raw {
						t.Fatal("unchanged response lost native spelling")
					}
					after, _ := p.EncodeValue(candidate)
					if before != after {
						t.Fatal("response encoding mutated its input")
					}
				})
			}
		})
	}
	for _, fixture := range []struct{ field, value string }{{"service_tier", "1"}, {"metadata", "[]"}, {"store", `"false"`}} {
		t.Run("invalid/"+fixture.field, func(t *testing.T) {
			raw := strings.TrimSuffix(base, " }") + `,"` + fixture.field + `":` + fixture.value + " }"
			_, err := compiled.DecodeResponse(t.Context(), []byte(raw), newDraftAuditOptions())
			requireDraftRejection(t, err, "/"+fixture.field)
		})
	}
}

func TestDraftResponsesModelOwners(t *testing.T) {
	compiled := testCompiled(t, Responses)
	base, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi"}`), newDraftAuditOptions())
	if err != nil {
		t.Fatal(err)
	}
	base.Native = nil
	for _, fixture := range []struct {
		name, model, owner string
		canEncode          bool
	}{
		{"missing", "", "", true},
		{"null", `null`, "", false},
		{"empty-string", `""`, "", true},
		{"boolean", `false`, "", false},
		{"number", `1e2`, "", false},
		{"array", `[null,9007199254740993]`, "", false},
		{"object", `{"nested":{"original":1}}`, `{"future":true}`, false},
		{"owner-empty", `"m"`, `{}`, true},
		{"owner-fills-model", "", `{"model":{"future":null}}`, false},
		{"owner-merges-model", `{"nested":{"original":1}}`, `{"model":{"nested":{"extra":2},"future":null}}`, false},
		{"equal-string-conflict", `"m"`, `{"model":"m"}`, false},
		{"null-conflict", `null`, `{"model":null}`, false},
		{"array-conflict", `[]`, `{"model":[]}`, false},
		{"nested-conflict", `{"nested":{"value":1}}`, `{"model":{"nested":{"value":1}}}`, false},
		{"object-scalar-conflict", `{"value":1}`, `{"model":true}`, false},
		{"null-owner", `"m"`, `null`, false},
		{"scalar-owner", `"m"`, `false`, false},
		{"array-owner", `"m"`, `[]`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := base.Clone()
			request.Model = p.Value{}
			if fixture.model != "" {
				request.Model = testValue(t, fixture.model)
			}
			if fixture.owner != "" {
				request.Parameters = p.Object{"wire:openai_responses": testValue(t, fixture.owner)}
			}
			before, _ := p.EncodeValue(request)
			wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			if fixture.canEncode {
				if err != nil {
					t.Fatal(err)
				}
				fields, _ := wire.ReadObject()
				if fields["model"] != request.Model {
					t.Fatal("direct encoder invented a model", wire)
				}
			} else {
				if err == nil {
					err = compiled.ValidateRequestOutput(t.Context(), wire, newDraftAuditOptions())
				}
				requireDraftRejection(t, err, "")
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("model owner rejection mutated input")
			}
		})
	}
}
