package server

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConsoleBrowserEnvironmentOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake opener uses a POSIX shell")
	}
	// Never open a real browser, including when the override is broken.
	dir := t.TempDir()
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	on, off := true, false
	strPtr := func(s string) *string { return &s }
	for _, tc := range []struct {
		name                  string
		env                   *string
		configured            *bool
		wantOpen, wantWarning bool
	}{
		{"default", nil, nil, true, false},
		{"configured off", nil, &off, false, false},
		{"configured on", nil, &on, true, false},
		{"host disables default", strPtr("false"), nil, false, false},
		{"host overrides on", strPtr("false"), &on, false, false},
		{"host overrides off", strPtr("true"), &off, true, false},
		{"standard false", strPtr("0"), &on, false, false},
		{"standard true", strPtr("1"), &off, true, false},
		{"whitespace", strPtr(" false "), &on, false, false},
		{"invalid falls back off", strPtr("invalid"), &off, false, true},
		{"empty falls back on", strPtr(""), &on, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ELYSIA_API_OPEN_BROWSER", "")
			if tc.env == nil {
				if err := os.Unsetenv("ELYSIA_API_OPEN_BROWSER"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("ELYSIA_API_OPEN_BROWSER", *tc.env)
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previous) })
			launchConsoleBrowser(tc.configured, "127.0.0.1", 8765)
			if got := strings.Contains(output.String(), "已尝试在默认浏览器打开控制台"); got != tc.wantOpen {
				t.Fatalf("opened=%v, want %v; log=%s", got, tc.wantOpen, output.String())
			}
			if got := strings.Contains(output.String(), "ELYSIA_API_OPEN_BROWSER"); got != tc.wantWarning {
				t.Fatalf("warning=%v, want %v; log=%s", got, tc.wantWarning, output.String())
			}
		})
	}
}

// 通配监听地址归一为回环；显式地址原样保留。
func TestConsoleLaunchURL(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
		want string
	}{
		{"", 8765, "http://127.0.0.1:8765/ui/"},
		{"0.0.0.0", 3000, "http://127.0.0.1:3000/ui/"},
		{"::", 3000, "http://127.0.0.1:3000/ui/"},
		{"[::]", 3000, "http://127.0.0.1:3000/ui/"},
		{"localhost", 3000, "http://127.0.0.1:3000/ui/"},
		{"LOCALHOST", 3000, "http://127.0.0.1:3000/ui/"},
		{"127.0.0.1", 8765, "http://127.0.0.1:8765/ui/"},
		{"192.168.1.10", 8765, "http://192.168.1.10:8765/ui/"},
		{"gw.example.com", 8765, "http://gw.example.com:8765/ui/"},
		// 显式 IPv6 字面量必须加方括号，否则浏览器无法解析；
		// Go 监听地址的既成方括号写法不能被双重包裹。
		{"::1", 8765, "http://[::1]:8765/ui/"},
		{"[::1]", 8765, "http://[::1]:8765/ui/"},
		{"2001:db8::1", 8765, "http://[2001:db8::1]:8765/ui/"},
	} {
		if got := consoleLaunchURL(tc.host, tc.port); got != tc.want {
			t.Fatalf("consoleLaunchURL(%q, %d) = %q want %q", tc.host, tc.port, got, tc.want)
		}
	}
}
