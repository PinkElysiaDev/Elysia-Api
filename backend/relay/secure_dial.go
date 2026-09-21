package relay

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// DefaultDeniedIPRanges 是出站禁止 IP 段的预置默认（CIDR），覆盖旧固定判定
// IsPrivateOrRestrictedIP 的全部语义：环回、私网、组播、未指定、链路本地、
// CGNAT、文档/基准/保留段及对应 IPv6 段。列表整体可配置（config 的
// outbound.deniedIpRanges）：用户可在运行时配置页增删段（例如放行本机
// 127.0.0.1 上游时移除 127.0.0.0/8），清空即全放行。
var DefaultDeniedIPRanges = []string{
	"0.0.0.0/8",       // 未指定 + 本网络
	"10.0.0.0/8",      // RFC1918 私网
	"100.64.0.0/10",   // CGNAT 运营商级 NAT
	"127.0.0.0/8",     // 环回
	"169.254.0.0/16",  // 链路本地（含云元数据 169.254.169.254）
	"172.16.0.0/12",   // RFC1918 私网
	"192.0.2.0/24",    // TEST-NET-1（文档/示例）
	"192.168.0.0/16",  // RFC1918 私网
	"198.18.0.0/15",   // 基准测试段（Clash/Mihomo TUN fake-ip）
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // 组播
	"240.0.0.0/4",     // 保留（含 fake-ip 备用段）
	"::1/128",         // IPv6 环回
	"::/128",          // IPv6 未指定
	"fc00::/7",        // IPv6 唯一本地地址
	"fe80::/10",       // IPv6 链路本地
	"ff00::/8",        // IPv6 组播
}

// allowPrivateDial 仅供测试开启：完全放行出站连接，以便用 httptest 的
// 127.0.0.1 上游做端到端测试。生产恒为 false。
var allowPrivateDial atomic.Bool

// SetAllowPrivateDial 切换是否完全放行出站连接（跳过禁止列表）。仅测试调用。
func SetAllowPrivateDial(allow bool) { allowPrivateDial.Store(allow) }

// deniedIPRanges 是配置驱动的禁止出站 IP 段（已解析的 CIDR），由 server 层
// 在启动/热重载/admin 改配置/agent 改配置后调用 SetDeniedIPRanges 下发。
// 连接时校验（secureControl）与 server 层预校验（validateOutboundBaseURL）
// 共用同一份判定，两层同时生效。空列表 = 全放行。
var deniedIPRanges atomic.Pointer[[]*net.IPNet]

func init() {
	SetDeniedIPRanges(DefaultDeniedIPRanges)
}

// SetDeniedIPRanges 整体替换禁止出站 IP 段。非法 CIDR 条目跳过并记日志
// （不因个别坏条目让整份配置失效）；全部条目非法时列表为空 = 全放行，
// 调用方（admin/agent 工具）应在上游先做逐条校验避免这种情况静默发生。
func SetDeniedIPRanges(ranges []string) {
	parsed := make([]*net.IPNet, 0, len(ranges))
	for _, entry := range ranges {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, ipNet, err := net.ParseCIDR(entry); err == nil {
			parsed = append(parsed, ipNet)
		} else {
			log.Printf("[outbound-policy] ignoring invalid CIDR %q: %v", entry, err)
		}
	}
	deniedIPRanges.Store(&parsed)
}

// DeniedIPRanges 返回当前生效的禁止段（解析后的 CIDR 文本，去零值）。
// 供诊断与测试使用。
func DeniedIPRanges() []string {
	current := deniedIPRanges.Load()
	if current == nil {
		return nil
	}
	out := make([]string, 0, len(*current))
	for _, ipNet := range *current {
		out = append(out, ipNet.String())
	}
	return out
}

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
	if IsDeniedIP(ip) {
		return fmt.Errorf("refused to dial denied IP %s (see outbound.deniedIpRanges)", ip.String())
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

// IsDeniedIP 判定 IP 是否落在配置的禁止出站段内。除静态 CIDR 匹配外，
// IPv6 过渡地址（6to4/NAT64/Teredo）会解包内嵌 IPv4 递归判定——它们在
// IPv6 报文里内嵌一个 IPv4 地址，To4() 对其返回 nil（非 v4 映射形式），
// 从而绕过 v4 段表（例如 2002:a9fe:a9fe:: 内嵌 169.254.169.254 云元数据）。
// ip 为 nil 视为异常，拒绝。
func IsDeniedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if current := deniedIPRanges.Load(); current != nil {
		for _, ipNet := range *current {
			if ipNet.Contains(ip) {
				return true
			}
		}
	}
	if len(ip) == net.IPv6len {
		var embedded net.IP
		if ip[0] == 0x20 && ip[1] == 0x02 { // 2002::/16 6to4：字节 2-5
			embedded = net.IPv4(ip[2], ip[3], ip[4], ip[5])
		} else if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b { // 64:ff9b::/96 NAT64：字节 12-15
			embedded = net.IPv4(ip[12], ip[13], ip[14], ip[15])
		} else if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x00 && ip[3] == 0x00 { // 2001::/32 Teredo：字节 12-15 取反
			embedded = net.IPv4(^ip[12], ^ip[13], ^ip[14], ^ip[15])
		}
		if embedded != nil && IsDeniedIP(embedded) {
			return true
		}
	}
	return false
}
