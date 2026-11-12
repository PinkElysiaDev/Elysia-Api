package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"testing/iotest"
)

func TestReadBoundedBodySizeParity(t *testing.T) {
	failure := errors.New("read failure")
	for _, data := range [][]byte{nil, []byte("hello"), bytes.Repeat([]byte("x"), 4096)} {
		for _, limit := range []int{0, 3, 5, 4096, 4097} {
			for _, hint := range []int64{-1, 0, 1, 5, 4096, 1 << 40} {
				for _, wrap := range []func(io.Reader) io.Reader{
					func(r io.Reader) io.Reader { return r },
					func(r io.Reader) io.Reader { return iotest.OneByteReader(r) },
					func(r io.Reader) io.Reader { return iotest.DataErrReader(r) },
					func(r io.Reader) io.Reader { return io.MultiReader(r, iotest.ErrReader(failure)) },
					func(r io.Reader) io.Reader { return io.MultiReader(r, iotest.ErrReader(&http.MaxBytesError{Limit: 2})) },
				} {
					want, wantErr := ReadBoundedBody(wrap(bytes.NewReader(data)), limit)
					got, gotErr := ReadBoundedBodyWithSize(wrap(bytes.NewReader(data)), limit, hint)
					if !bytes.Equal(got, want) || (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
						t.Fatalf("length=%d limit=%d hint=%d: %v / %v", len(data), limit, hint, gotErr, wantErr)
					}
				}
			}
		}
	}
}

func BenchmarkBoundedBodySize(b *testing.B) {
	for _, size := range []int{105, 4096, 2 << 20, 31 << 20} {
		data := bytes.Repeat([]byte("x"), size)
		for _, known := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/known-%t", size, known), func(b *testing.B) {
				hint := int64(-1)
				if known {
					hint = int64(size)
				}
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					got, err := ReadBoundedBodyWithSize(bytes.NewReader(data), 32<<20, hint)
					if err != nil || len(got) != size {
						b.Fatal("read failed", err)
					}
				}
			})
		}
	}
}
