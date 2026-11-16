package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const citationChat = `[{"type":"url_citation","url_citation":{"url":"https://example.org/","title":"source","start_index":0,"end_index":2}}]`
const citationResponses = `[{"type":"url_citation","url":"https://example.org/","title":"source","start_index":0,"end_index":2}]`
const auditLogprobs = `[{"token":"OK","logprob":-0.125,"bytes":[79,75],"top_logprobs":[]}]`

func TestAuditMultipleChoicesKeepOwnership(t *testing.T) {
	compiled := testCompiled(t, Chat)
	const raw = `{"id":"r","model":"m","created":1,"service_tier":"default","choices":[{"index":2,"message":{"role":"assistant","content":"first"},"finish_reason":"stop","logprobs":{"content":[]},"vendor_choice":"one"},{"index":4,"message":{"role":"assistant","content":"second"},"finish_reason":"length","logprobs":null,"vendor_choice":"two"}]}`
	response, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	response.Native = nil
	wire, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := testValue(t, raw).ReadObject()
	after, _ := wire.ReadObject()
	if !after["choiceExtensions"].IsZero() {
		t.Fatal("choice metadata moved to the response root")
	}
	sameJSON(t, after["choices"].Bytes(), before["choices"].JSONString())
}

func TestAuditLogprobDeltaAfterNullSnapshot(t *testing.T) {
	frames := []string{
		`{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}`,
		`{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"OK"},"logprobs":{"content":` + auditLogprobs + `},"finish_reason":null}]}`,
		`{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	for _, target := range []string{Chat, Responses} {
		t.Run(target, func(t *testing.T) {
			output := auditConvertFrames(t, Chat, target, frames)
			wire, err := p.EncodeValue(output)
			if err != nil || !strings.Contains(wire.JSONString(), `"logprob":-0.125`) {
				t.Fatal("probabilities after null snapshot were lost", wire.JSONString(), err)
			}
		})
	}
}

func TestAuditPublicMetadataJSON(t *testing.T) {
	for _, source := range []string{Chat, Responses} {
		target := Chat
		if source == Chat {
			target = Responses
		}
		from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, target)
		raw := auditResponses[source]
		if source == Chat {
			raw = strings.Replace(raw, `"content":"OK"`, `"content":"OK","annotations":`+citationChat, 1)
			raw = strings.Replace(raw, `"logprobs":null`, `"logprobs":{"content":`+auditLogprobs+`}`, 1)
		} else {
			raw = strings.Replace(raw, `"annotations":[]`, `"annotations":`+citationResponses+`,"logprobs":`+auditLogprobs, 1)
		}
		r, err := from.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
		r, err = c.Response(t.Context(), r, c.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := to.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(string(wire), `https://example.org/`) || !strings.Contains(string(wire), `-0.125`) {
			t.Fatal(string(wire))
		}
		decoded, err := to.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := p.EncodeValue(decoded)
		if !strings.Contains(string(encoded.Bytes()), `https://example.org/`) {
			t.Fatal(string(wire))
		}
	}
}

func auditConvertFrames(t *testing.T, source, target string, raw []string) []p.Value {
	t.Helper()
	return convertFixtureStream(t, shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, target), raw)
}

func TestAuditPublicMetadataStream(t *testing.T) {
	raw := []string{
		`{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"OK"},"logprobs":{"content":` + auditLogprobs + `},"finish_reason":null}]}`,
		`{"id":"r","created":1,"model":"m","choices":[{"index":0,"delta":{"annotations":` + citationChat + `},"finish_reason":"stop"}]}`,
	}
	frames := auditConvertFrames(t, Chat, Responses, raw)
	var texts []string
	var annotations, logs int
	for _, v := range frames {
		s := string(v.Bytes())
		texts = append(texts, s)
		if strings.Contains(s, `"type":"response.output_text.annotation.added"`) {
			annotations++
		}
		if strings.Contains(s, `"logprob":-0.125`) {
			logs++
		}
	}
	if annotations != 1 || logs < 2 {
		t.Fatal(annotations, logs, texts)
	}
	back := auditConvertFrames(t, Responses, Chat, texts)
	var output string
	for _, v := range back {
		output += string(v.Bytes())
	}
	if !strings.Contains(output, `https://example.org/`) || !strings.Contains(output, `-0.125`) {
		t.Fatal(output)
	}
}

func TestAuditEmptyMetadataDiagnosticAndUnknownCitation(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	r, err := from.DecodeResponse(t.Context(), []byte(auditResponses[Responses]), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Response(t.Context(), r, c.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON), sink)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range sink.Issues() {
		if i.Code == p.ConversionNormalized && strings.HasSuffix(i.Path, "/annotations") {
			found = true
		}
	}
	if !found {
		t.Fatal(sink.Issues())
	}
	raw := strings.Replace(auditResponses[Responses], `"annotations":[]`, `"annotations":[{"type":"file_citation","file_id":"secret"}]`, 1)
	r, err = from.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), nil); err == nil {
		t.Fatal("file citation silently discarded")
	}
}

func TestAuditTextMergeRebasesCitationsAndKeepsToolID(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","created_at":1,"status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"AB","annotations":[]}]},{"type":"function_call","id":"fc1","call_id":"call1","name":"f","arguments":"{}"},{"type":"message","id":"m2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":`+citationResponses+`}]}]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	r, err = c.Response(t.Context(), r, c.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON), sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"content":"ABOK"`, `"id":"call1"`, `"start_index":2`, `"end_index":4`} {
		if !strings.Contains(string(wire), want) {
			t.Fatal(want, string(wire))
		}
	}
	if len(sink.Issues()) == 0 {
		t.Fatal("boundary loss had no diagnostic")
	}
}

func TestAuditFinalWireLifecycleCannotBeBypassed(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	v, err := compiled.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	start := testValue(t, `{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","created_at":1,"model":"m","status":"in_progress","output":[]}}`)
	if err = v.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if err = v.Consume(t.Context(), testValue(t, `{"type":"response.output_text.delta","sequence_number":1,"output_index":0,"content_index":0,"item_id":"missing-start","delta":"bad"}`)); err == nil {
		t.Fatal("orphan content delta accepted")
	}
}

func TestAuditStreamingTextCitationOffsets(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	for _, strict := range []bool{false, true} {
		policy := p.DefaultConversionPolicy(to, from)
		if strict {
			policy.Mode = "strict"
		}
		c, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		route := c.VerificationRoute(from.Identity(), to.Identity(), p.SSE)
		sink := &p.DiagnosticSink{}
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Diagnostics: sink})
		defer options.State.Release()
		options, consume := bindFixtureOutput(t, to, options.WithConversion(c, route))
		encode := func(event p.Event) ([]p.Value, error) {
			projected, err := c.Event(t.Context(), event, route, sink)
			if err != nil {
				return nil, err
			}
			frames, err := to.EncodeFrames(t.Context(), projected, options)
			if err == nil {
				consume(frames)
			}
			return frames, err
		}
		if _, err := encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, Source: from.Identity(), Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}}); err != nil {
			t.Fatal(err)
		}
		event := func(typ p.EventType, id, txt string, metadata bool) p.Event {
			e := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: typ, Source: from.Identity(), ItemID: p.StringValue(id)}
			if typ == p.ItemStarted || typ == p.ItemSnapshot || typ == p.ItemFinished {
				e.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue(txt)}
			} else {
				e.Delta = p.StringValue(txt)
			}
			if metadata {
				e.Item.Metadata = []p.ResponseMetadata{{Meaning: "url-references", References: testValue(t, `[{"url":"https://example.test","title":"reference","start":0,"end":2}]`), Name: "annotations", Location: "content", Codec: Responses, SourceCodec: Responses, Path: "/output/1/content/0/annotations", Value: testValue(t, citationResponses)}}
			}
			return e
		}
		for _, e := range []p.Event{event(p.ItemStarted, "a", "", false), event(p.ItemDelta, "a", "\U0001f600", false), event(p.ItemFinished, "a", "\U0001f600", false)} {
			if _, err = encode(e); err != nil {
				t.Fatal(err)
			}
		}
		_, err = encode(event(p.ItemStarted, "b", "", false))
		if strict {
			if err == nil {
				t.Fatal("strict accepted text boundary loss")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = encode(event(p.ItemDelta, "b", "OK", false)); err != nil {
			t.Fatal(err)
		}
		frames, err := encode(event(p.ItemSnapshot, "b", "OK", true))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := p.EncodeValue(frames)
		if !strings.Contains(string(raw.Bytes()), `"start_index":2`) || !strings.Contains(string(raw.Bytes()), `"end_index":4`) {
			t.Fatal(string(raw.Bytes()))
		}
		if len(sink.Issues()) == 0 {
			t.Fatal("no boundary diagnostic")
		}
		// A duplicate full snapshot must neither add text nor rebase twice.
		again, err := encode(event(p.ItemSnapshot, "b", "OK", true))
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != 0 {
			t.Fatal("snapshot was delivered twice", again)
		}
		if _, err := encode(event(p.ItemFinished, "b", "OK", true)); err != nil {
			t.Fatal(err)
		}
		if _, err := encode(event(p.ItemFinished, "b", "OK", true)); err == nil {
			t.Fatal("finished item accepted a second completion")
		}
	}
}
