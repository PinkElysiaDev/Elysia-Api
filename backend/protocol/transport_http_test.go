package protocol

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type fragmentedReader struct{ content string }

func (reader *fragmentedReader) Read(buffer []byte) (int, error) {
	if reader.content == "" {
		return 0, io.EOF
	}
	buffer[0] = reader.content[0]
	reader.content = reader.content[1:]
	return 1, nil
}

func TestHTTPFramingPreservesFragmentsAndBounds(t *testing.T) {
	operation := Operation{Transport: SSE, Framing: &Framing{Done: []string{"[DONE]"}}}
	input := ": heartbeat\r\nevent: token\r\nid: 7\r\ndata: {\r\ndata: \"n\":9007199254740993123}\r\n\r\ndata: [DONE]\r\n\r\n"
	var got Value
	err := ReadFrames(t.Context(), &fragmentedReader{input}, operation, 100, func(value Value, metadata Object) error {
		got = value
		if metadata["eventName"].raw != `"token"` || metadata["eventId"].raw != `"7"` {
			t.Fatalf("metadata: %+v", metadata)
		}
		return nil
	})
	if err != nil || !strings.Contains(got.raw, "9007199254740993123") {
		t.Fatalf("fragmented SSE: %v %s", err, got.Bytes())
	}
	for _, input := range []string{"data: {\"n\":1}", "data: {\"n\":\"" + strings.Repeat("x", 100) + "\"}\n\n", "data: invalid\n\n"} {
		if err := ReadFrames(t.Context(), strings.NewReader(input), operation, 32, func(Value, Object) error { return nil }); err == nil {
			t.Fatalf("accepted broken frame: %s", input)
		}
	}
	var output bytes.Buffer
	if err := WriteFrame(&output, Operation{Transport: NDJSON}, got); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(output.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("NDJSON emitted multiline JSON: %s", output.Bytes())
	}
}

func TestHTTPDeclaredCredentialsAndRelativePath(t *testing.T) {
	operation := Operation{Method: "POST", Path: "/models/{model}:generate", Transport: HTTPJSON, Auth: Credential{Location: "query", Name: "key"}, Query: map[string]string{"mode": "exact"}}
	request, err := BuildHTTPRequest(t.Context(), "https://example.test/v1", "secret+&token", operation, []byte(`{"n":9007199254740993}`), map[string]string{"model": "vendor/model"})
	if err != nil {
		t.Fatal(err)
	}
	if request.URL.Host != "example.test" || request.URL.Query().Get("key") != "secret+&token" || request.URL.EscapedPath() != "/v1/models/vendor%2Fmodel:generate" {
		t.Fatalf("request target = %s", request.URL)
	}
	if request.Header.Get("Authorization") != "" {
		t.Fatal("added undeclared bearer auth")
	}
	for _, path := range []string{"https://other.test", "//other.test/x", "/unresolved/{unknown}", "/path#fragment", "/\\evil"} {
		operation.Path = path
		if _, err := BuildHTTPRequest(t.Context(), "https://example.test", "secret", operation, nil, nil); err == nil {
			t.Fatalf("accepted path %s", path)
		}
	}
	parameters, matches := MatchOperationPath("/models/{model}:generate", "/models/group:generate")
	if !matches || parameters["model"] != "group" {
		t.Fatalf("path parameters: %+v %v", parameters, matches)
	}
}
