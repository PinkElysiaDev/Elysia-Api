package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// ProtocolTransport owns shared secure HTTP and session clients. All wire
// construction and parsing belongs to the pinned protocol definition.
type ProtocolTransport struct {
	client       *dynamicTimeoutClient
	streamClient *http.Client
}

// NewProtocolTransport creates clients with shared outbound address policy.
func NewProtocolTransport(timeout time.Duration) *ProtocolTransport {
	return &ProtocolTransport{client: newDynamicTimeoutClient(timeout), streamClient: &http.Client{Transport: newSecureTransport()}}
}

// SetTimeout replaces the HTTP client atomically; active streams retain their
// transport and are canceled through their request context.
func (transport *ProtocolTransport) SetTimeout(timeout time.Duration) {
	transport.client.SetTimeout(timeout)
}

// SendProtocolRequest uses the gateway's configured clients, including proxy,
// TLS and dial-time address validation, for a pinned declarative operation.
func (transport *ProtocolTransport) SendProtocolRequest(ctx context.Context, baseURL, credential string, operation protocol.Operation, body []byte, parameters map[string]string) (*http.Response, error) {
	request, err := protocol.BuildHTTPRequest(ctx, baseURL, credential, operation, body, parameters)
	if err != nil {
		return nil, err
	}
	var client interface {
		Do(*http.Request) (*http.Response, error)
	} = transport.client
	if operation.Transport == protocol.SSE || operation.Transport == protocol.NDJSON {
		idleMillis := protocol.DefaultStreamIdleMillis
		if operation.Framing != nil && operation.Framing.IdleMillis > 0 {
			idleMillis = operation.Framing.IdleMillis
		}
		return transport.sendStreamRequest(request, time.Duration(idleMillis)*time.Millisecond)
	}
	if operation.Kind == "submit" || operation.Kind == "status" || operation.Kind == "result" || operation.Kind == "cancel" || operation.Kind == "models" {
		transport.client.mu.RLock()
		copy := *transport.client.client
		transport.client.mu.RUnlock()
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	response, err := client.Do(request)
	return response, sanitizeTransportError(err)
}

func sanitizeTransportError(err error) error {
	var urlError *url.Error
	if !errors.As(err, &urlError) || urlError.URL == "" {
		return err
	}
	parsed, parseErr := url.Parse(urlError.URL)
	if parseErr != nil {
		return fmt.Errorf("%s <redacted url>: %w", urlError.Op, urlError.Err)
	}
	parsed.RawQuery, parsed.Fragment, parsed.User = "", "", nil
	return fmt.Errorf("%s %q: %w", urlError.Op, parsed.String(), urlError.Err)
}
