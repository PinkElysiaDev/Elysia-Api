package protocol

import "testing"

func TestPackageEventVerificationChecksWireLifecycle(t *testing.T) {
	for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
		for _, fault := range []string{"sequence", "EOF"} {
			t.Run(string(direction)+"/"+fault, func(t *testing.T) {
				document := eventProtocolPackage(t)
				mapping := document.Mapping.Directions[direction]
				code, path := UpstreamContractViolation, "/seq"
				var inputs []Value
				if direction == DecodeEvent {
					inputs = []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ResponseFinished, 0, "")}
					// The semantic replay cannot see a bad wire sequence omitted by
					// the mapping, or a missing terminal replaced with a literal.
					mapping.Program.Fields["sequence"] = Expression{Op: "omit"}
					if fault == "EOF" {
						inputs = inputs[:1]
						mapping.Program.Fields["type"] = literalExpression(ResponseFinished)
					}
				} else {
					code = ConversionRejected
					inputs = []Value{
						fixtureValue(t, Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 0)}),
						fixtureValue(t, Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 1)}),
					}
					if fault == "sequence" {
						mapping.Program.Fields["seq"] = literalExpression(0)
					} else {
						isTerminal := Expression{Op: "equal", Items: []Expression{readExpression("input", "/type"), literalExpression(ResponseFinished)}}
						mapping.Program.Fields["kind"] = Expression{Op: "if", When: &isTerminal, Then: expressionPointer(literalExpression(UsageUpdated)), Otherwise: expressionPointer(readExpression("input", "/type"))}
						mapping.Program.Fields["usage"] = objectExpression(map[string]Expression{"input": literalExpression(1)})
					}
				}
				if fault == "EOF" {
					path = "/kind"
				}
				document.Mapping.Directions[direction] = mapping
				sample := Sample{ID: "wire-lifecycle", Direction: direction, Sequence: true, Input: fixtureValue(t, inputs), ExpectedIssue: code}
				document.Mapping.Samples = []Sample{sample}
				compiled := compilePackageForTest(t, document)
				_, err := executeEventFixture(t.Context(), compiled, sample)
				assertPackageRuntimeIssue(t, err, code, path)
				report := Verify(t.Context(), compiled)
				if len(report.Checks) != 1 || !report.Checks[0].Passed {
					t.Fatal("offline verification bypassed the independent wire lifecycle", report.Checks, report.Issues)
				}
			})
		}
	}
}
