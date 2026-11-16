package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func headerControlFixture(t *testing.T) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	matches := Expression{Op: "filter", Source: expressionPointer(readExpression("input", "/x-protocol-mode")),
		When: &Expression{Op: "equal", Items: []Expression{readExpression("item", ""), literalExpression("between-tools")}}}
	document.Mapping.HeaderControls = &ProtocolHeaderControls{
		Headers: []string{"x-protocol-mode"},
		Program: Expression{Op: "object", Fields: map[string]Expression{
			"interleavedThinking": {Op: "integer", Operation: "gt", Items: []Expression{{Op: "length", Source: &matches}, literalExpression(0)}},
		}},
	}
	return document
}

func TestPackageHeaderControlsUseDeclaredTokens(t *testing.T) {
	document := headerControlFixture(t)
	operation := document.Spec.Operations["generate"]
	operation.Headers = map[string]string{"X-Protocol-Mode": "first, between-tools"}
	document.Spec.Operations["generate"] = operation
	compiled := compilePackageForTest(t, document)
	bound, ok := compiled.Operation("generate")
	if !ok || !bound.IsInterleavedThinking {
		t.Fatal("outgoing operation ignored the Mapping declaration", bound)
	}
	for _, fixture := range []struct {
		header        string
		isInterleaved bool
	}{
		{"", false}, {"between-tools", true}, {"first, \tbetween-tools ,last", true},
		{"prefix-between-tools", false}, {"between-tools-suffix", false}, {"BETWEEN-TOOLS", false},
	} {
		controls, err := compiled.ResolveHeaderControls(t.Context(), func(name string) string {
			if name != "x-protocol-mode" {
				t.Fatalf("read undeclared header %s", name)
			}
			return fixture.header
		})
		if err != nil || controls.IsInterleavedThinking != fixture.isInterleaved {
			t.Fatal(fixture, controls, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.ResolveHeaderControls(ctx, func(string) string { t.Fatal("read after cancellation"); return "" }); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	if _, err := compiled.ResolveHeaderControls(t.Context(), func(string) string { return strings.Repeat("x", compiled.limits.BufferBytes) }); err == nil {
		t.Fatal("unbounded header accepted")
	}
	document.Mapping.HeaderControls = nil
	without := compilePackageForTest(t, document)
	bound, _ = without.Operation("generate")
	controls, err := without.ResolveHeaderControls(t.Context(), func(string) string { t.Fatal("undeclared mapping read headers"); return "" })
	if err != nil || bound.IsInterleavedThinking || controls.IsInterleavedThinking {
		t.Fatal("host inferred header semantics", bound, controls, err)
	}
}

func TestPackageHeaderControlsRejectInvalidDeclarations(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*ProtocolHeaderControls)
	}{
		{"empty", func(controls *ProtocolHeaderControls) { controls.Headers = nil }},
		{"duplicate", func(controls *ProtocolHeaderControls) {
			controls.Headers = append(controls.Headers, controls.Headers[0])
		}},
		{"case", func(controls *ProtocolHeaderControls) { controls.Headers[0] = "X-Protocol-Mode" }},
		{"invalid-header", func(controls *ProtocolHeaderControls) { controls.Headers[0] = "x-mode\r\n" }},
		{"unbound-runtime", func(controls *ProtocolHeaderControls) {
			controls.Program.Fields["interleavedThinking"] = readExpression("runtime", "/interleavedThinking")
		}},
		{"omitted-control", func(controls *ProtocolHeaderControls) { controls.Program.Fields = map[string]Expression{} }},
		{"null-control", func(controls *ProtocolHeaderControls) {
			controls.Program.Fields["interleavedThinking"] = literalExpression(nil)
		}},
		{"unknown-control", func(controls *ProtocolHeaderControls) { controls.Program.Fields["unknown"] = literalExpression(true) }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := headerControlFixture(t)
			fixture.edit(document.Mapping.HeaderControls)
			if err := document.Validate(DefaultLimits()); err == nil {
				t.Fatal("invalid header controls compiled")
			}
		})
	}
}
