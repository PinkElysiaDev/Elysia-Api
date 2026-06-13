# Elysia API WebUI

Elysia-API 的配置控制台。完全基于后端 `/api/admin/*` REST API 工作，不依赖 Koishi。

技术栈：React 18 + Vite + TypeScript + TailwindCSS + Radix UI + SWR + Recharts + React Router (Hash)。

主题：日间 = 粉色 / 白色，夜间 = 粉色 / 黑色，跟随系统并可手动切换（持久化于 localStorage）。

## 开发

```bash
# 先启动后端（默认 127.0.0.1:8765）
npm run dev          # Vite dev server，端口 5273，已代理 /api /v1 /health 到后端
```

登录使用后端 bootstrap `config.json` 中的 `panelAccessToken`。

## 构建

```bash
npm run build        # 产物输出到 dist/，base 为 /ui/
```

将 `dist/` 部署为后端 `webuiDir`，后端通过 `gin.Static("/ui", webuiDir)` 提供服务，
访问 `http://<host>:<port>/ui/`。因为后端无 history fallback，前端使用 HashRouter。

## 页面

登录、概览、模型源、模型缓存、模型组、API Tokens、Usage 统计、Usage 日志、系统日志、运行配置、诊断。

所有破坏性操作（删除 source / group / token、reset usage）均二次确认；
所有列表均有 loading / empty / error 三态；secret 输入保存后即清空明文。
