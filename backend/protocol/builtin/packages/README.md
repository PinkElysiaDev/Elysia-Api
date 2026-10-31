# 默认协议源

每个子目录包含两份独立编写、共同发布的配置：

| 协议 | 外部协议定义 | 默认转换行为 |
|---|---|---|
| Chat Completions | [spec.json](openai-chat/spec.json) | [mapping.json](openai-chat/mapping.json) |
| Responses | [spec.json](openai-responses/spec.json) | [mapping.json](openai-responses/mapping.json) |
| Anthropic Messages | [spec.json](anthropic/spec.json) | [mapping.json](anthropic/mapping.json) |
| Gemini GenerateContent | [spec.json](google-generate-content/spec.json) | [mapping.json](google-generate-content/mapping.json) |

Spec 描述外部文档、端点和生命周期。Mapping 描述与大自在天的双向映射、模型策略和目标取舍。DSL 提供供应商无关的执行原语。

`Compiler.CompileSources` 将两份源编译为 schemaVersion 4 的完整包；DSLVersion 为 2，核心版本为 2。整包编译、修订、验证及激活不可分割，请求复用已编译快照。Spec 和 Mapping 的命名表达式各自独立。

**目前仍处于迁移阶段，生产预置尚未切换到本目录。** 文件存在或组件测试通过不代表全部调用、资源及性能门槛已经通过。当前进度见[实施记录](../../../../docs/protocol-three-part-implementation-2026-11-01.md)，新验收依据见[三部分契约](../../../../docs/protocol-three-part-contract.md)。

旧版本的差分记录用于历史调查，不再要求复制旧错误文字、多故障首错树或重复资源计费。有效功能样例继续使用；reasoning `content:null`、辅助 metadata 等确认过的客户端兼容需要具名声明与实际调用验证。不得静默丢失正文、工具关联、usage 或终态。
