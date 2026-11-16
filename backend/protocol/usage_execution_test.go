package protocol

import (
	"strings"
	"testing"
)

const executionEntryFixture = `{"purpose":"sampling","topLevelRelation":"included","input":{"count":10,"origin":"observed"},"output":{"count":2,"origin":"observed"},"cacheRead":{"count":3,"origin":"observed"},"cacheCreation":{"count":2,"origin":"observed"}}`

func executionValue(t *testing.T, raw string) Value {
	t.Helper()
	value, err := ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newExecutionUsage(t *testing.T, entry string) *Usage {
	t.Helper()
	return &Usage{
		Input: &Counter{Count: 10, Origin: ObservedCount}, Output: &Counter{Count: 2, Origin: ObservedCount},
		CacheRead: &Counter{Count: 3, Origin: ObservedCount}, CacheCreation: &Counter{Count: 2, Origin: ObservedCount},
		Executions: executionValue(t, "["+entry+"]"),
	}
}

func TestExecutionUsageContract(t *testing.T) {
	for _, speed := range []string{`null`, `"standard"`, `"fast"`} {
		usage := &Usage{ExecutionSpeed: executionValue(t, speed)}
		if err := ValidateUsageArithmetic(usage); err != nil {
			t.Fatal(speed, err)
		}
	}
	for _, speed := range []string{`"future"`, `false`, `0`, `{}`, `[]`} {
		if err := ValidateUsageArithmetic(&Usage{ExecutionSpeed: executionValue(t, speed)}); err == nil {
			t.Fatal("invalid execution speed accepted", speed)
		}
	}
	for _, ledger := range []string{`null`, `[]`, "[" + executionEntryFixture + "]"} {
		usage := newExecutionUsage(t, executionEntryFixture)
		usage.Executions = executionValue(t, ledger)
		if err := ValidateUsageArithmetic(usage); err != nil {
			t.Fatal(ledger, err)
		}
	}
	if err := ValidateUsageArithmetic(&Usage{}); err != nil {
		t.Fatal("missing execution observation became required", err)
	}
}

func TestExecutionUsageRejectsInvalidLedger(t *testing.T) {
	cases := map[string]string{
		"negative":                   strings.Replace(executionEntryFixture, `"count":10`, `"count":-1`, 1),
		"fraction":                   strings.Replace(executionEntryFixture, `"count":10`, `"count":1.5`, 1),
		"overflow":                   strings.Replace(executionEntryFixture, `"count":10`, `"count":9223372036854775808`, 1),
		"unknown-origin":             strings.Replace(executionEntryFixture, `"origin":"observed"`, `"origin":"inferred"`, 1),
		"unknown-purpose":            strings.Replace(executionEntryFixture, `"purpose":"sampling"`, `"purpose":"future"`, 1),
		"unknown-relation":           strings.Replace(executionEntryFixture, `"included"`, `"billable"`, 1),
		"missing-counter":            strings.Replace(executionEntryFixture, `,"cacheRead":{"count":3,"origin":"observed"}`, ``, 1),
		"cache-overflow":             strings.Replace(executionEntryFixture, `"count":10`, `"count":4`, 1),
		"recursive":                  strings.TrimSuffix(executionEntryFixture, "}") + `,"executions":[]}`,
		"unknown-counter-field":      strings.Replace(executionEntryFixture, `"count":10`, `"count":10,"extra":false`, 1),
		"invalid-model":              strings.TrimSuffix(executionEntryFixture, "}") + `,"model":false}`,
		"retention-overflow":         strings.TrimSuffix(executionEntryFixture, "}") + `,"cacheCreationByRetention":{"5m":{"count":3,"origin":"observed"}}}`,
		"retention-incomplete-total": strings.TrimSuffix(executionEntryFixture, "}") + `,"cacheCreationByRetention":{"5m":{"count":0,"origin":"observed"},"1h":{"count":0,"origin":"observed"}}}`,
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateUsageArithmetic(newExecutionUsage(t, entry)); err == nil {
				t.Fatal("invalid execution ledger accepted", entry)
			}
		})
	}
	usage := newExecutionUsage(t, executionEntryFixture)
	usage.Executions = executionValue(t, "["+executionEntryFixture+","+executionEntryFixture+"]")
	if err := ValidateUsageArithmetic(usage); err == nil {
		t.Fatal("included executions exceeded parent totals")
	}
}

func TestExecutionUsageDoesNotAddUnknownOrExcludedCounts(t *testing.T) {
	for _, relation := range []string{"excluded", "unknown"} {
		entry := strings.Replace(executionEntryFixture, `"included"`, `"`+relation+`"`, 1)
		entry = strings.Replace(entry, `"count":10`, `"count":9007199254740993`, 1)
		usage := newExecutionUsage(t, entry)
		if err := ValidateUsageArithmetic(usage); err != nil {
			t.Fatal(relation, err)
		}
		merged := MergeUsage(nil, usage)
		if merged.Input.Count != 10 || merged.Output.Count != 2 || merged.Total.Count != 12 || !strings.Contains(merged.Executions.JSONString(), "9007199254740993") {
			t.Fatal("execution ledger was billed into the parent or rounded", merged)
		}
	}
}

func TestExecutionUsageSnapshotsAndOwnership(t *testing.T) {
	initial := newExecutionUsage(t, executionEntryFixture)
	initial.ExecutionSpeed = StringValue("fast")
	initial.Details = map[string]Counter{"input.text_tokens": {Count: 10, Origin: ObservedCount}}
	clone := CloneUsage(initial)
	if clone.Total != nil {
		t.Fatal("ownership snapshot inferred a missing total")
	}
	merged := MergeUsage(initial, &Usage{Output: &Counter{Count: 3, Origin: ObservedCount}})
	if merged.ExecutionSpeed != initial.ExecutionSpeed || merged.Executions != initial.Executions || merged.Output.Count != 3 {
		t.Fatal("counter-only update erased execution facts", merged)
	}
	empty := executionValue(t, `[]`)
	cleared := MergeUsage(merged, &Usage{ExecutionSpeed: executionValue(t, `null`), Executions: empty})
	if !cleared.ExecutionSpeed.IsNull() || cleared.Executions != empty || cleared.Input.Count != 10 {
		t.Fatal("explicit replacement was appended or treated as absent", cleared)
	}
	initial.Input.Count = 99
	initial.Details["input.text_tokens"] = Counter{Count: 99, Origin: ObservedCount}
	initial.Executions = empty
	initial.ExecutionSpeed = StringValue("standard")
	if clone.Input.Count != 10 || clone.Details["input.text_tokens"].Count != 10 || clone.Executions == empty || !clone.ExecutionSpeed.IsString("fast") {
		t.Fatal("source edits changed an owned snapshot", clone)
	}
	if CloneUsage(nil) != nil {
		t.Fatal("nil usage snapshot became present")
	}
}

func TestExecutionUsageParticipatesInWireEquivalenceAndLimits(t *testing.T) {
	usage := newExecutionUsage(t, executionEntryFixture)
	usage.ExecutionSpeed = StringValue("fast")
	baseline := fixtureValue(t, comparableWireUsage(usage))
	for _, change := range []func(*Usage){
		func(value *Usage) { value.ExecutionSpeed = Value{} },
		func(value *Usage) { value.ExecutionSpeed = executionValue(t, `null`) },
		func(value *Usage) { value.Executions = executionValue(t, `[]`) },
		func(value *Usage) {
			value.Executions = executionValue(t, "["+strings.TrimSuffix(executionEntryFixture, "}")+`,"model":"different"}]`)
		},
	} {
		changed := CloneUsage(usage)
		change(changed)
		if fixtureValue(t, comparableWireUsage(changed)) == baseline {
			t.Fatal("execution edit reused stale native equivalence", changed)
		}
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Usage: usage}
	limits := DefaultLimits()
	limits.BufferBytes = usage.Executions.Len() - 1
	if err := checkSemanticValue(response, limits); err == nil {
		t.Fatal("execution ledger bypassed the semantic byte budget")
	}
	limits = DefaultLimits()
	limits.Nodes = 10
	if err := checkSemanticValue(response, limits); err == nil {
		t.Fatal("execution ledger bypassed the semantic node budget")
	}
}
