package protocol

import (
	"strings"
	"testing"
)

func TestBuildSessionHandshakePreservesDeclaredValuesAndIsolatesCredentials(t *testing.T) {
	options := DefaultSessionConfig()
	options.QueryFields = []string{"model", "include"}
	options.HeaderFields = []string{"X-Version"}
	operation := Operation{Kind: "session", Method: "GET", Path: "/live/{model}", Transport: WebSocket, Session: &options, Auth: Credential{Location: "header", Name: "Authorization", Prefix: "Bearer "}}
	value, err := EncodeValue(SessionHandshake{
		Query:   map[string][]string{"model": {"group"}, "include": {"a", "b"}},
		Headers: map[string][]string{"X-Version": {"2026"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := BuildSessionHandshake(t.Context(), "https://provider.test/api", "UPSTREAM-SECRET", operation, value.Bytes(), map[string]string{"model": "vendor/model"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("Authorization") != "Bearer UPSTREAM-SECRET" || request.Header.Get("X-Version") != "2026" || strings.Join(request.URL.Query()["include"], ",") != "a,b" || request.Body != nil || request.URL.EscapedPath() != "/api/live/vendor%2Fmodel" {
		t.Fatalf("invalid handshake: %+v", request)
	}
}

func TestSessionHandshakeRejectsCredentialAndTransportOverrides(t *testing.T) {
	for _, field := range []string{"Authorization", "x-api-key", "Cookie", "Origin", "Sec-WebSocket-Key", "Upgrade", "Host"} {
		t.Run(field, func(t *testing.T) {
			options := DefaultSessionConfig()
			options.HeaderFields = []string{field}
			if checkHandshakeFields(&options) == nil {
				t.Fatal("reserved header allowed")
			}
		})
	}
	options := DefaultSessionConfig()
	options.QueryFields = []string{"model"}
	options.HeaderFields = []string{"X-Version"}
	operation := Operation{Method: "GET", Path: "/live", Session: &options, Auth: Credential{Location: "none"}}
	for _, raw := range []string{`{"query":{"key":["secret"]}}`, `{"headers":{"Authorization":["secret"]}}`, `{"headers":{"X-Version":["v\r\nInjected: true"]}}`, `{"query":{"model":"not-an-array"}}`, `{"body":{"ignored":"no"}}`} {
		if _, err := BuildSessionHandshake(t.Context(), "https://provider.test", "", operation, []byte(raw), nil); err == nil {
			t.Fatalf("accepted invalid mapping: %s", raw)
		}
	}
}
