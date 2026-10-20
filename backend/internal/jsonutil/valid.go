// Package jsonutil provides strict JSON validation using Sonic's decoder.
package jsonutil

import "github.com/bytedance/sonic/decoder"

// Valid checks one complete JSON value, including string escapes and control
// characters. UseNumber allows numbers outside the float64 range.
func Valid(data []byte) bool {
	d := decoder.NewDecoder(string(data))
	d.UseNumber()
	d.ValidateString()
	var value any
	return d.Decode(&value) == nil && d.CheckTrailings() == nil
}
