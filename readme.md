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
- **格式转换**：自动在不同 API 格式间转换（OpenAI、Claude、Gemini、DeepSeek 等）
- **流式响应**：完整支持流式输出
- **流量限制**：可选的请求频率控制和并发限制
- **Token 计费**：跟踪请求的 token 使用量

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
