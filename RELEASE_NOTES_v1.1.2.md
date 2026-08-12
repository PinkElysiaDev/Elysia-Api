# Elysia-API v1.1.2 发布报告

## 📌 版本概述
`v1.1.2` 是一个修复与优化版本，聚焦于四类同协议透传的稳定化、工具调用格式转换修复、模型管理一致性，以及发布产物体积回归。

---

## 🛠️ 主要变更细节

### 1. 四种协议同协议透传稳定化 (Same-Protocol Passthrough)
- 将 Chat Completions、Responses、Claude Messages、Gemini GenerateContent 四类协议的同源透传改为**无条件默认启用**（不再依赖 `relay.passthrough` 开关，该字段标记为弃用）；
- 彻底修复 codex 多轮 Responses 请求中 `reasoning_text` 等富字段被有损重建导致上游报错（`reasoning_text ... must be passed back`）的隐患。

### 2. Anthropic → Chat 工具调用格式转换修复 (tool_calls)
- 修复 Claude `tool_result` 块被错误渲染为 `user` 文本消息的问题；
- 现在 `tool_result` 会正确转换为 OpenAI `role:"tool"` 消息并携带匹配的 `tool_call_id`，消除上游 `insufficient tool messages` 报错。

### 3. 模型源手动模型同步 (Manual Models)
- 手动添加的模型改为保存时**同步写入**模型缓存，消除前端 revalidate 竞态导致的「手动模型不更新模型组」问题。

### 4. 模型源删除/停用后的模型组清理 (Model Group Cleanup)
- 删除模型源时事务内同步清理 `model_group_models` 引用；
- 模型组列表查询按源 `enabled` 状态过滤，停用/删除源后不再残留旧模型选中。

### 5. 发布产物体积回归 (Release Binary Size)
- 交叉编译脚本补回 `-ldflags "-s -w"`，剥离符号表与 DWARF 调试信息；
- 四平台二进制由约 22MB 回落至约 16MB。

---

## 📦 独立交叉编译产物 (Standalone Release Assets)
本次发布的独立运行包包含静态 WebUI 嵌入，无需任何外部 Node.js / 前端环境依赖：

| 平台架构 | 可执行文件名 |
| :--- | :--- |
| **Windows AMD64** | `dist/standalone/elysia-api-windows-amd64.exe` |
| **Linux AMD64** | `dist/standalone/elysia-api-linux-amd64` |
| **macOS Intel (AMD64)** | `dist/standalone/elysia-api-darwin-amd64` |
| **macOS Apple Silicon (ARM64)** | `dist/standalone/elysia-api-darwin-arm64` |
