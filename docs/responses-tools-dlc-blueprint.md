# Responses Tools DLC 蓝图

> 本文档是后续开发蓝图，不代表功能已经实现。当前阶段不修改运行时代码、不新增用户可见配置项、不改变现有 Responses 转换行为。

## 背景

`openai_responses -> anthropic` 在遇到 Responses builtin tool 时可能报错，例如：

```text
builtin tool "namespace" cannot be transformed to Claude messages
```

根因不是单纯缺少某个字段映射，而是 Responses builtin tools 与普通 function tools 的语义不同：

- builtin tools 带有执行位置语义，例如 OpenAI hosted、本地客户端执行、MCP server 执行、后端代理执行。
- 部分 builtin tools 使用专用 output item，例如 `shell_call`、`apply_patch_call`、`file_search_call`、`image_generation_call`。
- hosted execution 依赖远端平台资源，例如 OpenAI vector stores、code interpreter container、image generation service。
- Claude Messages 的普通 client tool 只表达 `tool_use` / `tool_result`，不能自动复刻 OpenAI hosted tool 的执行环境。

因此，Responses builtin tool 不能无脑转换成普通 Claude function tool。后续如果要兼容，应做成可选 DLC，而不是污染主 Responses 转换链路。

## 设计目标

- 默认不启用 Responses Tools DLC，避免影响现有功能。
- 主 Responses 转换链路保持简单：请求格式转换、响应格式转换、usage 记录仍是核心路径。
- builtin tool 兼容能力集中在独立模块中，便于后续逐步开发和回滚。
- 不假装支持 hosted-only 工具；无法保持语义时如实报错。
- shell、apply_patch 这类客户端执行工具默认桥接回 Codex 客户端，不由 elysia-api 后端执行。
- file_search、image_generation、MCP、sandbox 等能力必须有显式 executor 和配置后才能启用。

## 范围

### 当前阶段

- 只新增本文档。
- 不实现 Responses builtin tools 兼容。
- 不新增配置项。
- 不修改 `backend/relay`、`backend/server`、`backend/config` 的运行时代码。
- 不重新编译后端。

### 后续阶段

- 根据真实 Codex Responses 请求样本，逐步实现 namespace/function bridge。
- 在确定协议细节后实现 shell/apply_patch output item bridge。
- 只有在明确需求和执行器设计后，再考虑 file_search、image_generation、MCP、sandbox。

## 架构提案

后续可新增独立包，例如：

```text
backend/responsescompat/
  tools.go       # builtin tool 分类、namespace 展开、Claude tool name 编码/解码
  loop.go        # Claude tool_use loop、tool_result 回填、最大轮数和终止条件
  executors.go   # file_search、image_generation、MCP、sandbox executor 注册表
  config.go      # DLC 专属配置结构、默认关闭策略和配置校验
```

主流程只暴露窄接口：

```go
MaybePrepareTools(req, targetFormat, cfg)
MaybeRunToolLoop(ctx, upstreamClient, request, cfg)
ConvertToolUseToResponsesItem(toolUse, bridgeMeta)
```

DLC 关闭时，这些接口应全部 no-op，保证现有行为不变。

## 配置隔离

后续配置应集中在 `responses.tools` 下，避免污染全局配置：

```json
{
  "responses": {
    "tools": {
      "enabled": false,
      "mode": "off",
      "allowed": ["function", "namespace"],
      "executors": {
        "fileSearch": { "enabled": false },
        "imageGeneration": { "enabled": false },
        "mcp": { "enabled": false },
        "sandbox": { "enabled": false }
      }
    }
  }
}
```

建议默认值：

- `enabled`: `false`
- `mode`: `off`
- `allowed`: `function`、`namespace`
- 所有 executor 默认 disabled
- unsupported builtin tool 默认报错，不静默降级

## 工具处理矩阵

| 工具 | OpenAI/Responses 语义 | 建议策略 | 备注 |
| --- | --- | --- | --- |
| `function` | 应用侧执行普通函数 | 可直接转换 | 当前已有基础能力 |
| `namespace` | 工具命名空间或延迟加载后的工具集合 | 展开为 Claude client tools | 需要真实请求样本确认 raw shape |
| `shell` | 客户端或运行时执行命令，使用 `shell_call` / `shell_call_output` | 桥接回 Codex 客户端 | elysia-api 默认不执行命令 |
| `apply_patch` | 客户端 patch harness 执行，使用 `apply_patch_call` / `apply_patch_call_output` | 桥接回 Codex 客户端 | elysia-api 默认不改文件 |
| `web_search` / `web_search_preview` | OpenAI hosted search | 可映射 Claude 原生 web search | 语义标注为 provider-native substitute |
| `file_search` | OpenAI vector store hosted search | 需本地 vector store executor | 未配置时应报错 |
| `image_generation` | OpenAI 图像生成 hosted tool | 需图像生成 provider proxy | 可支持文生图/图生图全局默认配置 |
| `MCP` | OpenAI 远端连接 MCP server 并编排 tool call | 需本地 MCP client executor | server URL/auth 只留后端，不注入模型 |
| `code_interpreter` | OpenAI container 执行代码 | 需自建 sandbox 或 Claude 等价 server tool | 高成本，默认不建议实现 |
| `computer_use` | 截图/action loop 控制环境 | 需完整环境闭环 | 高风险，默认不建议实现 |

## Tool Loop 设计

如果启用后端执行器，Responses -> Claude 不再是一次性转换，而是需要 tool loop：

```text
Responses request
  -> canonical request
  -> Claude request with client tools
  -> Claude tool_use
  -> elysia-api executor
  -> Claude tool_result
  -> Claude final response
  -> Responses response
```

硬限制建议：

- `maxIterations`: 默认 4
- `maxToolCallsPerResponse`: 默认 8
- `toolTimeoutMs`: 默认 30000
- executor panic/error 必须转成结构化 tool_result 或明确 API error
- 所有敏感字段必须脱敏写入日志

## Executor 设计

### file_search

本地 file_search 不是简单读取文件路径，而是一个本地 RAG 子系统：

- 文件注册/上传 API
- vector store ID 管理
- chunking
- embedding
- 向量索引
- metadata filter
- top_k 检索
- 检索结果格式化为 tool_result

没有本地 vector store 时，`file_search` 应直接报错。

### image_generation

图片生成可作为 provider proxy：

- 全局配置 provider、model、size、quality、format、background。
- 支持文生图和图生图输入。
- 统一输出 base64 或 URL。
- 生成 Responses 兼容的 image generation output item。

未配置 provider 时，`image_generation` 应直接报错。

### MCP

OpenAI remote MCP 是 OpenAI 服务器连接 MCP server；本地 DLC 模式应改为 elysia-api 后端连接 MCP server：

- 解析 request 中 MCP tool 的 `server_label`、`server_url`、`allowed_tools`、`require_approval` 等信息。
- auth/header/server_url 只保存在后端执行上下文，不注入 Claude prompt。
- Claude 只看到工具名称、描述和 input schema。
- Claude 发起 tool_use 后，由 elysia-api 调用 MCP server 并回传 tool_result。

### sandbox

自建 sandbox 成本高，建议只作为实验 executor：

- 进程隔离
- 文件系统隔离
- 网络策略
- CPU/内存/时间限制
- stdout/stderr 截断
- 临时目录清理
- Windows/Linux 行为差异处理

默认不启用，不作为 namespace/function bridge 的前置条件。

## 风险

### 必要性风险

大多数普通代理场景不需要 builtin tools。DLC 默认关闭，避免为少数 Codex/Responses 场景增加主路径复杂度。

### 兼容性风险

Responses builtin output item 类型很多，直接转普通 function 会破坏客户端协议。必须按工具类型分别处理。

### 配置污染风险

所有配置必须集中在 `responses.tools` 下，且高级 executor 配置默认折叠或隐藏。

### 安全风险

shell、apply_patch、sandbox、MCP 都可能带来安全边界问题。默认不在后端执行 shell/apply_patch；sandbox/MCP 必须显式启用。

### 语义不一致风险

web_search 映射 Claude server tool 后，结果、引用、usage、事件类型都可能与 OpenAI 不完全一致，应在日志和文档中标注为替代实现。

## 后续里程碑

### M1：样本收集和文档完善

- 收集真实 Codex `/v1/responses` 请求样本。
- 重点确认 `namespace` raw shape。
- 收集 OpenAI 原生 Responses 输出样本。

### M2：namespace/function bridge 原型

- 实现 namespace 展开。
- 实现 Claude tool name 编码/解码。
- 不引入后端 executor。

### M3：shell/apply_patch bridge

- Claude tool_use 转 Responses `shell_call` / `apply_patch_call`。
- Responses call output 转 Claude `tool_result`。
- 后端不执行命令或补丁。

### M4：tool loop 框架

- 实现可选 loop runner。
- 加入最大轮数、超时、错误处理。
- 先用 mock executor 测试。

### M5：按需 executor

- file_search 本地 vector store。
- image_generation provider proxy。
- MCP local client。
- sandbox experimental executor。

## 测试策略

当前文档阶段：

- 检查文档存在。
- 检查标题、背景、架构、工具矩阵、风险、里程碑完整。
- 检查明确标注“蓝图，不代表功能已实现”。
- 不运行 Go 测试。

后续实现阶段：

- 每个 milestone 单独补单元测试。
- 所有 executor 需要错误、安全、超时、脱敏测试。
- 回归测试确保 DLC disabled 时现有行为不变。

## 当前结论

Responses Tools DLC 值得作为后续方向保留，但不应立即混入主链路。最稳妥的路线是先沉淀蓝图和真实样本，再从 namespace/function bridge 这种低风险能力开始逐步实现。
