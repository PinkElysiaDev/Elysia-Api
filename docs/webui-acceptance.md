# WebUI Acceptance Checklist

只有本清单全部通过后，才能考虑废弃旧 `aggregator` 和 `orchestrator` 插件。

## 基础运行

- [ ] 不安装任何 Koishi 插件，直接运行后端，WebUI 可以登录并完成全部配置。
- [ ] 安装新 `koishi-plugin-elysia-api`，不安装 aggregator/orchestrator，也能启动后端并打开 WebUI。
- [ ] 新入口插件默认使用 `data/elysia-api-standalone/config.json`，不会覆盖旧 orchestrator 配置。
- [ ] 旧 aggregator + orchestrator 仍可在旧端口运行。

## 模型源覆盖

- [ ] OpenAI source 可自动拉取模型。
- [ ] OpenAI-compatible source 可自动拉取模型。
- [ ] Claude source 拉取失败时可看到 fallback 或错误提示。
- [ ] Gemini source 可自动拉取支持 `generateContent` 的模型。
- [ ] 手动 source 可新增多个模型并进入模型缓存。
- [ ] 禁用 source 后不会参与刷新与选择。

## 模型组覆盖

- [ ] 可创建旧 orchestrator 等价的 LLM group。
- [ ] 可配置 round-robin、sequential、random。
- [ ] 可配置 maxRetries、retryInterval。
- [ ] 可配置 maxConcurrency、dailyLimitMaxRequests、dailyLimitMaxTokens。
- [ ] `/v1/models` 和 `/v1beta/models` 能返回启用的 group。
- [ ] 请求 group name 能转发到组内模型。

## Token 与安全

- [ ] API token 可创建、禁用、删除。
- [ ] 未授权访问 `/api/admin/*` 返回 401。
- [ ] 未授权访问 `/v1/*` 返回 401。
- [ ] token 和 API key 不在列表、日志、usage 明文泄漏。

## Usage 与日志

- [ ] streaming 和 non-streaming 请求都会写入 usage。
- [ ] usage logs 支持分页和过滤。
- [ ] usage stats 与日志范围一致。
- [ ] reset usage 后统计清零。
- [ ] 系统日志页能显示刷新模型、错误等事件。

## 并行迁移

- [ ] 旧模式与新模式可不同端口同时运行。
- [ ] 同一模型源和模型组在新模式下的转发结果与旧模式一致。
- [ ] 新 WebUI 配置覆盖旧 aggregator/orchestrator 的所有常用配置能力。
- [ ] 迁移报告能说明哪些旧配置已导入、哪些需要用户手动处理。
