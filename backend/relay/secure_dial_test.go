package relay

import (
	"net"
	"strings"
	"syscall"
	"testing"
)

// secureControl 在禁止列表生效时应拒绝私网/保留 IP（连接时校验，关掉 rebinding 窗口）。
func TestSecureControlRejectsPrivateWhenNotAllowed(t *testing.T) {
	SetAllowPrivateDial(false)
	defer SetAllowPrivateDial(true) // 还原供其余测试使用（TestMain 默认开）
	t.Cleanup(func() { SetDeniedIPRanges(DefaultDeniedIPRanges) })

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

// 预置默认列表等价于旧 IsPrivateOrRestrictedIP 的全部固定语义：
// 逐段抽查各 CIDR 代表地址；公网地址放行。
func TestIsDeniedIP_DefaultPresetSemantics(t *testing.T) {
	t.Cleanup(func() { SetDeniedIPRanges(DefaultDeniedIPRanges) })
	SetDeniedIPRanges(DefaultDeniedIPRanges)

	denied := []string{
		"127.0.0.1", "127.255.255.255", // 环回 127.0.0.0/8
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", // RFC1918
		"169.254.169.254",          // 链路本地 / 云元数据
		"100.64.0.1",               // CGNAT
		"0.0.0.0",                  // 未指定 / 0.0.0.0/8
		"192.0.2.1",                // TEST-NET-1
		"198.51.100.1",             // TEST-NET-2
		"203.0.113.1",              // TEST-NET-3
		"198.18.0.5", "198.19.1.1", // 基准/fake-ip 198.18.0.0/15
		"240.0.0.1", "255.255.255.255", // 240.0.0.0/4
		"224.0.0.1", "239.255.255.255", // 组播 224.0.0.0/4
		"::1",                // IPv6 环回
		"::",                 // IPv6 未指定
		"fc00::1", "fd12::1", // fc00::/7
		"fe80::1",          // IPv6 链路本地
		"ff02::1",          // IPv6 组播
		"::ffff:127.0.0.1", // v4 映射环回
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34",
		"2606:4700:4700::1111", "2001:4860:4860::8888",
	}

	for _, s := range denied {
		if !IsDeniedIP(net.ParseIP(s)) {
			t.Fatalf("expected %s to be denied under default preset", s)
		}
	}
	for _, s := range allowed {
		if IsDeniedIP(net.ParseIP(s)) {
			t.Fatalf("expected %s to be allowed under default preset", s)
		}
	}
}

// IPv6 过渡地址（6to4/NAT64/Teredo）内嵌的 IPv4 段按解包结果判定——
// 静态 CIDR 覆盖不了的规避路径。
func TestIsDeniedIP_IPv6TransitionUnwrap(t *testing.T) {
	t.Cleanup(func() { SetDeniedIPRanges(DefaultDeniedIPRanges) })
	SetDeniedIPRanges(DefaultDeniedIPRanges)

	if !IsDeniedIP(net.ParseIP("2002:a9fe:a9fe::")) { // 6to4 内嵌 169.254.169.254
		t.Fatalf("expected 6to4 address embedding cloud metadata IP to be denied")
	}
	if !IsDeniedIP(net.ParseIP("64:ff9b::7f00:1")) { // NAT64 内嵌 127.0.0.1
		t.Fatalf("expected NAT64 address embedding loopback to be denied")
	}
	// 内嵌公网 IPv4 的过渡地址放行。
	if IsDeniedIP(net.ParseIP("2002:0808:0808::")) {
		t.Fatalf("expected 6to4 address embedding public 8.8.8.8 to be allowed")
	}
}

// 列表整体可替换：移除环回段后 127.0.0.1 放行、其余段继续拦截；
// 清空列表 = 全放行；非法条目跳过。
func TestSetDeniedIPRanges_ReplaceAndEmpty(t *testing.T) {
	t.Cleanup(func() { SetDeniedIPRanges(DefaultDeniedIPRanges) })

	trimmed := []string{}
	for _, entry := range DefaultDeniedIPRanges {
		if entry == "127.0.0.0/8" {
			continue
		}
		trimmed = append(trimmed, entry)
	}
	SetDeniedIPRanges(trimmed)
	if IsDeniedIP(net.ParseIP("127.0.0.1")) {
		t.Fatalf("expected loopback allowed after removing 127.0.0.0/8 from deny list")
	}
	if !IsDeniedIP(net.ParseIP("10.0.0.1")) {
		t.Fatalf("expected private range still denied after trimming only loopback")
	}

	SetDeniedIPRanges(nil)
	if IsDeniedIP(net.ParseIP("127.0.0.1")) || IsDeniedIP(net.ParseIP("192.168.1.1")) {
		t.Fatalf("expected empty deny list to allow everything")
	}

	SetDeniedIPRanges([]string{"not-a-cidr", "10.0.0.0/8"})
	if IsDeniedIP(net.ParseIP("10.0.0.1")) == false || IsDeniedIP(net.ParseIP("172.16.0.1")) {
		t.Fatalf("expected invalid entries skipped, valid ones enforced")
	}
}
