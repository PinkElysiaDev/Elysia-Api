package protocol

import (
	"context"
	"errors"
	"testing"
)

func TestCheckLinksOrderedIdentityAndGroups(t *testing.T) {
	for _, fixture := range []struct {
		name, policy, records, path string
		isRequired                  bool
	}{
		{"decoded-identity", "key", `[{"kind":"define","key":"c","path":"/calls/0/id"},{"kind":"resolve","key":"\u0063","path":"/results/0/id"}]`, "", true},
		{"parallel-results", "key", `[{"kind":"define","key":"a","path":"/calls/0/id"},{"kind":"define","key":"b","path":"/calls/1/id"},{"kind":"resolve","key":"b","path":"/results/0/id"},{"kind":"resolve","key":"a","path":"/results/1/id"}]`, "", true},
		{"forward-result", "key", `[{"kind":"resolve","key":"c","path":"/results/0/id"},{"kind":"define","key":"c","path":"/calls/0/id"}]`, "/results/0/id", false},
		{"duplicate-definition-after-result", "key", `[{"kind":"define","key":"c","path":"/calls/0/id"},{"kind":"resolve","key":"c","path":"/results/0/id"},{"kind":"define","key":"\u0063","path":"/calls/1/id"}]`, "/calls/1/id", false},
		{"duplicate-result", "key", `[{"kind":"define","key":"c","path":"/calls/0/id"},{"kind":"resolve","key":"c","path":"/results/0/id"},{"kind":"resolve","key":"c","path":"/results/1/id"}]`, "/results/1/id", true},
		{"required-result", "key", `[{"kind":"define","key":"c","path":"/calls/0/id"}]`, "/calls/0/id", true},
		{"optional-result", "key", `[{"kind":"define","key":"c","path":"/calls/0/id"}]`, "", false},
		{"resolved-boundary", "key", `[{"kind":"barrier","path":"/messages/0/role"},{"kind":"define","key":"c","path":"/calls/0/id"},{"kind":"resolve","key":"c","path":"/results/0/id"},{"kind":"barrier","path":"/messages/2/role"}]`, "", true},
		{"pending-boundary", "key", `[{"kind":"define","key":"c","path":"/calls/0/id"},{"kind":"barrier","path":"/messages/1/role"}]`, "/messages/1/role", true},
		{"anonymous-rounds", "unique_group", `[{"kind":"define","group":"f","path":"/calls/0/name"},{"kind":"resolve","group":"f","path":"/results/0/name"},{"kind":"define","group":"f","path":"/calls/1/name"},{"kind":"resolve","group":"\u0066","path":"/results/1/name"}]`, "", true},
		{"ambiguous-group", "unique_group", `[{"kind":"define","key":"a","group":"f","path":"/calls/0/id"},{"kind":"define","key":"b","group":"f","path":"/calls/1/id"},{"kind":"resolve","group":"f","path":"/results/0/name"}]`, "/results/0/name", true},
		{"explicit-result-removes-group-candidate", "unique_group", `[{"kind":"define","key":"a","group":"f","path":"/calls/0/id"},{"kind":"define","key":"b","group":"f","path":"/calls/1/id"},{"kind":"resolve","key":"a","path":"/results/0/id"},{"kind":"resolve","group":"f","path":"/results/1/name"}]`, "", true},
		{"missing-group-definition", "unique_group", `[{"kind":"resolve","group":"f","path":"/results/0/name"}]`, "/results/0/name", false},
		{"null-is-not-missing", "unique_group", `[{"kind":"define","key":null,"group":"f","path":"/calls/0/id"}]`, "/calls/0/id", false},
		{"empty-key", "key", `[{"kind":"define","key":"","path":"/calls/0/id"}]`, "/calls/0/id", false},
		{"null-group", "unique_group", `[{"kind":"define","group":null,"path":"/calls/0/name"}]`, "/calls/0/name", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{
				Op: "check_links", Source: expressionPointer(readExpression("input", "")), Policy: fixture.policy, Required: fixture.isRequired,
			}))
			input := parseTestValue(t, fixture.records)
			value, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, root: input, budget: &evaluationBudget{limits: DefaultLimits()}})
			if fixture.path == "" {
				if err != nil || value.raw != "true" {
					t.Fatal("valid associations rejected", err, value)
				}
			} else {
				var failure *ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != InvalidAssociation || failure.Issues[0].Path != fixture.path {
					t.Fatal("invalid association lost its wire location", err)
				}
			}
			if input.raw != fixture.records {
				t.Fatal("association checking changed its input")
			}
		})
	}
}

func TestCheckLinksBudgetAndCancellation(t *testing.T) {
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{
		Op: "check_links", Source: expressionPointer(readExpression("input", "")), Policy: "key",
	}))
	input := parseTestValue(t, `[{"kind":"define","key":"a","path":"/calls/0/id"},{"kind":"define","key":"b","path":"/calls/1/id"}]`)
	limits := DefaultLimits()
	limits.StateItems = 1
	_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}})
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Domain != "links" || limit.Metric != "items" {
		t.Fatal("association definitions escaped their entry bound", err)
	}
	_, err = plan.evaluate(evaluation{ctx: &outputPlanCancelContext{Context: t.Context(), remaining: 3}, input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("association traversal ignored cancellation", err)
	}
}
