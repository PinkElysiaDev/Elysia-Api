package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

func TestRuntimeConfigRejectsWholeInvalidPatch(t *testing.T) {
	s := &Server{config: &config.Config{HTTPTimeout: 12}}
	c, recorder := adminProtocolContext(http.MethodPut, "/runtime-config", `{"httpTimeout":321,"agent":{"toolLoopLimit":101}}`)
	s.adminUpdateRuntimeConfig(c)
	if recorder.Code != 400 || s.config.GetHTTPTimeout() != 12 {
		t.Fatalf("status=%d timeout=%d", recorder.Code, s.config.GetHTTPTimeout())
	}
}

func TestRuntimeConfigSaveFailureHasNoSideEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"logLifecycleVersion":1,"httpTimeout":12,"agent":{"toolLoopLimit":17}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{config: cfg, protocolTransport: relay.NewProtocolTransport(0)}
	s.usageRetention = newUsageRetention(s)
	t.Cleanup(s.usageRetention.cancel)
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	c, recorder := adminProtocolContext(http.MethodPut, "/runtime-config", `{"host":"192.0.2.1","httpTimeout":321,"agent":{"toolLoopLimit":25},"usageLog":{"maxRecords":1},"outbound":{"deniedIpRanges":[]}}`)
	s.adminUpdateRuntimeConfig(c)
	if recorder.Code != 500 || cfg.GetHTTPTimeout() != 12 || cfg.ResolveAgentToolLoopLimit() != 17 || cfg.GetUsageLogConfig().MaxRecords != 0 || len(cfg.GetOutboundConfig().DeniedIPRanges) == 0 || s.restartRequired {
		t.Fatalf("failed save mutated runtime: status=%d timeout=%d", recorder.Code, cfg.GetHTTPTimeout())
	}
	if s.usageRetention.snapshotStats().Pending {
		t.Fatal("failed save scheduled destructive cleanup")
	}
}

func TestRetentionLimitZeroMeansUnlimited(t *testing.T) {
	for _, test := range []struct {
		before, after int64
		want          bool
	}{{0, 1, true}, {1, 0, false}, {2, 1, true}, {1, 2, false}, {1, 1, false}, {0, 0, false}} {
		if got := isLimitTighter(test.before, test.after); got != test.want {
			t.Errorf("%d -> %d: got %t", test.before, test.after, got)
		}
	}
}
