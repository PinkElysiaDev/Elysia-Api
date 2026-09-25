package server

import "testing"

// 通配监听地址归一为回环；显式地址原样保留。
func TestConsoleLaunchURL(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
		want string
	}{
		{"", 8765, "http://127.0.0.1:8765/ui/"},
		{"0.0.0.0", 3000, "http://127.0.0.1:3000/ui/"},
		{"::", 3000, "http://127.0.0.1:3000/ui/"},
		{"[::]", 3000, "http://127.0.0.1:3000/ui/"},
		{"localhost", 3000, "http://127.0.0.1:3000/ui/"},
		{"LOCALHOST", 3000, "http://127.0.0.1:3000/ui/"},
		{"127.0.0.1", 8765, "http://127.0.0.1:8765/ui/"},
		{"192.168.1.10", 8765, "http://192.168.1.10:8765/ui/"},
		{"gw.example.com", 8765, "http://gw.example.com:8765/ui/"},
		// 显式 IPv6 字面量必须加方括号，否则浏览器无法解析；
		// Go 监听地址的既成方括号写法不能被双重包裹。
		{"::1", 8765, "http://[::1]:8765/ui/"},
		{"[::1]", 8765, "http://[::1]:8765/ui/"},
		{"2001:db8::1", 8765, "http://[2001:db8::1]:8765/ui/"},
	} {
		if got := consoleLaunchURL(tc.host, tc.port); got != tc.want {
			t.Fatalf("consoleLaunchURL(%q, %d) = %q want %q", tc.host, tc.port, got, tc.want)
		}
	}
}
