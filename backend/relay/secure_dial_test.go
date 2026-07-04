package relay

import (
	"net"
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

// SetAllowFakeIPRanges（生产开关）开启后仅放行 Clash/Mihomo TUN fake-ip 段，
// 其余私网/元数据/环回/CGNAT 段仍拦截；关闭后 fake-ip 段恢复拦截。
func TestIsPrivateOrRestrictedIP_FakeIPExemption(t *testing.T) {
	t.Cleanup(func() { SetAllowFakeIPRanges(false) })

	mustIP := func(s string) net.IP {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad ip literal %q", s)
		}
		return ip
	}

	fakeIPs := []string{
		"198.18.0.5", "198.19.1.1", "198.18.255.254", // 198.18.0.0/15
		"240.0.0.1", "255.255.255.255", // 240.0.0.0/4
	}
	stillRestricted := []string{
		"10.0.0.1", "172.16.0.1", "192.168.1.1", // RFC1918 私网
		"127.0.0.1",       // 环回
		"169.254.169.254", // 链路本地 / 云元数据
		"100.64.0.1",      // CGNAT
		"0.0.0.0",         // 未指定 / 0.0.0.0/8
		"192.0.2.1",       // TEST-NET-1
		"198.51.100.1",    // TEST-NET-2
		"203.0.113.1",     // TEST-NET-3
		"::1",             // IPv6 环回
		"fc00::1",         // IPv6 ULA
		"fe80::1",         // IPv6 链路本地
	}

	// 默认（开关关闭）：fake-ip 段被视为受限。
	SetAllowFakeIPRanges(false)
	for _, s := range fakeIPs {
		if !IsPrivateOrRestrictedIP(mustIP(s)) {
			t.Fatalf("expected %s to be restricted when fake-ip exemption is OFF", s)
		}
	}

	// 开启：仅放行 fake-ip 段，其余受限段仍拦截。
	SetAllowFakeIPRanges(true)
	for _, s := range fakeIPs {
		if IsPrivateOrRestrictedIP(mustIP(s)) {
			t.Fatalf("expected %s to be ALLOWED when fake-ip exemption is ON", s)
		}
	}
	for _, s := range stillRestricted {
		if !IsPrivateOrRestrictedIP(mustIP(s)) {
			t.Fatalf("expected %s to remain restricted even with fake-ip exemption ON", s)
		}
	}

	// 关回后恢复拦截。
	SetAllowFakeIPRanges(false)
	if !IsPrivateOrRestrictedIP(mustIP("198.18.0.5")) {
		t.Fatalf("expected 198.18.0.5 to be restricted again after turning exemption OFF")
	}
}
