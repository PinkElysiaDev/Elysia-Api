package server

// AppVersion 是后端版本标识。构建脚本（scripts/build-standalone.mjs、Dockerfile）
// 通过 -ldflags "-X github.com/elysia-api/backend/server.AppVersion=<版本>" 注入，
// 取值来自最近的 git tag；未注入的开发构建为 "dev"。
var AppVersion = "dev"

// AppCommit 是构建对应的 git 短哈希（同由构建脚本经 ldflags 注入）。版本号
// 在两次发版之间不变，无法区分构建新旧——缓存修复这类"刚落地就要验证"的
// 场景靠它确认二进制确实包含目标提交。未注入时为空。
var AppCommit = ""

// BuildIdentity 返回 "版本+提交" 形态的构建标识，用于启动日志与 /health。
func BuildIdentity() string {
	if AppCommit == "" {
		return AppVersion
	}
	return AppVersion + "+" + AppCommit
}
