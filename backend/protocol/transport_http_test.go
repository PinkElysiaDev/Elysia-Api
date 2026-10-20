package protocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/elysia-api/backend/internal/eventstream"
)

type fragmentedReader struct{ content string }

func TestHTTPEventNamesUseDeclaredPath(t *testing.T) {
	operation := Operation{Transport: SSE, Framing: &Framing{EventNamePath: "/type"}}
	var output bytes.Buffer
	value, _ := ParseValue([]byte(`{"type":"content_block_delta","delta":{"text":"hi"}}`))
	if err := WriteFrame(&output, operation, value); err != nil || !strings.HasPrefix(output.String(), "event: content_block_delta\n") {
		t.Fatalf("dynamic event header: %s, %v", output.String(), err)
	}
	for _, body := range []string{`{}`, `{"type":null}`, `{"type":12}`, `{"type":"x\ndata: forged"}`} {
		output.Reset()
		value, _ := ParseValue([]byte(body))
		if err := WriteFrame(&output, operation, value); err == nil || output.Len() != 0 {
			t.Fatalf("invalid name partially emitted: %s", body)
		}
	}
}

func TestHTTPSSELibraryMetadataAndControlFrames(t *testing.T) {
	t.Parallel()
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		input := "\xef\xbb\xbf: initial heartbeat\n\nid: first\n\nretry: 200\n\nunknown: ignored\n\n" +
			"event: delta\ndata: {\ndata: \"n\":9007199254740993123,\"value\":null,\"ok\":false}\n\n" +
			": keepalive\n\nid: invalid\x00id\ndata: {}\n\nid:\ndata: {}\n\ndata: [DONE]\n\ndata: invalid\n\n"
		input = strings.ReplaceAll(input, "\n", newline)
		var values []Value
		var metadata []Object
		err := ReadFrames(t.Context(), &fragmentedReader{input}, Operation{Transport: SSE, Framing: &Framing{Done: []string{"[DONE]"}}}, 1024, func(value Value, meta Object) error {
			values = append(values, value)
			metadata = append(metadata, meta)
			return nil
		})
		if err != nil || len(values) != 3 {
			t.Fatalf("newline %q: values=%v err=%v", newline, values, err)
		}
		if !strings.Contains(values[0].raw, "9007199254740993123") || !strings.Contains(values[0].raw, `"value":null`) || !strings.Contains(values[0].raw, `"ok":false`) {
			t.Fatalf("JSON changed: %s", values[0].raw)
		}
		for i, wantID := range []string{`"first"`, `"first"`, `""`} {
			if metadata[i]["eventId"].raw != wantID {
				t.Fatalf("ID %d: %v", i, metadata[i])
			}
		}
		if metadata[0]["eventName"].raw != `"delta"` || metadata[1]["eventName"].raw != `""` {
			t.Fatalf("event names: %v", metadata)
		}
	}
}

func TestHTTPSSELibraryFrameBudgetAndTruncation(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"event: " + strings.Repeat("x", 70) + "\ndata: {}\n\n",
		": " + strings.Repeat("x", 70) + "\n\ndata: {}\n\n",
		strings.Repeat("data: 1\n", 9) + "\n",
	} {
		err := ReadFrames(t.Context(), strings.NewReader(input), Operation{Transport: SSE}, 64, func(Value, Object) error { t.Fatal("oversized event delivered"); return nil })
		if err == nil || !strings.Contains(err.Error(), "response_frame") {
			t.Fatalf("frame bound not mapped: %v", err)
		}
	}
	count := 0
	err := ReadFrames(t.Context(), strings.NewReader("data: {}\n\ndata: {\"finished\":true}\n"), Operation{Transport: SSE}, 100, func(Value, Object) error { count++; return nil })
	if !errors.Is(err, eventstream.ErrIncomplete) || count != 1 {
		t.Fatalf("partial final frame accepted: count=%d err=%v", count, err)
	}
}

func TestHTTPStreamCompactionPreservesRawJSON(t *testing.T) {
	t.Parallel()
	value, err := ParseValue([]byte("{\n\"n\":9007199254740993123, \"html\":\"<>&\", \"zero\":-0, \"null\":null, \"false\":false}"))
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []Transport{SSE, NDJSON} {
		var output bytes.Buffer
		if err := WriteFrame(&output, Operation{Transport: transport}, value); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"9007199254740993123", "<>&", `"zero":-0`, `"null":null`, `"false":false`} {
			if !strings.Contains(output.String(), text) {
				t.Fatalf("%s lost %s: %s", transport, text, output.String())
			}
		}
	}
}

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
