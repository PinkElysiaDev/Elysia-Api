// Package webui 把构建好的 WebUI 静态资源通过 //go:embed 嵌入二进制，
// 使后端开箱即用——无需任何外部目录或 webuiDir 配置即可在 /ui/ 提供控制台。
//
// dist/ 下是前端构建产物（packages/webui 的 vite build 输出），由
// scripts/build-standalone.mjs 在发布前同步，不随仓库提交；仓库只跟踪
// dist/.gitkeep 占位文件，保证全新 clone 在未构建前端时也能 `go build`
// （此时 /ui 优雅降级为不可用，见 FS/HasIndex）。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS 返回以 dist 为根的子文件系统，可直接交给 http.FS 托管。
// 第二个返回值表示是否含真实可用的资源（index.html 存在）。
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, false
	}
	return sub, HasIndex()
}

// HasIndex 报告内嵌资源里是否存在 dist/index.html，
// 用于区分「真有构建产物」与「空目录占位」。
func HasIndex() bool {
	f, err := assets.Open("dist/index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
