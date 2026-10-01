package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// BuildHTTPRequest constructs the declared operation. The caller owns the
// configured HTTP client, outbound address policy, proxy and TLS settings.
func BuildHTTPRequest(ctx context.Context, baseURL, credential string, operation Operation, body []byte, parameters map[string]string) (*http.Request, error) {
	path, err := ExpandOperationPath(operation.Path, parameters)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(strings.TrimRight(baseURL, "/") + path)
	if err != nil {
		return nil, fmt.Errorf("invalid protocol target URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("protocol target requires HTTP or HTTPS")
	}
	if target.User != nil || target.Host == "" {
		return nil, fmt.Errorf("protocol target requires a host without embedded credentials")
	}
	query := target.Query()
	for key, value := range operation.Query {
		query.Set(key, value)
	}
	if operation.Auth.Location == "query" {
		query.Set(operation.Auth.Name, operation.Auth.Prefix+credential)
	}
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, operation.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot construct protocol HTTP request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", TransportContentType(operation.Transport))
	for name, value := range operation.Headers {
		request.Header.Set(name, value)
	}
	if operation.Auth.Location == "header" {
		if strings.ContainsAny(credential, "\r\n") {
			return nil, fmt.Errorf("protocol credential contains a line break")
		}
		request.Header.Set(operation.Auth.Name, operation.Auth.Prefix+credential)
	}
	return request, nil
}

// ExpandOperationPath substitutes explicitly named, escaped path components.
func ExpandOperationPath(path string, parameters map[string]string) (string, error) {
	for name, value := range parameters {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}
	if strings.ContainsAny(path, "{}") {
		return "", fmt.Errorf("operation path has an unresolved parameter")
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") || parsed.Fragment != "" {
		return "", fmt.Errorf("operation path must stay relative to the configured host")
	}
	return path, nil
}

// MatchOperationPath extracts declared components without changing the router.
func MatchOperationPath(template, path string) (map[string]string, bool) {
	parts, actual := strings.Split(template, "/"), strings.Split(path, "/")
	if len(parts) != len(actual) {
		return nil, false
	}
	parameters := map[string]string{}
	for index, part := range parts {
		start, end := strings.IndexByte(part, '{'), strings.IndexByte(part, '}')
		if start < 0 || end < start {
			if part != actual[index] {
				return nil, false
			}
			continue
		}
		prefix, suffix := part[:start], part[end+1:]
		value := actual[index]
		if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) || len(value) <= len(prefix)+len(suffix) {
			return nil, false
		}
		parameters[part[start+1:end]] = value[len(prefix) : len(value)-len(suffix)]
	}
	return parameters, true
}

// TransportContentType returns the explicit framing media type.
func TransportContentType(transport Transport) string {
	switch transport {
	case SSE:
		return "text/event-stream"
	case NDJSON:
		return "application/x-ndjson"
	default:
		return "application/json"
	}
}

// ReadBoundedBody applies the same byte limit to inbound and upstream JSON.
func ReadBoundedBody(reader io.Reader, limit int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("protocol body exceeds %d byte limit", limit)
	}
	return body, nil
}

// ReadFrames handles fragmented SSE/NDJSON and drains usage after semantic
// completion. A transport end marker ends framing, never fabricates success.
func ReadFrames(ctx context.Context, reader io.Reader, operation Operation, limit int, consume func(Value, Object) error) error {
	if operation.Transport != SSE && operation.Transport != NDJSON {
		return fmt.Errorf("operation is not an HTTP event stream")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, min(limit, 4096)), limit)
	var data []byte
	var name, id string
	isDone := false
	emit := func(raw []byte) error {
		if operation.Framing != nil && slices.Contains(operation.Framing.Done, string(raw)) {
			isDone = true
			return nil
		}
		if isDone {
			return fmt.Errorf("upstream frame arrived after transport end marker")
		}
		value, err := ParseValue(raw)
		if err != nil {
			return fmt.Errorf("invalid upstream event JSON: %w", err)
		}
		return consume(value, Object{"eventName": StringValue(name), "eventId": StringValue(id)})
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if operation.Transport == NDJSON {
			if strings.TrimSpace(line) != "" {
				if err := emit([]byte(line)); err != nil {
					return err
				}
				if isDone {
					return nil
				}
			}
			continue
		}
		if line == "" {
			if len(data) > 0 {
				if err := emit(data[:len(data)-1]); err != nil {
					return err
				}
				if isDone {
					return nil
				}
			}
			data, name = data[:0], ""
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			if len(data)+len(value)+1 > limit {
				return fmt.Errorf("upstream event exceeds buffer limit")
			}
			data = append(data, value...)
			data = append(data, '\n')
		case "event":
			name = value
		case "id":
			id = value
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(data) > 0 {
		return fmt.Errorf("upstream ended with an incomplete SSE frame")
	}
	return ctx.Err()
}

// WriteFrame emits one body using the declared downstream framing.
func WriteFrame(writer io.Writer, operation Operation, value Value) error {
	// Both SSE and NDJSON require physical line boundaries owned by framing.
	var compact bytes.Buffer
	if err := json.Compact(&compact, value.Bytes()); err != nil {
		return err
	}
	if operation.Transport == NDJSON {
		_, err := fmt.Fprintf(writer, "%s\n", compact.Bytes())
		return err
	}
	if operation.Transport != SSE {
		return fmt.Errorf("operation is not an HTTP event stream")
	}
	if operation.Framing != nil && operation.Framing.EventName != "" {
		if _, err := fmt.Fprintf(writer, "event: %s\n", operation.Framing.EventName); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "data: %s\n\n", compact.Bytes())
	return err
}
