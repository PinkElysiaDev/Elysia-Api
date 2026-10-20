package eventstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	sse "github.com/tmaxmax/go-sse"
)

type oneByteReader struct{ io.Reader }

func (r oneByteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(len(p), 1)]) }

func TestSSECompleteFrameBoundary(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"\n", "\r", "\r\n"} {
		for _, second := range []string{"\n", "\r", "\r\n"} {
			// CR followed by LF is one CRLF, not two empty-line boundaries.
			complete := !(first == "\r" && second == "\n")
			input := "data: {}" + first + second
			for _, reader := range []io.Reader{strings.NewReader(input), oneByteReader{strings.NewReader(input)}} {
				count := 0
				err := Read(t.Context(), reader, 100, func(sse.Event) error { count++; return nil })
				if complete && (err != nil || count != 1) {
					t.Fatalf("boundary %q: count=%d err=%v", first+second, count, err)
				}
				if !complete && (err == nil || count != 0) {
					t.Fatalf("truncated boundary accepted: count=%d err=%v", count, err)
				}
			}
		}
	}
	for _, ending := range []string{"", "\n", "\r", "\r\n"} {
		count := 0
		err := Read(t.Context(), strings.NewReader("data: {\"done\":true}"+ending), 100, func(sse.Event) error { count++; return nil })
		if err == nil || count != 0 {
			t.Fatalf("truncated completion %q delivered: %d %v", ending, count, err)
		}
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSSEWriteErrorsAndEncoding(t *testing.T) {
	t.Parallel()
	failure := errors.New("disconnected")
	if err := Write(failingWriter{failure}, "delta", "{\"text\":\"ok\"}"); !errors.Is(err, failure) {
		t.Fatalf("write lost error: %v", err)
	}
	if err := Comment(failingWriter{failure}, "keepalive"); !errors.Is(err, failure) {
		t.Fatalf("comment lost error: %v", err)
	}
	var output bytes.Buffer
	if err := Comment(&output, "keepalive"); err != nil {
		t.Fatal(err)
	}
	if err := Write(&output, "delta", "first\r\nsecond\rthird"); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), ": keepalive\n\nevent: delta\ndata: first\ndata: second\ndata: third\n\n"; got != want {
		t.Fatalf("wire: %q", got)
	}
	output.Reset()
	if err := Write(&output, "bad\ndata: injected", "{}"); err == nil || output.Len() != 0 {
		t.Fatal("invalid name partially emitted")
	}
	if err := Write(&output, "", ""); err != nil || output.String() != "data: \n\n" {
		t.Fatalf("empty data: %q %v", output.String(), err)
	}
}

func TestSSECancellationAndConsumerError(t *testing.T) {
	t.Parallel()
	input := "data: {}\n\ndata: {}\n\n"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	count := 0
	err := Read(ctx, strings.NewReader(input), 100, func(sse.Event) error { count++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("cancellation: count=%d err=%v", count, err)
	}
	want := errors.New("consumer failed")
	err = Read(t.Context(), strings.NewReader(input), 100, func(sse.Event) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("consumer error lost: %v", err)
	}
}
