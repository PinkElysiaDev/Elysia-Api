package outbound

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyURLValidation(t *testing.T) {
	for _, raw := range []string{"", "  ", "http://localhost:7890", "http://user:p%40ss@[::1]:7890/", "http://proxy.example"} {
		if _, err := ParseProxyURL(raw); err != nil {
			t.Fatalf("valid proxy rejected: %v", err)
		}
	}
	for _, raw := range []string{"localhost:7890", "socks5://localhost:1080", "http://", "http://user:supersecret@localhost:0", "http://localhost:65536", "http://localhost:", "http://localhost/path", "http://localhost?x=1", "http://localhost#frag", "http://user:supersecret@%zz"} {
		if _, err := ParseProxyURL(raw); err == nil || strings.Contains(err.Error(), "supersecret") {
			t.Fatal("invalid URL accepted or secret leaked")
		}
	}
}

func TestProxyRoutesAuthenticationSwitchingAndNoFallback(t *testing.T) {
	previousAllowPrivateDial := allowPrivateDial.Load()
	SetAllowPrivateDial(false)
	SetDeniedIPRanges(DefaultDeniedIPRanges)
	SetOutboundProxyURL("")
	t.Cleanup(func() {
		SetAllowPrivateDial(previousAllowPrivateDial)
		SetOutboundProxyURL("")
		SetDeniedIPRanges(DefaultDeniedIPRanges)
	})
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !r.URL.IsAbs() || r.URL.Host != "8.8.8.8" {
			t.Error("proxy did not receive absolute target")
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:supersecret")) {
			t.Error("missing proxy authentication")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
			return
		}
		w.Write([]byte("proxied"))
	}))
	defer proxy.Close()
	parsed, _ := url.Parse(proxy.URL)
	parsed.User = url.UserPassword("user", "supersecret")
	client := &http.Client{Transport: NewSecureTransport(), Timeout: time.Second}
	defer client.CloseIdleConnections()
	SetOutboundProxyURL(parsed.String())
	get := func(target, want string) {
		t.Helper()
		response, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if string(body) != want {
			t.Fatalf("got %q", body)
		}
	}
	get("http://8.8.8.8/models", "proxied")
	for _, target := range []string{"http://127.0.0.1/private", "http://169.254.169.254/latest", "http://8.8.8.8/redirect"} {
		response, err := client.Get(target)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("denied proxy target accepted")
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("denied targets reached proxy: %d", hits.Load())
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("other")) }))
	defer other.Close()
	SetOutboundProxyURL(other.URL)
	get("http://8.8.8.8/models", "other")
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directHits.Add(1); w.Write([]byte("direct")) }))
	defer direct.Close()
	SetDeniedIPRanges(nil)
	other.Close()
	if response, err := client.Get(direct.URL); err == nil {
		response.Body.Close()
		t.Fatal("failed proxy silently fell back")
	}
	if directHits.Load() != 0 {
		t.Fatal("direct connection attempted on proxy failure")
	}
	SetOutboundProxyURL("")
	get(direct.URL, "direct")
}

func TestProxyErrorsRetainCancellationWithoutSecrets(t *testing.T) {
	overlapping, _ := ParseProxyURL("http://user:usersecret@localhost:7890")
	if got := redactProxyError("password usersecret", overlapping); got != "password ***" {
		t.Fatalf("partially exposed password: %q", got)
	}
	proxy, _ := ParseProxyURL("http://user:supersecret@localhost:7890")
	err := &proxyRequestError{cause: context.Canceled, message: redactProxyError("proxy http://user:supersecret@localhost failed", proxy)}
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), "user") {
		t.Fatal("proxy error leaks credentials or loses cancellation")
	}
}

func TestProxyCatalogOverrideTakesPrecedence(t *testing.T) {
	SetOutboundProxyURL("http://127.0.0.1:1")
	t.Cleanup(func() { SetOutboundProxyURL("") })
	for _, scheme := range []string{"https", "socks5", "socks5h"} {
		if _, err := parseProxyURL(scheme+"://localhost:7890", true); err != nil {
			t.Fatal("legacy catalog proxy rejected", err)
		}
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("catalog")) }))
	defer proxy.Close()
	client := &http.Client{Transport: NewSecureTransport(proxy.URL), Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://8.8.8.8/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "catalog" {
		t.Fatalf("catalog override failed: %q %v", body, err)
	}
}
