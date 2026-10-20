package outbound

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var outboundProxyURL atomic.Pointer[string]

// ParseProxyURL never includes credentials in validation errors.
func ParseProxyURL(raw string) (*url.URL, error) { return parseProxyURL(raw, false) }

func parseProxyURL(raw string, catalogOverride bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	invalid := fmt.Errorf("proxyUrl must be an HTTP proxy URL such as http://127.0.0.1:7890, without a path, query or fragment")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && !(catalogOverride && (u.Scheme == "https" || u.Scheme == "socks5" || u.Scheme == "socks5h"))) || u.Hostname() == "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, invalid
	}
	if strings.Contains(u.Hostname(), ":") && net.ParseIP(u.Hostname()) == nil {
		return nil, invalid
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n%") || strings.HasSuffix(u.Host, ":") {
		return nil, invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, invalid
		}
	}
	u.Path, u.RawPath = "", ""
	return u, nil
}

// SetOutboundProxyURL publishes a validated setting; existing calls keep their transport.
func SetOutboundProxyURL(raw string) { raw = strings.TrimSpace(raw); outboundProxyURL.Store(&raw) }

// Each client retains one pool; configuration changes replace it without mutating live requests.
type outboundTransport struct {
	mu        sync.Mutex
	override  string
	current   string
	transport *http.Transport
	proxy     *url.URL
}

func (t *outboundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	raw := t.override
	if raw == "" {
		if current := outboundProxyURL.Load(); current != nil {
			raw = *current
		}
	}
	t.mu.Lock()
	if t.transport == nil || raw != t.current {
		proxy, err := parseProxyURL(raw, t.override != "")
		if err != nil {
			t.mu.Unlock()
			return nil, err
		}
		transport := newDirectTransport()
		if proxy != nil {
			transport.Proxy = http.ProxyURL(proxy)
			// Only the explicitly configured proxy is trusted to be local; targets are checked below.
			transport.DialContext = (&net.Dialer{Timeout: secureDialTimeout, KeepAlive: secureDialKeepAlive}).DialContext
		}
		if t.transport != nil {
			t.transport.CloseIdleConnections()
		}
		t.transport, t.proxy, t.current = transport, proxy, raw
	}
	transport, proxy := t.transport, t.proxy
	t.mu.Unlock()
	if proxy != nil {
		if err := checkProxyTarget(req); err != nil {
			return nil, err
		}
	}
	response, err := transport.RoundTrip(req)
	if err != nil && proxy != nil {
		// A proxy can echo its credentials in an error response; never surface those verbatim.
		return response, &proxyRequestError{cause: err, message: redactProxyError(err.Error(), proxy)}
	}
	return response, err
}

func (t *outboundTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.transport != nil {
		t.transport.CloseIdleConnections()
	}
}

func checkProxyTarget(req *http.Request) error {
	if allowPrivateDial.Load() {
		return nil
	}
	host := req.URL.Hostname()
	addresses, err := net.DefaultResolver.LookupIPAddr(req.Context(), host)
	if err != nil {
		return fmt.Errorf("proxy target DNS lookup failed")
	}
	if len(addresses) == 0 {
		return fmt.Errorf("proxy target has no addresses")
	}
	for _, address := range addresses {
		if IsDeniedIP(address.IP) {
			return fmt.Errorf("proxy target is in outbound.deniedIpRanges")
		}
	}
	return nil
}

// Preserve cancellation/error classification while redacting proxy credentials.
type proxyRequestError struct {
	cause   error
	message string
}

func (e *proxyRequestError) Error() string { return "proxy request failed: " + e.message }
func (e *proxyRequestError) Unwrap() error { return e.cause }
func redactProxyError(message string, proxy *url.URL) string {
	if proxy.User == nil {
		return message
	}
	password, _ := proxy.User.Password()
	secrets := []string{proxy.User.String(), proxy.User.Username(), password, url.QueryEscape(password), url.PathEscape(password)}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "***")
		}
	}
	return message
}
