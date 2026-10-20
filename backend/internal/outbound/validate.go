package outbound

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateBaseURL checks configured upstream URLs before use. Dial-time checks
// still apply independently to the actual destination, including after redirects.
func ValidateBaseURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("missing host")
	}
	if parsed.User != nil {
		return fmt.Errorf("userinfo is not allowed in baseUrl")
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("missing hostname")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
	if err != nil {
		return fmt.Errorf("dns resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("hostname resolved to no addresses")
	}
	for _, ip := range ips {
		if IsDeniedIP(ip) {
			return fmt.Errorf("resolved IP %s is in the outbound deny list", ip.String())
		}
	}
	return nil
}
