package relay

import (
	"bufio"
	"io"
)

func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, SSEBufInitial), SSEBufMax)
	return s
}
