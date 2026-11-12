package protocol

import "io"

// ReadBoundedBodyWithSize treats size only as an allocation hint, never a limit
// or proof of the body's actual length. Large and unknown sizes keep the chunked
// reader, avoiding eager allocations near the configured request limit.
func ReadBoundedBodyWithSize(reader io.Reader, limit int, size int64) ([]byte, error) {
	if size <= 0 || size > min(int64(limit), 8<<20) {
		return ReadBoundedBody(reader, limit)
	}
	return readBoundedBody(reader, limit, int(size))
}

// One spare byte lets an accurately sized body reach EOF without reallocating,
// while still detecting a dishonest length. The caller always bounds the reader.
func readBodyWithSize(reader io.Reader, size int) ([]byte, error) {
	if size == 0 {
		return io.ReadAll(reader)
	}
	body := make([]byte, 0, size+1)
	for len(body) < cap(body) {
		n, err := reader.Read(body[len(body):cap(body)])
		body = body[:len(body)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return body, err
		}
	}
	// A hint can be wrong. Preserve the original read-to-limit behavior and
	// error semantics rather than truncating or trusting the advertised length.
	tail, err := io.ReadAll(reader)
	return append(body, tail...), err
}
