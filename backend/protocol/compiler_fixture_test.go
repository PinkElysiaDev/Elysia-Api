package protocol

import "context"

// evaluateFixturePlan isolates the DSL plan when a test uses arbitrary JSON
// instead of a core request. End-to-end fixtures use the public package runtime.
func evaluateFixturePlan(ctx context.Context, compiled *Compiled, direction Direction, input Value) (Value, error) {
	state := evaluation{ctx: ctx, input: input, root: input, budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	return compiled.mappings[direction].outputPlan.evaluate(state)
}

func testDocument(direction Direction) string {
	if isEventDirection(direction) { return "event" }
	return "test"
}

func generationFixtureEffects(direction Direction) []ProtocolSemanticEffect {
	if isEventDirection(direction) { return nil }
	return []ProtocolSemanticEffect{{ID: "task-boundary", Kind: "reject", Path: "/task", When: expressionPointer(Expression{Op: "present", Source: expressionPointer(readExpression("input", "/task"))}), Reason: "This generation fixture does not map asynchronous task state."}}
}

// testFrameContract keeps expression fixtures independent of provider shapes.
// Lifecycle-specific tests declare their own entities and external terminal rules.
func testFrameContract() *ProtocolStreamSpec {
	isUsage := Expression{Op: "equal", Items: []Expression{readExpression("input", "/event"), literalExpression("usage.updated")}}
	terminal := Expression{Op: "any", Items: []Expression{
		{Op: "equal", Items: []Expression{readExpression("input", "/event"), literalExpression("end")}},
		{Op: "equal", Items: []Expression{readExpression("input", "/event"), literalExpression("response.finished")}},
	}}
	return &ProtocolStreamSpec{
		Discriminator: "/event", DiscriminatorValue: expressionPointer(literalExpression("frame")),
		ResponseID: "/response", ResponseIDValue: expressionPointer(literalExpression("fixture")),
		Lifecycle: ProtocolStreamLifecycleSpec{EOF: "require_terminal", OpenEntities: "finish"},
		Events: map[string]ProtocolStreamEventSpec{"frame": {
			{Action: "stream_start", Implicit: true, When: &Expression{Op: "not", Source: &isUsage}},
			{Action: "stream_finish", When: &terminal},
			{Action: "observe", When: &isUsage, AfterTerminal: true},
		}},
	}
}
