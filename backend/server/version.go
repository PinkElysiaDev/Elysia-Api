package server

// AppVersion 是后端版本标识。构建脚本（scripts/build-standalone.mjs、Dockerfile）
// 通过 -ldflags "-X github.com/elysia-api/backend/server.AppVersion=<版本>" 注入，
// 取值来自最近的 git tag；未注入的开发构建为 "dev"。
var AppVersion = "dev"
