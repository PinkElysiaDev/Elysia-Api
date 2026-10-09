# dev.28 本地修复与验收记录

接续 [dev.27 记录](protocol-audit-dev27-local-2026-10-10.md)。用户要求完成下一轮修改后先不要真实测试，等待其调整 API。本轮没有请求真实渠道，也没有修改 `scripts/protocol-audit/config.local.json`。测试只使用合成内容、回环地址和隔离数据库；提交均留在本地。

## 修改与证据

| 提交 | 修复 |
|---|---|
| `f366462` | 为 Responses 有序思考正文增加独立语义字段，与摘要分别保存。JSON 重建、交错 SSE、终局快照、深拷贝、比较和续传摘要使用同一表示。编译器提升至 `2.0.0-dev.28`。 |
| `535770a` | 修复 Chat 独立编码分支可能把多段思考写成空值的缺口。Chat、Anthropic、Gemini 的请求、响应和事件编码均拒绝尚未投影的复杂思考结构。 |
| `40bf837` | Responses `content_filters:null` 通过既有 `response_metadata` 规则规范化，记录信息级诊断。修复流式目标在规则关闭后仍可能忽略外族元数据的缺口。 |
| `d812c02`、`03d1b46` | 上游错误无法完整转换时保留已解码的消息字符串、HTTP 状态及类型化转换诊断；不复制整份未知错误对象，不改变 HTTP 重试分类。空响应继续返回校验错误。 |
| `2484ee1` | 固定版本 OpenAI / Vercel OpenAI SDK 完整消费混合摘要与多段正文的 JSON/SSE；另核对 OpenAI 最终对象中的各段内容。 |
| `a69b220` | 新增正文段参与作用域与来源标注、绑定能力观察、用户节点规则、元数据规则、缓存计数、输入长度估算及 Agent 历史摘要提取。 |

### 思考结构

Responses 的 `summary` 与 `content` 分别对应摘要节点和有序正文节点。不能把摘要当成正文，也不能默认把多段内容拼成一个字符串。只有无额外状态的单个普通正文段才可以消除冗余容器；此等价形式的续传摘要保持一致。

本地回归覆盖：多个正文段、摘要/正文交错、段索引、已结束内容被改写或删除、孤立增量、终局一致性、克隆隔离和正文修改导致认证摘要变化。单段正文转 Chat、Anthropic、Gemini 的已实现路径继续通过。

多段或摘要与正文混合结构尚无其他协议的等价投影。没有以静默串接、删除摘要、放宽比较或重新标为普通文本的方式宣称支持。带厂商附加字段的正文可保留在原生 JSON 重建中，流式重建遇到尚无映射的段扩展仍拒绝。

### 过滤状态与错误信息

`content_filters` 只识别已观察到的 `null` 哨兵。原生输出保留其存在性；跨协议规范化记录 `conversion_normalized`、规则 ID、策略哈希和路径，兼容/严格模式均可通过。非 null 的对象、数组、标量继续作为未知扩展保护，不能据此宣称支持供应商过滤状态。禁用规则时 JSON 和 SSE 都拒绝未经处理的字段。

错误回归使用模拟 HTTP 400 的 `Thinking mode does not support this tool_choice`，经公开 Anthropic 入口和 `/gateway/` 入口验证消息与 `/error/code` 诊断同时可见。每次调用只请求一次模拟上游。HTTP 503 的重试分类仍基于真实 HTTP 失败，HTTP 200 后本地转换失败不因此变成可重试。附加上下文仅取已解码的消息字符串，不序列化 `details` 等未知对象；错误转换仍标记失败。

## 本地验证

最终代码提交为 `03d1b46`。本轮运行结果：

- `go test ./... -count=1` 全量通过，server 包 406.872 秒。补充遍历修复前的全量也通过，保留两份日志，不混作同一轮。
- 最终 `go vet ./...` 通过；结构化思考、字段规范化、续传和错误路径的定向 race 通过；新增遍历与估算修复另行执行 race 通过。事件回放 fuzz 15 秒通过。
- 审计自测 **89/89**，无跳过；其中固定 SDK 用例 **20/20**。
- Chromium/WebKit **20/20**；协议编辑、预览、验证、激活和回滚相关用例连接临时真实后端，其他历史页面用例使用模拟 API。前端 lint 通过。

日志位于被忽略的 `.tmp-dev/`：`audit-dev28-go-final.log`、`audit-dev28-final-vet.log`、`audit-dev28-race.log`、`audit-dev28-final-race.log`、`audit-dev28-fuzz.log`、`audit-dev28-selftests.log`、`audit-dev28-sdk.log`、`audit-dev28-browser.log`、`audit-dev28-lint.log`。

交付构建使用 `node scripts/build-standalone.mjs`，包含前端类型检查、生产构建和六平台编译，日志为 `.tmp-dev/audit-dev28-delivery-builds.log`。产物位于 `dist/standalone/`。Windows 产物的隔离新库启动、十次重启及实际引擎版本检查另记在 `.tmp-dev/audit-dev28-delivery-smoke.json`；此新库检查不替代原环境升级验收。

## 未关闭项

1. 消息 `phase` 的 JSON、历史和流式归属仍需实现，尤其是最终消息事件才给出值的情况。不能在内容段已经结束后把它附到不相关文本，也不能从往返比较中删除它来制造通过。
2. Responses `access_programs`、`tool_usage`、`usage.attribution` 等状态与计量字段仍待逐项映射。未知扩展边界保持有效。
3. 复杂思考结构的跨协议投影、摘要历史及供应商真实签名接受情况未完成；本地认证测试使用合成签名。
4. 本轮错误补充上下文依赖成功解码出供应商消息。外族错误包导致解码本身失败的情况仍须单独建立协议证据，不能声称所有供应商错误格式均已修复。
5. 真正的 16 方向工具多轮、最终真实渠道矩阵及 Claude Code 插件验收尚未关闭。用户未使用 Cherry Studio，不将其作为用户验收前提；VS Code 插件仍待更新问题解决。

真实测试继续暂停。后续恢复须依据用户新的通知，不能把旧目标文本或已有临时密钥当作恢复指令。
