package server

// 启动时自动在系统默认浏览器打开控制台。默认（未配置）尝试打开——桌面
// 直跑场景开箱即用；作为子进程托管（如 Koishi 插件宿主）或无桌面环境用
// config.json 的 "openBrowserOnStart": false 显式关闭；打开命令缺失时静默
// 跳过，不影响启动。

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
)

// consoleLaunchURL 由监听地址推导浏览器可访问的控制台地址：通配监听
// （空/0.0.0.0/::/localhost）归一为回环，避免浏览器解析到未监听的栈。
func consoleLaunchURL(host string, port int) string {
	browserHost := strings.TrimSpace(host)
	switch strings.ToLower(browserHost) {
	case "", "0.0.0.0", "::", "[::]", "localhost":
		browserHost = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d/ui/", browserHost, port)
}

// launchBrowser 尽力打开系统默认浏览器；命令缺失（典型为无桌面服务器）
// 返回 false，调用方静默降级。
func launchBrowser(url string) bool {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 走 ShellExecute 路径，不会闪 cmd 窗口。
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return false
	}
	go func() { _ = command.Wait() }()
	return true
}

// launchConsoleBrowser 按配置打开控制台（nil = 默认尝试）。必须在监听
// 建立之后调用——起不来就不弹窗。
func launchConsoleBrowser(openBrowser *bool, host string, port int) {
	if openBrowser != nil && !*openBrowser {
		return
	}
	url := consoleLaunchURL(host, port)
	if launchBrowser(url) {
		log.Printf("已在默认浏览器打开控制台: %s（config.json 可设 openBrowserOnStart: false 关闭）", url)
	}
}
