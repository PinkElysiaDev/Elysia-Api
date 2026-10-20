package atomicfile

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

func BenchmarkAtomicWrite(b *testing.B) {
	for _, size := range []int{256, 64 << 10, 1 << 20} {
		data := bytes.Repeat([]byte("x"), size)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "state.json")
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				if err := WriteFile(path, data, 0o600); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
