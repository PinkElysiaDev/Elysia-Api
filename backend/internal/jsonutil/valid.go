// Package jsonutil provides strict JSON validation without materializing values.
package jsonutil

import "encoding/json"

// Valid checks one complete JSON value, including string escapes and control
// characters. Syntax validation never converts numbers or builds an object tree.
func Valid(data []byte) bool {
	return json.Valid(data)
}
