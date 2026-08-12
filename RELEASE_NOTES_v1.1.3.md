# Elysia-API v1.1.3 发布报告

## 版本概述

`v1.1.3` 是修复版本，聚焦三类稳定性缺陷：模型组删除导致的后台整页空白、工具调用 `id` 缺失导致的上游拒绝、以及 Responses 同协议透传丢失 `reasoning_text`。

---

## 主要变更细节

### 1. 修复删除被使用的模型组导致管理面板整页空白

- 零可见模型的模型组现在返回 `"models":[]` 而非 `null`，消除前端 `group.models.slice` 崩溃的根源；
- `DeleteGroup` 改为事务化操作，级联移除所有 API Key `allowedGroups` 中的组名引用，同时完整保留 usage 历史；
- 删除后同步清理该组的限流、轮询游标与粘滞路由运行时状态，避免残留内存；
- WebUI 增加数据归一化与根部 ErrorBoundary，异常时展示可重试界面而非白屏。

### 2. 修复工具调用 id 缺失导致的 `messages[N]: missing field id`

- Claude、OpenAI Chat、Responses 输入中缺失的工具调用 `id` 会生成确定性的 `call_<消息序号>_<调用序号>`；
- assistant 的 `tool_calls[].id` 与后续 `role:"tool"` 的 `tool_call_id` 保持一致；
- OpenAI 直通请求仅在确实缺少 id 时做最小修补，其余字节原样透传；流式渲染同步兜底。

### 3. 修复 Responses 同协议透传丢失 `reasoning_text`

- Responses→Responses 与 OpenAI 系同协议流改为原始 SSE 逐行转发，不再经 Maheshvara 重渲染；
- `response.reasoning_text.*` 等 provider 私有事件完整到达下游，多轮思考模式续传不再报 `reasoning_text must be passed back`；
- usage 统计、首字节耗时与错误处理逻辑保持不变。

---

## 验证

- 后端：`go test ./...` 与 `go vet ./...` 全部通过；
- 前端：TypeScript 编译与 Vite 构建通过；
- 新增存储、协议转换、流式透传回归测试覆盖上述场景。
