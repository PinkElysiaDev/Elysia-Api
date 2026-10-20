package protocol

import (
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
)

// JSONToolSnapshotSuffix handles a complete JSON tool snapshot after streamed
// arguments. Semantic serialization may change whitespace or escaping, but
// cannot change types, exact numbers, or values. Partial JSON and text inputs
// retain the ordinary append-only prefix rule. Callers retain their original
// bytes, including those used for continuation provenance.
func JSONToolSnapshotSuffix(previous, snapshot string) (string, error) {
	if strings.HasPrefix(snapshot, previous) {
		return snapshot[len(previous):], nil
	}
	a, aerr := ParseValue([]byte(previous))
	b, berr := ParseValue([]byte(snapshot))
	if aerr == nil && berr == nil && sameJSONToolTokens(a, b) {
		return "", nil
	}
	return "", streamIssue(UpstreamContractViolation, "snapshot", "JSON tool snapshot changed already forwarded arguments")
}

// Sonic AST iterators retain duplicate properties and their order. Converting
// objects into maps here would discard information already sent downstream.
func sameJSONToolTokens(a, b Value) bool {
	return sameJSONToolNodes(ast.NewRaw(a.raw), ast.NewRaw(b.raw))
}

func sameJSONToolNodes(a, b ast.Node) bool {
	if a.TypeSafe() != b.TypeSafe() {
		return false
	}
	switch a.TypeSafe() {
	case ast.V_OBJECT:
		x, xe := a.Properties()
		y, ye := b.Properties()
		if xe != nil || ye != nil {
			return false
		}
		var left, right ast.Pair
		for {
			lx, ry := x.Next(&left), y.Next(&right)
			if !lx || !ry {
				return lx == ry && a.Check() == nil && b.Check() == nil
			}
			if left.Key != right.Key || !sameJSONToolNodes(left.Value, right.Value) {
				return false
			}
		}
	case ast.V_ARRAY:
		x, xe := a.Values()
		y, ye := b.Values()
		if xe != nil || ye != nil {
			return false
		}
		var left, right ast.Node
		for {
			lx, ry := x.Next(&left), y.Next(&right)
			if !lx || !ry {
				return lx == ry && a.Check() == nil && b.Check() == nil
			}
			if !sameJSONToolNodes(left, right) {
				return false
			}
		}
	case ast.V_STRING:
		left, le := a.Raw()
		right, re := b.Raw()
		var x, y string
		return le == nil && re == nil && sonic.ConfigStd.UnmarshalFromString(left, &x) == nil && sonic.ConfigStd.UnmarshalFromString(right, &y) == nil && x == y
	default:
		left, le := a.InterfaceUseNumber()
		right, re := b.InterfaceUseNumber()
		return le == nil && re == nil && equalJSON(left, right)
	}
}
