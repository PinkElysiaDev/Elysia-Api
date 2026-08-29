package relay

import (
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"syscall"
	"time"
)

// allowPrivateDial 仅供测试开启：放行到私网/环回地址的出站连接，
// 以便用 httptest 的 127.0.0.1 上游做端到端转发测试。生产恒为 false。
var allowPrivateDial atomic.Bool

// SetAllowPrivateDial 切换是否放行私网/环回出站连接。仅测试调用。
func SetAllowPrivateDial(allow bool) { allowPrivateDial.Store(allow) }

// allowFakeIPRanges 由配置驱动（生产开关）：开启后放行 Clash/Mihomo TUN
// fake-ip 段（198.18.0.0/15、240.0.0.0/4），其余私网/元数据/环回/CGNAT 段
// 仍按 IsPrivateOrRestrictedIP 拦截。由 server 层在启动/热重载/admin 改配置后
// 调用 SetAllowFakeIPRanges 下发，供连接时校验（secureControl）与 server 层
// 预校验（validateOutboundBaseURL）共用同一份判定，两层同时生效。
var allowFakeIPRanges atomic.Bool

// SetAllowFakeIPRanges 切换是否放行 fake-ip 段出站连接。生产由配置驱动。
func SetAllowFakeIPRanges(allow bool) { allowFakeIPRanges.Store(allow) }

// secureControl 是 net.Dialer.Control 回调：在 DNS 解析之后、实际 connect 之前，
// 拿到「将要连接的真实 IP:port」并校验。这关掉了 DNS rebinding 的 TOCTOU 窗口——
// 校验的就是即将连接的那个 IP，而非更早一次独立解析的结果。
func secureControl(network, address string, _ syscall.RawConn) error {
	if allowPrivateDial.Load() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Control 阶段 address 已是解析后的 IP；解析不出 IP 视为异常，拒绝。
		return fmt.Errorf("refused to dial non-IP address %q", address)
	}
	if IsPrivateOrRestrictedIP(ip) {
		return fmt.Errorf("refused to dial private or restricted IP %s", ip.String())
	}
	return nil
}

// NewSecureTransport 导出带连接时 SSRF 校验的 http.Transport，供 server 层
// 非转发出站路径（健康探测等）复用同一份拨号防护——裸 http.Client 跟随
// 重定向时无连接级校验，可能被引到内网/元数据地址。
func NewSecureTransport() *http.Transport {
	return newSecureTransport()
}

// newSecureTransport 构造带连接时 SSRF 校验的 http.Transport。
// 所有上游适配器（OpenAI/Claude/Gemini）共用，确保出站连接的目标 IP
// 在 connect 时被校验，杜绝 rebinding 绕过。
func newSecureTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   secureControl,
	}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
}

// IsPrivateOrRestrictedIP 判定 IP 是否属于私网/环回/链路本地/保留段等
// 不应作为出站上游的地址。集中在 relay 层，供连接时校验与 server 层
// 预校验（validateOutboundBaseURL）共用同一份判定逻辑。
func IsPrivateOrRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return true
	}

	if v4 := ip.To4(); v4 != nil {
		// 配置驱动的 fake-ip 豁免：开启后放行 Clash/Mihomo TUN fake-ip 段。
		// 必须在下面的保留段判定之前短路，否则 198.18.0.0/15（基准测试段）
		// 与 240.0.0.0/4（保留段）会被当作受限段拒绝，TUN 代理下所有上游
		// 域名都被解析到该段而被误杀。其余私网/元数据/环回段不受影响。
		if allowFakeIPRanges.Load() {
			if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) { // 198.18.0.0/15
				return false
			}
			if v4[0] >= 240 { // 240.0.0.0/4
				return false
			}
		}
		switch {
		case v4[0] == 169 && v4[1] == 254: // 169.254.0.0/16 link-local（含云元数据 169.254.169.254）
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // 100.64.0.0/10 CGNAT
			return true
		case v4[0] == 0: // 0.0.0.0/8
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2: // 192.0.2.0/24 TEST-NET-1（文档/示例）
			return true
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19): // 198.18.0.0/15 基准测试
			return true
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100: // 198.51.100.0/24 TEST-NET-2
			return true
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113: // 203.0.113.0/24 TEST-NET-3
			return true
		case v4[0] >= 240: // 240.0.0.0/4 保留
			return true
		}
		return false
	}

	if len(ip) == net.IPv6len {
		if (ip[0] & 0xfe) == 0xfc { // fc00::/7 unique local
			return true
		}
		if ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 { // fe80::/10 link local
			return true
		}
		// IPv6 过渡范围会在 IPv6 报文里内嵌一个 IPv4 地址，To4() 对它们返回
		// nil（非 v4 映射形式），从而绕过上面的 v4 限制表——解包内嵌地址后
		// 递归走一遍 v4 判定（例如 2002:a9fe:a9fe:: 内嵌 169.254.169.254
		// 云元数据地址）。
		var embedded net.IP
		if ip[0] == 0x20 && ip[1] == 0x02 { // 2002::/16 6to4：字节 2-5
			embedded = net.IPv4(ip[2], ip[3], ip[4], ip[5])
		} else if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b { // 64:ff9b::/96 NAT64：字节 12-15
			embedded = net.IPv4(ip[12], ip[13], ip[14], ip[15])
		} else if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x00 && ip[3] == 0x00 { // 2001::/32 Teredo：字节 12-15 取反
			embedded = net.IPv4(^ip[12], ^ip[13], ^ip[14], ^ip[15])
		}
		if embedded != nil && IsPrivateOrRestrictedIP(embedded) {
			return true
		}
	}

	return false
}
