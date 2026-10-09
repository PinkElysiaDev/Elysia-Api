package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const citationChat = `[{"type":"url_citation","url_citation":{"url":"https://example.org/","title":"source","start_index":0,"end_index":2}}]`
const citationResponses = `[{"type":"url_citation","url":"https://example.org/","title":"source","start_index":0,"end_index":2}]`
const auditLogprobs = `[{"token":"OK","logprob":-0.125,"bytes":[79,75],"top_logprobs":[]}]`

func TestAuditLogprobDeltaAfterNullSnapshot(t *testing.T) {
	meta := p.ResponseMetadata{Name: "logprobs", Codec: Chat, Location: "choice", Value: testValue(t, "null")}
	next := meta
	next.Value = testValue(t, `{"content":`+auditLogprobs+`}`)
	merged := p.MergeNodeMetadata([]p.ResponseMetadata{meta}, []p.ResponseMetadata{next}, true)
	if len(merged) != 1 || !strings.Contains(string(merged[0].Value.Bytes()), `-0.125`) {
		t.Fatal(merged)
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
		r, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
		r, err = c.Response(t.Context(), r, c.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON), nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := to.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(string(wire), `https://example.org/`) || !strings.Contains(string(wire), `-0.125`) {
			t.Fatal(string(wire))
		}
		decoded, err := to.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
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
	from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, target)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	route := c.VerificationRoute(from.Identity(), to.Identity(), p.SSE)
	state := p.NewConversionEventState(c, route)
	opts := p.EvaluationContext{State: p.NewEvaluationState()}
	var result []p.Value
	emit := func(e p.Event) {
		converted, err := c.Event(t.Context(), e, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := to.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{converted}}, opts)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, out...)
	}
	for _, body := range raw {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, body), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.DeliveryFrameEvents(frame.Events) {
			batch, err := state.Push(e)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range batch {
				emit(v)
			}
		}
	}
	tail, err := state.Drain()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range tail {
		emit(e)
	}
	out, err := to.FinishEvents(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	result = append(result, out...)
	for _, v := range result {
		if err := to.ValidateWireOutput(p.EncodeEvent, v); err != nil {
			t.Fatal(err, string(v.Bytes()))
		}
	}
	return result
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
	r, err := from.DecodeResponse(t.Context(), []byte(auditResponses[Responses]), p.EvaluationContext{})
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
	r, err = from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Response(t.Context(), r, p.ConversionContext{}, nil); err == nil {
		t.Fatal("file citation silently discarded")
	}
}

func TestAuditMultipleChoicesKeepOwnership(t *testing.T) {
	a := module{name: Chat, family: "openai_chat"}
	r, err := a.decodeResponse(testValue(t, `{"id":"r","model":"m","created":1,"service_tier":"default","choices":[{"index":2,"message":{"role":"assistant","content":"first"},"finish_reason":"stop","logprobs":{"content":[]},"vendor_choice":"one"},{"index":4,"message":{"role":"assistant","content":"second"},"finish_reason":"length","logprobs":null,"vendor_choice":"two"}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := a.encodeResponse(r, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := wire.ReadObject()
	choices, _ := readArray(f["choices"])
	if len(choices) != 2 || !f["choiceExtensions"].IsZero() {
		t.Fatal(string(wire.Bytes()))
	}
	first, _ := choices[0].ReadObject()
	second, _ := choices[1].ReadObject()
	if first["vendor_choice"] != p.StringValue("one") || second["vendor_choice"] != p.StringValue("two") || second["finish_reason"] != p.StringValue("length") || second["index"] != testValue(t, `4`) {
		t.Fatal(string(wire.Bytes()))
	}
}

func TestAuditTextMergeRebasesCitationsAndKeepsToolID(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","created_at":1,"status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"AB","annotations":[]}]},{"type":"function_call","id":"fc1","call_id":"call1","name":"f","arguments":"{}"},{"type":"message","id":"m2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":`+citationResponses+`}]}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	r, err = c.Response(t.Context(), r, c.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON), sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), r, p.EvaluationContext{})
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
		event := func(typ p.EventType, id, txt string, metadata bool) p.Event {
			e := p.Event{SchemaVersion: 1, Type: typ, Source: from.Identity(), ItemID: p.StringValue(id)}
			if typ == p.ItemStarted || typ == p.ItemFinished {
				e.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue(txt)}
			} else {
				e.Delta = p.StringValue(txt)
			}
			if metadata {
				e.Item.Metadata = []p.ResponseMetadata{{Name: "annotations", Location: "content", Codec: Responses, SourceCodec: Responses, Path: "/output/1/content/0/annotations", Value: testValue(t, citationResponses)}}
			}
			return e
		}
		for _, e := range []p.Event{event(p.ItemStarted, "a", "", false), event(p.ItemDelta, "a", "\U0001f600", false), event(p.ItemFinished, "a", "\U0001f600", false)} {
			if _, err = c.Event(t.Context(), e, route, sink); err != nil {
				t.Fatal(err)
			}
		}
		_, err = c.Event(t.Context(), event(p.ItemStarted, "b", "", false), route, sink)
		if strict {
			if err == nil {
				t.Fatal("strict accepted text boundary loss")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Event(t.Context(), event(p.ItemDelta, "b", "OK", false), route, sink); err != nil {
			t.Fatal(err)
		}
		projected, err := c.Event(t.Context(), event(p.ItemFinished, "b", "OK", true), route, sink)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := p.EncodeValue(projected)
		if !strings.Contains(string(raw.Bytes()), `"start_index":2`) || !strings.Contains(string(raw.Bytes()), `"end_index":4`) {
			t.Fatal(string(raw.Bytes()))
		}
		if len(sink.Issues()) == 0 {
			t.Fatal("no boundary diagnostic")
		}
		// A duplicate full snapshot must neither add text nor rebase twice.
		again, err := c.Event(t.Context(), event(p.ItemFinished, "b", "OK", true), route, sink)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := p.EncodeValue(again)
		if string(encoded.Bytes()) != string(raw.Bytes()) {
			t.Fatal("snapshot accumulated twice")
		}
	}
}
