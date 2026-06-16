package server

import (
	"os"
	"testing"

	"github.com/elysia-api/backend/relay"
)

// TestMain 为整个 server 测试包放行私网/环回出站连接：测试用 httptest 起的
// 上游恒为 127.0.0.1，生产路径的连接时 SSRF 校验（relay.secureControl）会拒绝
// 这类地址。仅测试进程内开启，不影响生产。
func TestMain(m *testing.M) {
	relay.SetAllowPrivateDial(true)
	os.Exit(m.Run())
}
