package relay

import (
	"os"
	"testing"
)

// TestMain 为整个 relay 测试包放行私网/环回出站连接：测试用 httptest 起的
// 上游恒为 127.0.0.1，若不放行会被连接时 SSRF 校验（secureControl）拒绝。
// 生产路径不调用 SetAllowPrivateDial，恒为 false。
func TestMain(m *testing.M) {
	SetAllowPrivateDial(true)
	os.Exit(m.Run())
}
