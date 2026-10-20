// Package eventstream adapts go-sse to finite gateway streams. The library owns
// field parsing and encoding; the gateway requires complete frames at EOF.
package eventstream

import (
	"context"
	"errors"
	"io"
	"strings"

	sse "github.com/tmaxmax/go-sse"
)

// ErrIncomplete reports EOF before the final event's blank-line delimiter.
var ErrIncomplete = errors.New("upstream ended with an incomplete SSE frame")

// Read does not reconnect or evaluate JSON. Empty/control events update the
// library's ID state but do not reach the JSON consumer. The limit covers the
// entire wire event, including fields, comments, and delimiters.
func Read(ctx context.Context, reader io.Reader, limit int, consume func(sse.Event) error) error {
	if limit <= 0 {
		return errors.New("SSE frame limit must be positive")
	}
	guard := &completeReader{ctx: ctx, reader: reader}
	for event, err := range sse.Read(guard, &sse.ReadConfig{MaxEventSize: limit}) {
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return err
		}
		if event.Data == "" {
			continue
		}
		if err := consume(event); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// go-sse accepts a final data line ending in a single newline at EOF. For finite
// model streams, delivering that truncated event could fabricate completion or
// usage. Returning a non-EOF error suppresses the library's EOF dispatch. This
// guard retains only four trailing bytes, not a second field/frame parser.
type completeReader struct {
	ctx    context.Context
	reader io.Reader
	tail   [4]byte
	used   int
}

func (r *completeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		if n >= len(r.tail) {
			r.used = copy(r.tail[:], p[n-len(r.tail):n])
		} else {
			keep := min(r.used, len(r.tail)-n)
			copy(r.tail[:keep], r.tail[r.used-keep:r.used])
			r.used = keep + copy(r.tail[keep:], p[:n])
		}
	}
	if err == io.EOF && r.used > 0 {
		// Remove one complete newline, then require another newline immediately
		// before it. CRLF is one separator, not two.
		end := r.used
		if end >= 2 && r.tail[end-2] == '\r' && r.tail[end-1] == '\n' {
			end -= 2
		} else if r.tail[end-1] == '\n' || r.tail[end-1] == '\r' {
			end--
		}
		if end == 0 || r.tail[end-1] != '\n' && r.tail[end-1] != '\r' {
			return n, ErrIncomplete
		}
	}
	return n, err
}

// Write and Comment do not flush; the HTTP owner controls flush/lifetime policy.
func Write(writer io.Writer, name, data string) error {
	var message sse.Message
	if strings.ContainsRune(name, 0) {
		return errors.New("invalid SSE event name")
	}
	if name != "" {
		typ, err := sse.NewType(name)
		if err != nil {
			return err
		}
		message.Type = typ
	}
	// go-sse otherwise omits an empty data field.
	if data == "" {
		data = "\n"
	}
	message.AppendData(data)
	_, err := message.WriteTo(writer)
	return err
}

func Comment(writer io.Writer, text string) error {
	var message sse.Message
	message.AppendComment(text)
	_, err := message.WriteTo(writer)
	return err
}
