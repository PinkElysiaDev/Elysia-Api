package server

import (
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/internal/outbound"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeProxyPatchPreservesDenylistAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"logLifecycleVersion":1,"outbound":{"deniedIpRanges":["127.0.0.0/8"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{config: cfg}
	t.Cleanup(func() { outbound.SetOutboundProxyURL(""); outbound.SetDeniedIPRanges(outbound.DefaultDeniedIPRanges) })
	save := func(body string, status int) {
		t.Helper()
		c, w := adminProtocolContext(http.MethodPut, "/runtime-config", body)
		s.adminUpdateRuntimeConfig(c)
		if w.Code != status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
	save(`{"outbound":{"proxyUrl":" http://user:secret@127.0.0.1:7890 "}}`, 200)
	if cfg.GetOutboundConfig().ProxyURL != "http://user:secret@127.0.0.1:7890" || len(cfg.GetOutboundConfig().DeniedIPRanges) != 1 {
		t.Fatal("proxy patch overwrote denylist")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.GetOutboundConfig().ProxyURL != cfg.GetOutboundConfig().ProxyURL {
		t.Fatal("proxy not persisted", err)
	}
	save(`{"outbound":{"deniedIpRanges":[]}}`, 200)
	if cfg.GetOutboundConfig().ProxyURL == "" || cfg.GetOutboundConfig().DeniedIPRanges == nil || len(cfg.GetOutboundConfig().DeniedIPRanges) != 0 {
		t.Fatal("denylist edit overwrote proxy or failed to clear list")
	}
	save(`{"httpTimeout":321,"outbound":{"proxyUrl":"socks5://user:secret@127.0.0.1:1080"}}`, 400)
	if cfg.GetHTTPTimeout() == 321 {
		t.Fatal("invalid proxy partially published config")
	}
	save(`{"outbound":{"proxyUrl":""}}`, 200)
	if cfg.GetOutboundConfig().ProxyURL != "" || s.restartRequired {
		t.Fatal("clear proxy requires restart or did not clear")
	}
}

func TestInvalidProxyInFileFailsWithoutLeakingCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"logLifecycleVersion":1,"outbound":{"proxyUrl":"http://user:supersecret@localhost:99999"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Fatal("invalid persisted proxy accepted or leaked")
	}
}
