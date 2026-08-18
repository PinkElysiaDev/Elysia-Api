# Elysia-API v1.1.4 发布报告

## 版本概述

`v1.1.4` 修复 Anthropic Messages 转到 OpenAI Chat Completions 时 `tool_choice` 形态不合法的问题。Claude Code 默认发送的 `{"type":"auto"}` 不再原样进入 Chat 上游。

---

## 主要变更细节

### 1. 修复 Anthropic → Chat 的 `tool_choice` 转换

- Anthropic 对象形态现在映射为 Chat Completions 合法值：
  - `{"type":"auto"}` → `"auto"`
  - `{"type":"any"}` → `"required"`
  - `{"type":"none"}` → `"none"`
  - `{"type":"tool","name":"X"}` → `{"type":"function","function":{"name":"X"}}`
- Responses 扁平的 `{"type":"function","name":"X"}` 也会补成 Chat 的 `function` 嵌套对象；
- 不再把 `{"type":"auto"}` 原样发给 Chat 上游，消除 `Expected field function in tool_choice`。

### 2. 对齐并行工具调用开关

- Claude `disable_parallel_tool_use` 写入 `parallel_tool_calls`，再由 Chat 请求发出；
- Chat → Claude 时，若 `parallel_tool_calls=false`，会在 Claude `tool_choice` 对象上补 `disable_parallel_tool_use: true`。

### 3. 修复 Chat → Claude 时 `tool_choice` 被原始值覆盖

- 去掉 Claude 写出路径里用原始 `req.ToolChoice` 覆盖转换结果的逻辑；
- Chat 的 `"auto"` / `{"type":"function",...}` 现在稳定渲染为 Claude 的 `{"type":"auto"}` / `{"type":"tool","name":...}`。

---

## 验证

- 后端：`go test ./relay` 通过；
- 新增 `tool_choice` 表驱动测试与 Claude ↔ Chat 往返回归。
