package server

import (
	"context"
	"errors"
	"testing"
)

func TestCLIReadFailuresRemainErrors(t *testing.T) {
	s := newOpsTestServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, found, err := findCLISource(ctx, s.store, "missing"); found || !errors.Is(err, context.Canceled) {
		t.Fatal("source query failure became not found", found, err)
	}
	if _, found, err := findCLIGroup(ctx, s.store, "missing"); found || !errors.Is(err, context.Canceled) {
		t.Fatal("group query failure became not found", found, err)
	}
	if counts, err := countSourceModels(ctx, s.store); counts != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("model count failure became zero", counts, err)
	}
	for _, result := range []CLIResult{
		(&updateSourceTool{server: s}).Execute(ctx, &opsTestContext{}, []byte(`{"source":"missing"}`)),
		(&updateGroupTool{server: s}).Execute(ctx, &opsTestContext{}, []byte(`{"group":"missing"}`)),
		(&listModelsTool{server: s}).Execute(ctx, &opsTestContext{}, []byte(`{"source":"missing"}`)),
	} {
		if result.OK || result.Data.(map[string]any)["error"] != "read_failed" {
			t.Fatalf("tool hid read failure: %+v", result)
		}
	}
}
