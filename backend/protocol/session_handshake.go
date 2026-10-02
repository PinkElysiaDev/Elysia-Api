package protocol

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// SessionHandshake is the explicit JSON input/output of handshake mappings.
// Arrays preserve repeated query/header values; credentials are supplied only
// by the gateway, outside this mapping envelope.
type SessionHandshake struct {
	Query   map[string][]string `json:"query"`
	Headers map[string][]string `json:"headers"`
}

func isCredentialField(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "x-goog-api-key", "key", "api_key", "apikey", "api-key", "token", "access_token", "panel_access_token":
		return true
	}
	return false
}

func isTransportHeader(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "sec-websocket-") || slices.Contains([]string{"host", "connection", "upgrade", "content-length", "transfer-encoding", "te", "trailer", "origin"}, name)
}

func checkHandshakeFields(config *SessionConfig) error {
	for _, entry := range []struct {
		names    []string
		isHeader bool
	}{{config.QueryFields, false}, {config.HeaderFields, true}} {
		seen := map[string]bool{}
		for _, name := range entry.names {
			key := name
			if entry.isHeader {
				key = http.CanonicalHeaderKey(name)
			}
			if name == "" || strings.ContainsAny(name, "\r\n\x00 :") || seen[key] || isCredentialField(name) || (entry.isHeader && isTransportHeader(name)) {
				return fmt.Errorf("handshake fields must be unique noncredential names outside transport headers")
			}
			seen[key] = true
		}
	}
	return nil
}

// ReadSessionHandshake exposes only fields explicitly declared by the ingress.
// Even an unrestricted object expression cannot read gateway credentials.
func ReadSessionHandshake(request *http.Request, operation Operation) (Value, error) {
	handshake := SessionHandshake{Query: map[string][]string{}, Headers: map[string][]string{}}
	for _, name := range operation.Session.QueryFields {
		if values, exists := request.URL.Query()[name]; exists {
			handshake.Query[name] = slices.Clone(values)
		}
	}
	for _, name := range operation.Session.HeaderFields {
		if values := request.Header.Values(name); len(values) > 0 {
			handshake.Headers[name] = slices.Clone(values)
		}
	}
	return EncodeValue(handshake)
}

// BuildSessionHandshake applies declared mapped fields to the same secure HTTP
// request contract as ordinary forwarding. A WebSocket library performs the
// upgrade using this URL/header and the gateway's existing HTTP client.
func BuildSessionHandshake(ctx context.Context, baseURL, credential string, operation Operation, body []byte, parameters map[string]string) (*http.Request, error) {
	var handshake SessionHandshake
	if err := decodeContract(body, &handshake); err != nil {
		return nil, fmt.Errorf("invalid session handshake mapping: %w", err)
	}
	request, err := BuildHTTPRequest(ctx, baseURL, credential, operation, nil, parameters)
	if err != nil {
		return nil, err
	}
	request.Body = nil
	request.Header.Del("Content-Type")
	query := request.URL.Query()
	for name, values := range handshake.Query {
		if !slices.Contains(operation.Session.QueryFields, name) || isCredentialField(name) || (operation.Auth.Location == "query" && name == operation.Auth.Name) {
			return nil, fmt.Errorf("session handshake query field %q is not declared or is reserved", name)
		}
		if _, exists := operation.Query[name]; exists {
			return nil, fmt.Errorf("session handshake query field %q conflicts with operation metadata", name)
		}
		query[name] = slices.Clone(values)
	}
	for name, values := range handshake.Headers {
		if !slices.ContainsFunc(operation.Session.HeaderFields, func(field string) bool { return strings.EqualFold(field, name) }) || isCredentialField(name) || isTransportHeader(name) || (operation.Auth.Location == "header" && strings.EqualFold(name, operation.Auth.Name)) {
			return nil, fmt.Errorf("session handshake header %q is not declared or is reserved", name)
		}
		for key := range operation.Headers {
			if strings.EqualFold(key, name) {
				return nil, fmt.Errorf("session handshake header %q conflicts with operation metadata", name)
			}
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n\x00") {
				return nil, fmt.Errorf("session handshake header contains invalid characters")
			}
		}
		request.Header[http.CanonicalHeaderKey(name)] = slices.Clone(values)
	}
	request.URL.RawQuery = query.Encode()
	return request, nil
}
