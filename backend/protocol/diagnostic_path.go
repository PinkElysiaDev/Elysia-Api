package protocol

import "strconv"

// A path is borrowed scratch for one synchronous depth-first walk. Children may
// overwrite siblings after they return; only String escapes into a diagnostic.
// A small stack buffer at each walk's root covers ordinary shallow content, with
// append's ordinary growth retaining correctness for unusually deep inputs.
type diagnosticPath []byte

func (path diagnosticPath) suffix(suffix string) diagnosticPath { return append(path, suffix...) }
func (path diagnosticPath) index(index int) diagnosticPath {
	return strconv.AppendInt(append(path, '/'), int64(index), 10)
}
func (path diagnosticPath) String() string { return string(path) }
