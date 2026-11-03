package server

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"
)

// External processes keep the existing workload and output assertions while
// allowing an unchanged release executable to be the measured gateway.
func readExternalConversionAddress(t *testing.T, path, role string) conversionHTTPAddress {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var addresses map[string]conversionHTTPAddress
	if err := json.Unmarshal(raw, &addresses); err != nil {
		t.Fatal(err)
	}
	address, ok := addresses[role]
	if !ok || address.PID <= 0 || address.Execution == nil {
		t.Fatal("external process identity missing", role)
	}
	for _, raw := range []string{address.URL, address.Metrics} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
			t.Fatal("external performance process must use loopback HTTP", raw)
		}
	}
	return address
}
