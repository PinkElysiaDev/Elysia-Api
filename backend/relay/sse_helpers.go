package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"
)

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
