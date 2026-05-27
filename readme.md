# Elysia-API for Koishi

> 受 [New-API](https://github.com/Calcium-Ion/new-API) 启发，为 Koishi 打造的 AI 模型网关和编排解决方案。

[![npm](https://img.shields.io/npm/v/koishi-plugin-elysia-api-orchestrator)](https://www.npmjs.com/package/koishi-plugin-elysia-api-orchestrator)
[![npm](https://img.shields.io/npm/v/koishi-plugin-elysia-api-aggregator)](https://www.npmjs.com/package/koishi-plugin-elysia-api-aggregator)

## 简介

Elysia-API 是为 Koishi 设计的模型网关和编排插件，由两个部分组成：

- **模型聚合插件** (`koishi-plugin-elysia-api-aggregator`)：自动获取和管理来自各种来源的可用 AI 模型
- **模型编排插件** (`koishi-plugin-elysia-api-orchestrator`)：管理 API 网关，支持自定义模型组和负载均衡策略

## 功能特性

### 模型聚合插件
- **自动获取**：从配置的 API 源自动获取可用模型（支持 OpenAI 兼容、Claude、Gemini 等）
- **手动配置**：支持手动添加自定义模型
- **模型验证**：验证模型可用性和能力
- **类型支持**：LLM、Embedding 和 Reranker 模型

### 模型编排插件
- **模型组管理**：将模型组织成自定义组
- **负载均衡**：支持轮询、顺序和随机策略
- **API 网关**：内置 Go 后端实现高性能请求转发
- **格式转换**：自动在不同 API 格式间转换（OpenAI Chat Completions、OpenAI Responses、Claude Messages、Gemini GenerateContent 等）
- **Responses API**：支持 `/v1/responses` 端点，可原生转发或通过 Canonical 中间层转换到 Chat / Claude / Gemini 上游
- **流式响应**：完整支持流式输出，并支持 Chat / Claude / Gemini 流转换为 Responses SSE 事件
- **流量限制**：可选的请求频率控制和并发限制
- **Token 计费**：跟踪请求的 token 使用量，包含缓存命中、推理 token、多模态 token 和内置工具调用统计

## 安装

```bash
# 安装两个插件
koishi add elysia-api-aggregator
koishi add elysia-api-orchestrator
```

或通过 npm 安装：

```bash
npm install koishi-plugin-elysia-api-aggregator koishi-plugin-elysia-api-orchestrator
```

## 快速开始

1. **配置聚合插件**，添加你的 API 源
2. **配置编排插件**，创建模型组
3. **通过 OpenAI 兼容的 API 端点使用模型**

## 支持的平台

- OpenAI / OpenAI 兼容 API
- Anthropic Claude
- Google Gemini
- DeepSeek
- SiliconFlow
- 以及更多...

## API 格式与 Responses 支持

后端以 Canonical Request / Response / Usage 作为中间表示，支持在以下格式之间转换：

| 输入 / 输出格式 | 非流式 | 流式 | 工具调用 | Usage 提取 |
| --- | --- | --- | --- | --- |
| OpenAI Chat Completions (`/v1/chat/completions`) | ✅ | ✅ | ✅ function tools | ✅ prompt / completion / cached / reasoning |
| OpenAI Responses (`/v1/responses`) | ✅ | ✅ | ✅ function 与部分 builtin tools | ✅ input / output / cached / reasoning |
| Claude Messages (`/v1/messages`) | ✅ | ✅ 转 Responses SSE | ✅ tool_use / tool_result | ✅ input / output / cache read / cache creation |
| Gemini GenerateContent | ✅ | ✅ 转 Responses SSE | ✅ functionCall / functionResponse | ✅ prompt / candidates / thoughts / cached / modality details |

### `/v1/responses`

`/v1/responses` 可根据模型端点能力和配置选择处理方式：

- **native**：上游原生支持 Responses 时直接请求上游 `/responses`。
- **transform**：上游不支持 Responses 时，将请求转换为目标上游格式，再将响应转换回 Responses 格式。
- **auto**：根据模型 `endpoints.responses` / `endpoints.chatCompletions` / `endpoints.claudeMessages` / `endpoints.geminiGenerateContent` 自动选择。

配置示例：

```json
{
  "responses": {
    "enabled": true,
    "upstreamMode": "auto",
    "transformUnsupportedBehavior": "error",
    "passThroughUnknownFields": true
  },
  "usage": {
    "estimateWhenMissing": true,
    "charsPerToken": 4,
    "defaultOutputTokenEstimate": 1024,
    "imageInputTokenEstimate": 300,
    "fileInputTokenEstimatePerKB": 128
  },
  "modelGroups": [
    {
      "id": "default",
      "name": "default",
      "models": [
        {
          "id": "gpt",
          "name": "gpt-4.1",
          "platform": "openai",
          "baseUrl": "https://api.openai.com/v1",
          "endpoints": {
            "chatCompletions": true,
            "responses": true
          }
        }
      ]
    }
  ]
}
```

### Usage 统计字段

用量记录会尽量保留各平台返回的原始 token 语义，并统一输出到 dashboard / logs：

- `inputTokens` / `outputTokens` / `totalTokens`
- `cacheHitTokens`
- `usageDetail.cachedInputTokens`
- `usageDetail.cacheCreationInputTokens`
- `usageDetail.reasoningTokens`
- `usageDetail.textInputTokens` / `textOutputTokens`
- `usageDetail.imageInputTokens` / `imageOutputTokens`
- `usageDetail.audioInputTokens` / `audioOutputTokens`
- `usageDetail.toolUseTokens`
- `builtinToolUsage.webSearchCalls`
- `builtinToolUsage.fileSearchCalls`
- `builtinToolUsage.imageGenerationCalls`
- `usageSource`
- `estimated` / `estimatedTokens`

当上游没有返回 usage 且开启 `usage.estimateWhenMissing` 时，后端会基于 Canonical 请求内容进行估算。估算值会标记为 `estimated=true`，并单独计入 `estimatedTokens`，不会污染 provider 返回的真实 token 总量。

## CLI 命令

```bash
# 模型管理
elysia-api.models.reload    # 重新加载模型列表
elysia-api.models.list      # 列出所有可用模型

# 后端管理
elysia-api.backend.status   # 查看后端状态
elysia-api.backend.reload   # 重载后端配置
elysia-api.backend.restart  # 重启后端
```

## 架构

```
┌─────────────────┐     ┌─────────────────┐
│   Aggregator    │────▶│   Orchestrator  │
│  (模型来源)     │     │   (API 网关)    │
└─────────────────┘     └────────┬────────┘
                                 │
                         ┌───────▼───────────┐
                         │  Go 后端         │
                         │  (高性能)        │
                         └───────┬───────────┘
                                 │
                    ┌────────────┼────────────┐
                    │            │            │
                ▼─────▼      ▼───▼       ▼───▼
              OpenAI       Claude      Gemini  ...
```

## 许可证

Apache License 2.0

## 致谢

借鉴自 [New-API](https://github.com/Calcium-Ion/new-API) 项目
