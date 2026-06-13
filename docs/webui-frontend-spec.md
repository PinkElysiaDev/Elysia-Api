# WebUI Frontend Spec

WebUI 目标：完整覆盖旧 `aggregator` 与 `orchestrator` 的配置能力，并只通过新后端 `/api/admin/*` 工作。

## 页面结构

1. 登录页
   - 输入 `panelAccessToken`。
   - token 存储在浏览器本地，所有请求添加 `Authorization: Bearer <token>`。
   - 401 时清除本地 token 并回到登录页。

2. 概览页
   - 调用 `GET /api/admin/health` 显示后端状态、SQLite 状态和内存指标。
   - 调用 `GET /api/admin/usage/stats` 显示请求数、成功率、token 总量、平均耗时。
   - 提供快捷入口：刷新全部模型、查看 usage 日志、查看系统日志。

3. 模型源页
   - `GET /api/admin/model-sources` 列表。
   - 新增/编辑/删除 source。
   - 支持 OpenAI、OpenAI-compatible、Claude、Gemini。
   - 支持自动拉取与手动模型两种模式。
   - 单个 source 刷新：`POST /api/admin/model-sources/:id/fetch`。

4. 模型缓存页
   - `GET /api/admin/models` 展示聚合后的模型。
   - `POST /api/admin/models/refresh` 全量刷新。
   - 按 source 分组，支持平台、类型、能力过滤。

5. 模型组页
   - `GET /api/admin/model-groups` 列表。
   - 新增/编辑/删除 group。
   - 从模型缓存中选择模型。
   - 配置策略、重试、限流、能力和 maxTokens。
   - group `name` 是客户端 `/v1/models` 看到的模型 ID。

6. API Tokens 页
   - `GET /api/admin/api-tokens` 列表。
   - 新增/编辑/删除 token。
   - token 明文仅在创建/编辑表单短暂存在，列表展示脱敏值。

7. Usage 统计页
   - `GET /api/admin/usage/stats` 展示时间范围内汇总。
   - 支持时间范围、key、group、model、status 过滤。

8. Usage 日志页
   - `GET /api/admin/usage/logs` 分页表格。
   - `GET /api/admin/usage/logs/:id` 查看详情。
   - 展示截断标记，提醒用户请求/响应体可能不是完整内容。
   - `POST /api/admin/usage/reset` 需要二次确认。

9. 系统日志页
   - `GET /api/admin/logs` 分页展示。
   - 支持 level 过滤。

10. 运行配置页
    - `GET /api/admin/runtime-config` 展示当前配置。
    - `PUT /api/admin/runtime-config` 修改可热更新字段。
    - host/port 变化时提示需要入口插件或用户重启后端。

11. 诊断页
    - 展示 `/api/admin/health` 内存指标。
    - 如果后端启用 pprof，可提示用户访问 `/debug/pprof`。

## 旧插件功能覆盖映射

- Aggregator sources → 模型源页。
- Aggregator manualModels → 模型源页的手动模型表格。
- Aggregator reload/list → 模型缓存页刷新和列表。
- Orchestrator tokens → API Tokens 页。
- Orchestrator modelGroups → 模型组页。
- Orchestrator usage dashboard → Usage 统计页 + Usage 日志页。
- Orchestrator backend status/reload/reset → 概览页、运行配置页、Usage 页。

## UX 要求

- 所有 destructive 操作必须二次确认：删除 source、删除 group、删除 token、reset usage。
- 保存 secret 后立即清空明文输入框。
- 所有列表必须支持 loading、empty、error 三种状态。
- 不假设 Koishi 存在；WebUI 应完全基于后端 API 工作。
