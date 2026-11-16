package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func compileBuiltinPackage(t testing.TB, name string) *p.Compiled {
	t.Helper()
	return testCompiled(t, name)
}
