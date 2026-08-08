package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultSSEIdleTimeout = 5 * time.Minute

// SSEEvent is one fully assembled Server-Sent Event. Multiple data lines are
// joined with a newline as required by the SSE specification.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry time.Duration
}

type sseReadResult struct {
	event SSEEvent
	err   error
}

// SSEEventReader parses a stream in a background goroutine so callers can
// enforce context cancellation and an idle timeout without losing multiline
// events. Close stops delivery; callers still own and should close the source.
type SSEEventReader struct {
	results   chan sseReadResult
	done      chan struct{}
	closeOnce sync.Once
}

func NewSSEEventReader(reader io.Reader) *SSEEventReader {
	result := &SSEEventReader{
		results: make(chan sseReadResult, 1),
		done:    make(chan struct{}),
	}
	go result.scan(reader)
	return result
}

func (reader *SSEEventReader) Close() {
	if reader == nil {
		return
	}
	reader.closeOnce.Do(func() { close(reader.done) })
}

func (reader *SSEEventReader) Read(ctx context.Context, idleTimeout time.Duration) (SSEEvent, bool, error) {
	if reader == nil {
		return SSEEvent{}, false, fmt.Errorf("nil SSE event reader")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if idleTimeout <= 0 {
		select {
		case <-ctx.Done():
			return SSEEvent{}, false, ctx.Err()
		case result, ok := <-reader.results:
			if !ok {
				return SSEEvent{}, false, nil
			}
			return result.event, result.err == nil, result.err
		}
	}
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return SSEEvent{}, false, ctx.Err()
	case <-timer.C:
		return SSEEvent{}, false, fmt.Errorf("stream read timeout after %v", idleTimeout)
	case result, ok := <-reader.results:
		if !ok {
			return SSEEvent{}, false, nil
		}
		return result.event, result.err == nil, result.err
	}
}

func (reader *SSEEventReader) scan(source io.Reader) {
	defer close(reader.results)
	scanner := newSSEScanner(source)
	var eventName string
	var eventID string
	var retry time.Duration
	var dataLines []string
	hasFields := false

	emit := func() bool {
		if !hasFields && len(dataLines) == 0 {
			return true
		}
		result := sseReadResult{event: SSEEvent{
			Event: eventName,
			Data:  strings.Join(dataLines, "\n"),
			ID:    eventID,
			Retry: retry,
		}}
		eventName = ""
		eventID = ""
		retry = 0
		dataLines = nil
		hasFields = false
		select {
		case <-reader.done:
			return false
		case reader.results <- result:
			return true
		}
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if !emit() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			eventName = value
			hasFields = true
		case "data":
			dataLines = append(dataLines, value)
			hasFields = true
		case "id":
			eventID = value
			hasFields = true
		case "retry":
			if milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && milliseconds >= 0 {
				retry = time.Duration(milliseconds) * time.Millisecond
			}
			hasFields = true
		default:
			trimmed := strings.TrimSpace(line)
			if !hasFields && (trimmed == "[DONE]" || json.Valid([]byte(trimmed))) {
				dataLines = append(dataLines, trimmed)
				if !emit() {
					return
				}
				continue
			}
			dataLines = append(dataLines, line)
			hasFields = true
		}
	}
	if !emit() {
		return
	}
	if err := scanner.Err(); err != nil {
		select {
		case <-reader.done:
		case reader.results <- sseReadResult{err: err}:
		}
	}
}

func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, SSEBufInitial), SSEBufMax)
	return s
}

// scanSSEWithTimeout 使用 scanner 读取 SSE 流，带超时保护。每次成功读取一行后重置超时计时器。
// 这避免了长工作流程中因 IdleConnTimeout 或无心跳导致的连接静默断开。
// 返回 (line string, hasMore bool, err error)。
func scanSSEWithTimeout(ctx context.Context, scanner *bufio.Scanner, timeout time.Duration) (string, bool, error) {
	type scanResult struct {
		line    string
		hasMore bool
	}
	resultCh := make(chan scanResult, 1)

	go func() {
		hasMore := scanner.Scan()
		resultCh <- scanResult{line: scanner.Text(), hasMore: hasMore}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return "", false, ctx.Err()
	case <-timer.C:
		return "", false, fmt.Errorf("stream read timeout after %v", timeout)
	case result := <-resultCh:
		if !result.hasMore {
			if err := scanner.Err(); err != nil {
				return "", false, err
			}
			return "", false, nil // EOF
		}
		return result.line, true, nil
	}
}
