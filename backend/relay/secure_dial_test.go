package relay

import (
	"strings"
	"syscall"
	"testing"
)

// secureControl 在 toggle 关闭时应拒绝私网/保留 IP（连接时校验，关掉 rebinding 窗口）。
func TestSecureControlRejectsPrivateWhenNotAllowed(t *testing.T) {
	SetAllowPrivateDial(false)
	defer SetAllowPrivateDial(true) // 还原供其余测试使用（TestMain 默认开）

	rejects := []string{
		"127.0.0.1:443",
		"10.1.2.3:80",
		"169.254.169.254:80", // 云元数据
		"192.168.0.1:443",
	}
	for _, addr := range rejects {
		if err := secureControl("tcp", addr, syscall.RawConn(nil)); err == nil {
			t.Fatalf("expected secureControl to reject private dial target %q", addr)
		}
	}

	if err := secureControl("tcp", "8.8.8.8:443", syscall.RawConn(nil)); err != nil {
		t.Fatalf("public IP should be allowed, got %v", err)
	}
}

// toggle 打开时（测试模式）放行私网，否则 httptest 的 127.0.0.1 上游无法连通。
func TestSecureControlAllowsPrivateWhenToggled(t *testing.T) {
	SetAllowPrivateDial(true)
	if err := secureControl("tcp", "127.0.0.1:8080", syscall.RawConn(nil)); err != nil {
		t.Fatalf("toggle on should allow loopback, got %v", err)
	}
}

func TestSecureControlRejectsNonIP(t *testing.T) {
	SetAllowPrivateDial(false)
	defer SetAllowPrivateDial(true)
	if err := secureControl("tcp", "not-an-ip", syscall.RawConn(nil)); err == nil || !strings.Contains(err.Error(), "non-IP") {
		t.Fatalf("expected non-IP address to be refused, got %v", err)
	}
}
