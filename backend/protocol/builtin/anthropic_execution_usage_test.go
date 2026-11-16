package builtin

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	advisorIterationsFixture = `[{"type":"message","model":null,"input_tokens":412,"output_tokens":89,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":null},{"type":"advisor_message","model":"advisor","input_tokens":823,"output_tokens":1612,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},{"type":"message","input_tokens":1348,"output_tokens":442,"cache_read_input_tokens":412,"cache_creation_input_tokens":0,"cache_creation":{}}]`
	advisorUsageFixture      = `{"input_tokens":1760,"output_tokens":531,"cache_read_input_tokens":412,"cache_creation_input_tokens":0,"speed":"fast","iterations":` + advisorIterationsFixture + `}`
	canonicalAdvisorFixture  = `[{"purpose":"sampling","model":null,"topLevelRelation":"included","input":{"count":412,"origin":"observed"},"output":{"count":89,"origin":"observed"},"cacheRead":{"count":0,"origin":"observed"},"cacheCreation":{"count":0,"origin":"observed"},"cacheCreationByRetention":null},{"purpose":"advisor","model":"advisor","topLevelRelation":"excluded","input":{"count":823,"origin":"observed"},"output":{"count":1612,"origin":"observed"},"cacheRead":{"count":0,"origin":"observed"},"cacheCreation":{"count":0,"origin":"observed"}},{"purpose":"sampling","topLevelRelation":"included","input":{"count":1760,"origin":"observed"},"output":{"count":442,"origin":"observed"},"cacheRead":{"count":412,"origin":"observed"},"cacheCreation":{"count":0,"origin":"observed"},"cacheCreationByRetention":{}}]`
)

func buildExecutionResponse(usage string) string {
	return `{"id":"r","type":"message","role":"assistant","model":"executor","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":` + usage + `}`
}

func checkEncodedExecutionUsage(t *testing.T, compiled *p.Compiled, response *p.Response, expected string) {
	t.Helper()
	wire, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := wire.ReadObject()
	sameJSON(t, fields["usage"].Bytes(), expected)
}

func TestAnthropicExecutionUsageJSONRebuildAndEdit(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	response, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(advisorUsageFixture)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	usage := response.Usage
	if !usage.ExecutionSpeed.IsString("fast") || usage.Input.Count != 2172 || usage.Output.Count != 531 || usage.Total.Count != 2703 {
		t.Fatal("advisor usage changed parent accounting", usage)
	}
	sameJSON(t, usage.Executions.Bytes(), canonicalAdvisorFixture)
	response.Native = nil
	checkEncodedExecutionUsage(t, compiled, response, advisorUsageFixture)
	usage.ExecutionSpeed = p.StringValue("standard")
	usage.Executions = testValue(t, strings.Replace(canonicalAdvisorFixture, `"model":"advisor"`, `"model":"edited-advisor"`, 1))
	expected := strings.Replace(strings.Replace(advisorUsageFixture, `"speed":"fast"`, `"speed":"standard"`, 1), `"model":"advisor"`, `"model":"edited-advisor"`, 1)
	checkEncodedExecutionUsage(t, compiled, response, expected)
	edited := strings.Replace(canonicalAdvisorFixture, `"purpose":"advisor","model":"advisor"`, `"purpose":"context-compaction"`, 1)
	usage.Executions = testValue(t, strings.Replace(edited, `"count":823`, `"count":9007199254740993`, 1))
	expected = strings.Replace(advisorUsageFixture, `"type":"advisor_message","model":"advisor","input_tokens":823`, `"type":"compaction","input_tokens":9007199254740993`, 1)
	expected = strings.Replace(expected, `"speed":"fast"`, `"speed":"standard"`, 1)
	checkEncodedExecutionUsage(t, compiled, response, expected)
	usage.Executions = testValue(t, strings.Replace(canonicalAdvisorFixture, `"topLevelRelation":"excluded"`, `"topLevelRelation":"unknown"`, 1))
	_, err = compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	requireContextIssue(t, err, p.UnsupportedCapability, "/usage/executions")
}

func TestAnthropicExecutionUsageFallbackRelationsAndRetention(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	iterations := `[{"type":"message","model":"same","input_tokens":9007199254740993,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},{"type":"compaction","input_tokens":50,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":1}},{"type":"fallback_message","model":"same","input_tokens":10,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`
	usage := `{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"iterations":` + iterations + `}`
	response, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(usage)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	var ledger []p.Object
	if err := response.Usage.Executions.Decode(&ledger); err != nil {
		t.Fatal(err)
	}
	for index, relation := range []string{"unknown", "excluded", "included"} {
		if !ledger[index]["topLevelRelation"].IsString(relation) {
			t.Fatal("same model was mistaken for an execution attempt", ledger)
		}
	}
	if response.Usage.Input.Count != 10 || response.Usage.Total.Count != 12 || !strings.Contains(response.Usage.Executions.JSONString(), "9007199254740993") {
		t.Fatal("fallback attempts were rounded or billed into parent totals", response.Usage)
	}
	response.Native = nil
	checkEncodedExecutionUsage(t, compiled, response, usage)
	large := strings.Replace(strings.Replace(usage, "9007199254740993", "9223372036854775807", 1), `"output_tokens":0`, `"output_tokens":1`, 1)
	if _, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(large)), fixtureOptions(p.EvaluationContext{})); err != nil {
		t.Fatal("execution counters were forced into an unnecessary combined total", err)
	}
}

func TestAnthropicExecutionUsagePresenceAndUnknownOwnership(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, fields := range []string{``, `,"speed":null`, `,"speed":"standard"`, `,"iterations":null`, `,"iterations":[]`} {
		usage := `{"input_tokens":10,"output_tokens":2` + fields + `}`
		response, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(usage)), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		wireUsage, _ := testValue(t, usage).ReadObject()
		if response.Usage == nil || response.Usage.Executions != wireUsage["iterations"] || response.Usage.ExecutionSpeed != wireUsage["speed"] {
			t.Fatal("missing, null or empty execution observations changed", response.Usage)
		}
		response.Native = nil
		checkEncodedExecutionUsage(t, compiled, response, usage)
	}
	for _, iterations := range []string{
		`[{"type":"future","observation":9007199254740993}]`,
		`[{"type":"future","cache_creation":false}]`,
		`[{"type":"future","cache_creation":[]}]`,
		strings.Replace(advisorIterationsFixture, `"output_tokens":89`, `"output_tokens":89,"extra":false`, 1),
		strings.Replace(advisorIterationsFixture, `"cache_creation":null`, `"cache_creation":{"future":0}`, 1),
	} {
		usage := `{"input_tokens":1760,"output_tokens":531,"cache_read_input_tokens":412,"cache_creation_input_tokens":0,"iterations":` + iterations + `}`
		response, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(usage)), fixtureOptions(p.EvaluationContext{}))
		if err != nil || !response.Usage.Executions.IsZero() {
			t.Fatal("unknown execution acquired canonical meaning", response, err)
		}
		response.Native = nil
		checkEncodedExecutionUsage(t, compiled, response, usage)
		target := testCompiled(t, Chat)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, compiled))
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, compiled.Identity()), nil)
		if err == nil {
			_, err = target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("unknown source execution ledger was discarded")
		}
	}
}

func TestAnthropicExecutionUsageSpecBoundary(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, testCase := range []struct {
		fields string
		path   string
	}{
		{`,"speed":false`, "/usage/speed"},
		{`,"speed":"future"`, "/usage/speed"},
		{`,"iterations":{}`, "/usage/iterations"},
		{`,"iterations":[{"type":"message","input_tokens":-1,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`, "/usage/iterations/0/input_tokens"},
		{`,"iterations":[{"type":"message","input_tokens":1.5,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`, "/usage/iterations/0/input_tokens"},
		{`,"iterations":[{"type":"message","input_tokens":9223372036854775808,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`, "/usage/iterations/0/input_tokens"},
		{`,"iterations":[{"type":"advisor_message","input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`, "/usage/iterations/0/model"},
		{`,"iterations":[{"type":"fallback_message","model":null,"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}]`, "/usage/iterations/0/model"},
	} {
		_, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(`{"input_tokens":10,"output_tokens":2`+testCase.fields+`}`)), fixtureOptions(p.EvaluationContext{}))
		requireContextIssue(t, err, p.UpstreamContractViolation, testCase.path)
	}
}

func TestExecutionUsageTargetProjectionPolicies(t *testing.T) {
	for _, targetName := range []string{Chat, Responses, Gemini} {
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(targetName+"/"+mode, func(t *testing.T) {
				checkExecutionProjection(t, targetName, mode)
			})
		}
	}
}

func checkExecutionProjection(t *testing.T, targetName, mode string) {
	t.Helper()
	source, target := testCompiled(t, Anthropic), testCompiled(t, targetName)
	response, err := source.DecodeResponse(t.Context(), []byte(buildExecutionResponse(advisorUsageFixture)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.EncodeValue(response)
	if err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(target, source)
	if mode == "strict" {
		policy.Mode = mode
	}
	for index, rule := range policy.Rules {
		if mode == "disabled" && strings.Contains(rule.ID, "-execution-") {
			policy.Rules[index].Enabled = false
		}
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, source.Identity()), sink)
	if mode == "strict" {
		requireContextIssue(t, err, p.ConversionRejected, "/usage/executionSpeed")
	} else {
		if err != nil {
			t.Fatal(err)
		}
		wire, err := target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		if mode == "disabled" {
			requireContextIssue(t, err, p.UnsupportedCapability, "/usage/executionSpeed")
		} else {
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := target.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil || decoded.Usage.Input.Count != 2172 || decoded.Usage.Output.Count != 531 {
				t.Fatal("client projection changed parent accounting", decoded, err)
			}
			for _, field := range []string{"executionSpeed", "executions"} {
				hasIssue := false
				for _, issue := range sink.Issues() {
					if issue.Path == "/usage/"+field && strings.HasPrefix(issue.RuleID, "response-execution-") && issue.PolicyHash == conversion.Hash && issue.PolicyRevision != "" && issue.Fidelity == "lossy_compatible" {
						hasIssue = true
					}
				}
				if !hasIssue {
					t.Fatal("missing named execution disclosure diagnostic", field, sink.Issues())
				}
			}
		}
	}
	after, err := p.EncodeValue(response)
	if err != nil || after != before {
		t.Fatal("client projection changed source execution facts", err)
	}
}

func executionSpeedFrames(speed string) []string {
	return []string{
		`{"type":"message_start","message":{"id":"r","type":"message","model":"m","role":"assistant","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0,"speed":` + speed + `}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}
}

func TestAnthropicExecutionUsageWithoutLedgerStaysAbsent(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, missingUsage := range []string{"none", "start", "delta"} {
		t.Run(missingUsage, func(t *testing.T) {
			raws := executionSpeedFrames(`"fast"`)
			raws[0] = strings.Replace(raws[0], `,"speed":"fast"`, "", 1)
			wantOutput := int64(2)
			if missingUsage == "start" {
				raws[0] = strings.Replace(raws[0], `,"usage":{"input_tokens":10,"output_tokens":0}`, "", 1)
			} else if missingUsage == "delta" {
				raws[4] = strings.Replace(raws[4], `,"usage":{"output_tokens":2}`, "", 1)
				wantOutput = 0
			}
			frames, response := decodePackageStream(t, compiled, raws, p.Scope{})
			usage := response.Usage
			if usage == nil || usage.Output == nil || usage.Output.Count != wantOutput || !usage.Executions.IsZero() || !usage.ExecutionSpeed.IsZero() {
				t.Fatal("ordinary usage acquired execution observations or changed counters", usage)
			}
			if missingUsage == "start" {
				if usage.Input != nil {
					t.Fatal("missing start usage invented input counters", usage)
				}
			} else if usage.Input == nil || usage.Input.Count != 10 {
				t.Fatal("ordinary start usage lost input counters", usage)
			}
			if missingUsage == "start" {
				checkMissingStartUsageRejected(t, compiled, frames[0])
				return
			}
			checkStreamExecutionFieldsAbsent(t, encodePackageStream(t, compiled, frames, p.Scope{}, false))
		})
	}
}

func checkMissingStartUsageRejected(t *testing.T, compiled *p.Compiled, frame *p.EventFrame) {
	t.Helper()
	validation, err := compiled.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}).WithOutputStream(validation)
	defer options.State.Release()
	output, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options)
	if err != nil || len(output) != 1 {
		t.Fatal("expected one start frame before target validation", output, err)
	}
	requireContextIssue(t, validation.Consume(t.Context(), output[0]), p.ConversionRejected, "/message/usage")
}

func checkStreamExecutionFieldsAbsent(t *testing.T, frames []string) {
	t.Helper()
	for _, raw := range frames {
		fields, _ := testValue(t, raw).ReadObject()
		wireUsage := fields["usage"]
		if fields["type"].IsString("message_start") {
			message, _ := fields["message"].ReadObject()
			wireUsage = message["usage"]
		}
		if wireUsage.IsZero() {
			continue
		}
		observations, _ := wireUsage.ReadObject()
		if !observations["iterations"].IsZero() || !observations["speed"].IsZero() {
			t.Fatal("no-Native stream rebuild invented execution observations", raw)
		}
	}
}

func TestAnthropicExecutionSpeedStreamRebuild(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, speed := range []string{`null`, `"standard"`, `"fast"`} {
		frames, response := decodePackageStream(t, compiled, executionSpeedFrames(speed), p.Scope{})
		if response.Usage.ExecutionSpeed != testValue(t, speed) || response.Usage.Input.Count != 10 || response.Usage.Output.Count != 2 {
			t.Fatal("later counters erased start speed", response.Usage)
		}
		wire := encodePackageStream(t, compiled, frames, p.Scope{}, false)
		for _, frame := range wire {
			fields, _ := testValue(t, frame).ReadObject()
			if fields["type"].IsString("message_start") {
				message, _ := fields["message"].ReadObject()
				usage, _ := message["usage"].ReadObject()
				if usage["speed"] != testValue(t, speed) {
					t.Fatal("no-Native rebuild lost start speed", frame)
				}
			} else if fields["type"].IsString("message_delta") && strings.Contains(frame, `"speed"`) {
				t.Fatal("speed leaked into an undeclared delta position", frame)
			}
		}
		for _, target := range []string{Chat, Responses, Gemini} {
			for _, frame := range auditConvertFrames(t, Anthropic, target, executionSpeedFrames(speed)) {
				if strings.Contains(frame.JSONString(), `"speed"`) || strings.Contains(frame.JSONString(), `"service_tier"`) {
					t.Fatal("execution speed was mapped to a foreign wire option", target, frame)
				}
			}
		}
	}
}

func TestAnthropicExecutionDeltaSpeedRemainsSourceOwned(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, speed := range []string{`null`, `"standard"`, `{"future":true}`} {
		raws := executionSpeedFrames(`"fast"`)
		raws[4] = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"speed":` + speed + `}}`
		frames, response := decodePackageStream(t, compiled, raws, p.Scope{})
		if !response.Usage.ExecutionSpeed.IsString("fast") {
			t.Fatal("undeclared delta speed replaced the observed start speed", response.Usage)
		}
		event := frames[4].Events[0]
		if event.Unmapped == nil || event.Unmapped.Source.Protocol != compiled.Identity() || event.Unmapped.Source.Direction != p.DecodeEvent || event.Unmapped.Source.Path != "" || event.Unmapped.Value != testValue(t, raws[4]) {
			t.Fatal("undeclared delta speed lost original frame ownership", event)
		}
		if replayed := encodePackageStream(t, compiled, frames, p.Scope{}, true); !slices.Equal(replayed, raws) {
			t.Fatal("original frame replay changed unknown delta speed", replayed)
		}
		for _, targetName := range []string{Anthropic, Chat, Responses, Gemini} {
			target := testCompiled(t, targetName)
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			options, _ = bindFixtureOutput(t, target, options)
			candidate := event
			candidate.Native = nil
			candidate.Usage = p.CloneUsage(candidate.Usage)
			if candidate.Usage != nil {
				candidate.Usage.ExecutionSpeed = p.Value{}
			}
			_, err := target.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{candidate}}, options)
			options.State.Release()
			if err == nil || !strings.Contains(err.Error(), "/unmapped") {
				t.Fatal("source-owned delta speed crossed its native boundary", targetName, err)
			}
		}
	}
}

func TestAnthropicIterationsStreamRemainsUnsupported(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, iterations := range []string{`null`, `[]`, advisorIterationsFixture, `[` + strings.Trim(advisorIterationsFixture, "[]") + `,` + strings.Trim(advisorIterationsFixture, "[]") + `]`} {
		for _, isStart := range []bool{false, true} {
			t.Run(fmt.Sprintf("start=%t/%s", isStart, iterations), func(t *testing.T) {
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				defer options.State.Release()
				body := strings.Replace(executionSpeedFrames(`"fast"`)[0], `"speed":"fast"`, `"speed":"fast","iterations":`+iterations, 1)
				path := "/message/usage/iterations"
				if !isStart {
					if _, err := compiled.DecodeFrame(t.Context(), testValue(t, executionSpeedFrames(`"fast"`)[0]), options); err != nil {
						t.Fatal(err)
					}
					body = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"iterations":` + iterations + `}}`
					path = "/usage/iterations"
				}
				_, err := compiled.DecodeFrame(t.Context(), testValue(t, body), options)
				requireContextIssue(t, err, p.UnsupportedCapability, path)
			})
		}
	}
}

func TestExecutionUsageEventProjectionPolicies(t *testing.T) {
	for _, targetName := range []string{Chat, Responses, Gemini} {
		for _, field := range []string{"executionSpeed", "executions"} {
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				t.Run(targetName+"/"+field+"/"+mode, func(t *testing.T) {
					checkExecutionEventProjection(t, targetName, field, mode)
				})
			}
		}
	}
}

func checkExecutionEventProjection(t *testing.T, targetName, field, mode string) {
	t.Helper()
	source, target := testCompiled(t, Anthropic), testCompiled(t, targetName)
	ruleID := "event-execution-usage-disclosure"
	usage := &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, Output: &p.Counter{Count: 2, Origin: p.ObservedCount}}
	if field == "executionSpeed" {
		ruleID = "event-execution-speed-disclosure"
		usage.ExecutionSpeed = testValue(t, `null`)
	} else {
		usage.Executions = testValue(t, `[]`)
	}
	policy := p.DefaultConversionPolicy(target, source)
	hasRule := false
	for _, rule := range policy.Rules {
		if rule.ID == ruleID {
			hasRule = true
			if rule.Action != "core_controls" {
				t.Fatal("execution policy used a vendor action", rule)
			}
			rule.Enabled = mode != "disabled"
			policy.Rules = []p.ConversionRule{rule}
			break
		}
	}
	if !hasRule {
		t.Fatal("missing execution event disclosure rule", ruleID)
	}
	if mode == "strict" {
		policy.Mode = mode
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	event := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.UsageUpdated, ResponseID: p.StringValue("r"), Usage: usage}
	before, err := p.EncodeValue(event)
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Event(t.Context(), event, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
	path := "/usage/" + field
	if mode == "strict" {
		requireContextIssue(t, err, p.ConversionRejected, path)
	} else {
		if err != nil {
			t.Fatal(err)
		}
		if mode == "disabled" {
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			_, err = target.EncodeEvent(t.Context(), projected, options)
			requireContextIssue(t, err, p.UnsupportedCapability, path)
		} else if !projected.Usage.ExecutionSpeed.IsZero() || !projected.Usage.Executions.IsZero() || projected.Usage.Input.Count != 10 || len(sink.Issues()) != 1 || sink.Issues()[0].RuleID != ruleID || sink.Issues()[0].Path != path || sink.Issues()[0].Fidelity != "lossy_compatible" {
			t.Fatal("event execution projection lost its explicit disposition", projected, sink.Issues())
		}
	}
	after, err := p.EncodeValue(event)
	if err != nil || after != before {
		t.Fatal("event projection mutated source observations", err)
	}
}

func TestAnthropicExecutionSpeedEditsAndLateGuard(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	frames, _ := decodePackageStream(t, compiled, executionSpeedFrames(`"fast"`), p.Scope{})
	for _, frame := range frames {
		for index := range frame.Events {
			event := &frame.Events[index]
			if event.Usage != nil {
				event.Usage = p.CloneUsage(event.Usage)
				event.Usage.ExecutionSpeed = p.StringValue("standard")
			}
			if event.Response != nil && event.Response.Usage != nil {
				response := *event.Response
				response.Usage = p.CloneUsage(response.Usage)
				response.Usage.ExecutionSpeed = p.StringValue("standard")
				event.Response = &response
			}
		}
	}
	encoded := strings.Join(encodePackageStream(t, compiled, frames, p.Scope{}, false), "\n")
	if !strings.Contains(encoded, `"speed":"standard"`) || strings.Contains(encoded, `"speed":"fast"`) {
		t.Fatal("edited core speed reused stale source bytes", encoded)
	}
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	options, consume := bindFixtureOutput(t, compiled, options)
	output, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: frames[0].Events}, options)
	if err != nil {
		t.Fatal(err)
	}
	consume(output)
	late := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.UsageUpdated, ResponseID: p.StringValue("r"), Usage: &p.Usage{ExecutionSpeed: p.StringValue("fast")}}
	_, err = compiled.EncodeEvent(t.Context(), late, options)
	requireContextIssue(t, err, p.UnsupportedCapability, "/usage/executionSpeed")
	late.Usage = &p.Usage{Executions: testValue(t, `null`)}
	_, err = compiled.EncodeEvent(t.Context(), late, options)
	requireContextIssue(t, err, p.UnsupportedCapability, "/usage/executions")
}

func TestAnthropicExecutionNullUsageKeepsSourceBoundary(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, isStart := range []bool{false, true} {
		t.Run(fmt.Sprintf("start=%t", isStart), func(t *testing.T) {
			raws := executionSpeedFrames(`"fast"`)
			if isStart {
				raws[0] = strings.Replace(raws[0], `"usage":{"input_tokens":10,"output_tokens":0,"speed":"fast"}`, `"usage":null`, 1)
			} else {
				raws[4] = strings.Replace(raws[4], `"usage":{"output_tokens":2}`, `"usage":null`, 1)
			}
			_, response := decodePackageStream(t, compiled, raws, p.Scope{})
			usage := response.Usage
			if usage == nil || !usage.Executions.IsZero() {
				t.Fatal("nullable usage acquired an execution ledger", usage)
			}
			if isStart {
				if !usage.ExecutionSpeed.IsZero() || usage.Input != nil || usage.Output == nil || usage.Output.Count != 2 {
					t.Fatal("null start usage fabricated an earlier observation", usage)
				}
			} else if !usage.ExecutionSpeed.IsString("fast") || usage.Input == nil || usage.Input.Count != 10 || usage.Output == nil || usage.Output.Count != 0 {
				t.Fatal("null delta usage changed prior observations", usage)
			}
		})
	}
}

func TestAnthropicExecutionSpeedTracksEmittedStartUsage(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	frames, _ := decodePackageStream(t, compiled, executionSpeedFrames(`"fast"`), p.Scope{})
	for _, nestedSpeed := range []string{`"standard"`, `null`} {
		t.Run(nestedSpeed, func(t *testing.T) {
			start := frames[0].Events[0]
			start.Native = nil
			start.Usage = p.CloneUsage(start.Usage)
			start.Usage.ExecutionSpeed = p.StringValue("fast")
			response := *start.Response
			response.Native = nil
			response.Usage = &p.Usage{ExecutionSpeed: testValue(t, nestedSpeed)}
			start.Response = &response
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			options, consume := bindFixtureOutput(t, compiled, options)
			output, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{start}}, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(output) != 1 {
				t.Fatal("start did not emit one message_start", output)
			}
			fields, _ := output[0].ReadObject()
			message, _ := fields["message"].ReadObject()
			usage, _ := message["usage"].ReadObject()
			if usage["speed"] != testValue(t, nestedSpeed) {
				t.Fatal("message_start changed existing nested-over-root usage precedence", output)
			}
			consume(output)
			repeated := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.UsageUpdated, ResponseID: p.StringValue("r"),
				Usage: &p.Usage{ExecutionSpeed: testValue(t, nestedSpeed)}}
			if _, err := compiled.EncodeEvent(t.Context(), repeated, options); err != nil {
				t.Fatal("the emitted speed was rejected as a new disclosure", err)
			}
			repeated.Usage = &p.Usage{ExecutionSpeed: p.StringValue("fast")}
			_, err = compiled.EncodeEvent(t.Context(), repeated, options)
			requireContextIssue(t, err, p.UnsupportedCapability, "/usage/executionSpeed")
		})
	}
}
