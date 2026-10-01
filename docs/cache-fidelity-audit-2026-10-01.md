# 四协议缓存语义审计与修复

审计日期：2026-10-01。历史基线为 v1.4.0（`029c18a581342e57cf44983b2adf9b4a5feab888`），修复前版本为 `8a0b23f35ebeede52418069805fb182a955bb923`。本文所称“修复后”指在该提交上的本次工作区改动，不代表已发布或已部署。

## 结论与证据边界

**预置配置不完整和共用转换逻辑丢失语义同时存在，统计链路还存在独立问题。仅调整四份预置 JSON 无法完整修复。** Chat → Anthropic 会丢失系统缓存边界和工具缓存标记；部分路径甚至遗漏 Chat 的系统提示词。Anthropic → Anthropic 则涉及系统块回放、多模态与工具结果保真、块顺序及用量口径。请求侧缺陷可能导致真实不命中；响应侧缺陷可能使上游已命中但客户端或面板看不到正确计数。

多数问题在 v1.4.0 已存在。历史版本曾有正常命中与这些发现并不矛盾：是否触发缺陷取决于实际入口、目标平台、内置或自定义转发路径、缓存断点位置和已存储的协议定义。**本地证据不足以认定线上“全部为 0”由一次版本升级单独导致，也不能从面板的 0 直接推断真实上游未命中。**

本次通用修复与协议 ID 无关。任意 ID 的预置副本、从零注册且改变字段路径的自定义协议均已验证。前提是选择合适的 shape，并在模板中声明需要输出的缓存字段及 usage 映射；模板主动省略的字段不会被自动补齐。未携带缓存意图的请求不会被自动开启缓存。

验证使用本地 HTTP 模拟上游、固定非零缓存计数及 SQLite 持久化记录。它证明请求保真和计数传递，**不证明任何真实服务商的缓存命中率**。未提供生产失败请求、账号或模型凭据，因此线上根因占比和修复后的实际命中率尚未验证。

## 链路与缺陷分层

请求先由四种入口解析为 Maheshvara；内置平台直接调用目标转换器，自定义协议先按 shape 整形，再由声明式模板构造最终请求。上游响应经原生或自定义 decoder 提取用量，流式事件交给客户端 renderer，同时合并进持久化统计。内置平台和 JSON 预置并非同一条完整执行路径。

| 层次 | 触发条件与修复前行为 | 影响 | 本次处理 |
| --- | --- | --- | --- |
| 预置及 shape 系统字段 | Chat system/developer 位于消息列表，Anthropic 预置从旧 `instructions` 取值，目标消息转换又剔除 system 消息 | 系统内容遗漏、稳定前缀和断点丢失，可能真实不命中 | 内置 Anthropic 与 Anthropic shape 共用系统构造器，新增 `anthropic_system` 原生字段 |
| 共用内容转换 | 通用内容解析将块标记仅留在 Raw，单文本块又被压成字符串；图片、文档、工具定义/调用/结果的提取和输出不完整 | 已声明断点到不了上游；只改顶层模板无效 | 提取到语义字段，适用目标 renderer 读取这些字段，带标记文本不降为字符串 |
| Anthropic 原生回放 | 原生 tool_use 与其他内容被拆分后重新排序；结构化 tool_result 被字符串化；文本 document 源被重建为 base64 | 缓存边界位置或原有前缀内容改变，也可能改变模型输入 | 保存并恢复原生块序，保留结构化结果和文档源；保留 `is_error` |
| 消息级标记 | Chat 兼容扩展中的 message.cache_control 原样进入 Anthropic 消息外壳 | 字段位置不符合目标块结构 | 移到该消息最后一个可缓存块；不为 thinking 块创建标记 |
| 字段目录和预置 | Chat/Responses 预置遗漏缓存键/保留期；Anthropic 遗漏顶层控制；Gemini 遗漏 cachedContent | 策略或显式资源引用丢失；并非所有遗漏都必然导致命中为 0 | 补齐目录和映射，按目标语义区分对象和资源字符串 |
| Gemini 系统内容 | 旧预置即使没有系统内容也生成空 systemInstruction | 显式缓存请求可能附带不应产生的系统配置 | 新增可省略的 `gemini_system`，不凭空构造空系统指令 |
| 统计解析 | 内置流式通用解析未完整读取嵌套缓存明细；自定义 Anthropic input 未加缓存读写；创建 TTL 分桶缺少正确求和兜底 | 已命中显示 0、创建量缺失、输入总量和命中比例失真 | 共用标准用量解析，创建与读取独立，统一输入口径 |
| 流式合并 | 起始帧、输出结束帧和独立 usage 尾帧字段不齐；单帧推算 total 覆盖完整统计 | 缓存计数和总量丢失/缩小；晚到计数未完整输出 | decoder 累计并发送快照，再合并计数后推算总量；Anthropic 结束帧输出晚到缓存计数 |
| 自定义别名 | 显式 cached 别名为 0 时仍回退默认 cache_read | 覆盖作者定义；可造成虚假的非零计数 | 尊重显式零值；显式 input 别名视为作者归一化后的总输入 |

真实不命中的高风险条件是：上游需要显式断点，且唯一适用断点在转换时丢失；或已缓存前缀在后续轮次中发生变化。丢失 OpenAI `prompt_cache_key` 不能单独证明必然为 0，因为键、自动前缀缓存和保留期并非同一机制。Gemini 资源引用也不能转换成 Anthropic 缓存控制对象。

实现入口参见 [cache_semantics.go](../backend/relay/cache_semantics.go)、[request_in](../backend/relay/maheshvara_request_in.go)、[request_out](../backend/relay/maheshvara_request_out.go)、[custom_protocol.go](../backend/relay/custom_protocol.go)、[字段目录](../backend/relay/custom_protocol_mapping.go)、[流式合并](../backend/relay/maheshvara_stream_renderer.go) 和 [统计持久化](../backend/server/usage_maheshvara.go)。

## 历史归属

| 观察 | v1.4.0 | 修复前 8a0b23f | 归属 |
| --- | --- | --- | --- |
| Chat → Anthropic 预置的系统内容和工具标记 | 对照用例失败 | 对照用例失败 | 历史已有，切换内置/预置路径可能暴露 |
| Anthropic → Anthropic 预置的 system 块标记 | 对照用例首先在 system 标记断言失败 | 此断言通过，随后在持久化输入/总量断言失败 | 中间提交 `daa98e8` 已部分修复原生 system 回放；统计缺陷仍在 |
| 多模态/工具结果、块顺序、缓存类型隔离、用量归一化 | 对应回归组失败 | 对应回归组失败 | 历史已有共用层缺陷 |
| 显式零 cached 别名被默认值覆盖 | 用例通过 | 用例失败 | 相对 v1.4.0 的可复现回归；不是实际零命中的证据 |
| 新增原生 shape 字段无法在旧版本注册 | 无该字段 | 无该字段 | 本次能力补齐，不能称为版本回归 |

历史版本在隔离源码快照中运行测试，没有改动当前检出的 Git 历史。v1.4.0 使用旧预置 ID，测试适配为 `openai-chat`、`anthropic-messages`、`openai-responses`、`gemini-generate`，以免将 ID 更名误判为缓存缺陷。历史用例的断言按首次失败停止，失败总数相同不意味着每个失败的根因完全相同。最后新增的前缀稳定性和统计边界用例没有全部回放到 v1.4.0，不将其宣称为已实测的历史回归。

## 测试证据

`TestCacheWireMatrix` 共 96 个场景：4 种入口 × 4 种目标 × 3 条执行路径（内置、预置、任意 ID 副本）× 2 种响应方式（流式、非流式）。每个请求包含工具调用及工具结果历史；模拟上游捕获最终 HTTP 请求，检查系统内容、历史、缓存字段，并核验客户端缓存计数和 SQLite usage。

| 版本 | 内置：通过 / 失败 | 预置：通过 / 失败 | 任意 ID 副本：通过 / 失败 |
| --- | --- | --- | --- |
| v1.4.0 | 21 / 11 | 9 / 23 | 9 / 23 |
| 8a0b23f，修复前 | 21 / 11 | 9 / 23 | 9 / 23 |
| 本次修复后 | 32 / 0 | 32 / 0 | 32 / 0 |

这些数值是**保真与统计测试通过数，不是缓存命中率**。四协议完整交叉覆盖不表示四种缓存机制可以互换，检查按目标能力执行。

其他证据位于 [relay 回归测试](../backend/relay/cache_fidelity_test.go)、[HTTP 矩阵及自定义协议测试](../backend/server/cache_wire_matrix_test.go) 和 [持久化升级测试](../backend/server/cache_presets_upgrade_test.go)：

- Chat system/developer 顺序、块级和消息级断点、工具标记、原生图片/文档/结构化结果、原生块序、`ttl: "1h"` 原样保留。
- 四入口 × 四预置的 16 组前缀测试：重复渲染字节一致，追加用户轮次及第二轮完整工具调用/结果后，不改变此前系统内容、工具和消息前缀。Gemini 会合并连续 user 消息，此时核验原有 parts 的精确前缀。
- 新 ID 复制协议，以及从零构造、将字段放入 `payload.*` 的协议：注册、预览和实际 HTTP 转发一致，流式与非流式 usage 别名均进入持久化统计。
- 无标记请求、主动省略 system/cache 的模板、Gemini 显式引用不注入空系统内容。
- 读取 70、创建 20、未缓存输入 10、输出 5 的标准 Anthropic 样例：统一输入 100、总量 105；Anthropic 客户端的线制 input 还原为 10。TTL 两桶只作创建总量缺失时的兜底，不重复计数。
- 仅创建、无缓存字段、嵌套明细、显式零别名、仅缓存尾帧、仅输入/输出尾帧、旧快照不被后续合并修改。补测仅有输入且输出缺失的情况，总量仍能推算，随后输出尾帧正确更新总量。
- 缓存创建量同时核验持久化和 Chat 兼容客户端的 `prompt_tokens_details.cached_creation_tokens`，覆盖流式与非流式。该断言在修复前失败，补齐既有类型字段的输出后通过。Responses/Gemini 的标准响应没有对应创建计数字段，本次不发明新线制字段；创建明细仍保存在内部统计中，Anthropic 客户端使用原生创建字段。
- 真实旧预置内容哈希升级、编辑过的预置和自定义副本保留、重复播种幂等、清空运行时注册表后从持久化重新加载。新装播种由已有预置测试覆盖；这里的“重启”验证为同等注册表恢复流程，没有宣称执行了操作系统进程重启。

完成的检查：后端 `go test ./...`、`go vet ./...`、WebUI `tsc --noEmit`。设计器从 `/custom-protocols/schema` 动态取得后端字段目录，因此本次无需新增前端字段硬编码；中英文协议文档和 CLI 字段帮助已同步。

本地历史对照原始结果保存在工作区 `.cache/cache-baseline-tests.jsonl`、`.cache/cache-before-tests.jsonl` 和 `.cache/cache-fixed-tests.jsonl`，最终全量结果在 `.cache/cache-final-tests.txt`。这些是本机审计产物，不是项目运行依赖。回归源码及本文随改动交付。

## 预置升级与已编辑定义的迁移

| 预置 | 修复前 → 修复后 | body 变更 |
| --- | --- | --- |
| `chat-completions-api` | v4 → v5 | 增加 prompt_cache_key、prompt_cache_retention、cache_control |
| `responses-api` | v4 → v5 | 增加 prompt_cache_key、prompt_cache_retention |
| `anthropic-api` | v3 → v4 | system 改用 anthropic_system；增加顶层 cache_control |
| `gemini-api` | v3 → v4 | 增加 cachedContent；systemInstruction 改用 gemini_system |

已登记上一版经 Go `json.Marshal(config)` 生成的实际内容哈希，并保存 [旧定义夹具](../backend/server/testdata/cache-presets-before)。启动时只替换哈希匹配的未修改预置，编辑过的定义和副本继续保留。更改版本号本身不能替代迁移；必须补齐映射内容。

以下是可合并进**现有 request.body** 的具体片段，保留原有端点、鉴权及其他字段。对自定义嵌套目标，应把叶子放入上游实际要求的位置。

Chat 与 Responses：

```json
{
  "prompt_cache_key": {"field":"prompt_cache_key","omitIfEmpty":true},
  "prompt_cache_retention": {"field":"prompt_cache_retention","omitIfEmpty":true}
}
```

Chat 兼容上游需要缓存控制扩展时，再加入 `"cache_control":{"field":"cache_control","omitIfEmpty":true}`。Responses 预置不输出 Anthropic 缓存控制对象。

Anthropic（替换原来的 system 叶子，不能保留 `mode: "string"`）：

```json
{
  "system": {"field":"anthropic_system","omitIfEmpty":true},
  "cache_control": {"field":"cache_control","omitIfEmpty":true}
}
```

若 `aliases.usage.cache_creation` 和 `cache_read` 仍仅包含旧预置的同名标准字段，可移除这两个冗余类别，使用内置解析及 TTL 桶兜底。上游确实使用自定义别名时保留。显式 `aliases.usage.input` 必须指向含缓存读写的总输入；若使用标准 Anthropic `input_tokens`，移除这个显式 input 别名，让标准解析进行归一化。旧的根级 `system → instructions` 模板保留原生块回放兼容；新增或嵌套映射应使用 `anthropic_system`。

Gemini（替换旧 systemInstruction 对象映射）：

```json
{
  "systemInstruction": {"field":"gemini_system","omitIfEmpty":true},
  "cachedContent": {"field":"cache_control","omitIfEmpty":true}
}
```

## 从零新增自定义协议的示例

下面是完整的模拟供应商协议定义，演示字段改名、嵌套、Anthropic shape 和用量别名。它要求上游接受所示 `/vendor/generate` 请求和 `text/metrics` 响应结构；不是官方 Anthropic 端点定义。生产接入可复制最新预置，或按实际供应商契约修改此例。

```json
{
  "id": "my-cache-protocol",
  "name": "自定义缓存协议示例",
  "type": "llm",
  "request": {
    "method": "POST",
    "path": "/vendor/generate",
    "shape": "anthropic",
    "body": {
      "payload": {
        "model": {"field":"model"},
        "stream": {"field":"stream"},
        "system": {"field":"anthropic_system","omitIfEmpty":true},
        "messages": {"field":"messages"},
        "tools": {"field":"tools","omitIfEmpty":true},
        "cache": {"field":"cache_control","omitIfEmpty":true}
      }
    }
  },
  "aliases": {
    "usage": {
      "input": ["in"],
      "output": ["out"],
      "cached": ["hit"],
      "cache_creation": ["write"]
    }
  },
  "response": {
    "textPath": "text",
    "usagePath": "metrics",
    "stream": {
      "frames": [
        {"event":"text","response":{"textPath":"text","usagePath":"metrics"}},
        {"event":"end","terminal":true,"response":{"usagePath":"metrics"}}
      ]
    }
  }
}
```

响应示例为 `{"text":"ok","metrics":{"in":100,"out":5,"hit":70,"write":20}}`，其中 `in` 已包含 `hit` 与 `write`。流式可在 text 帧发送输入/缓存计数，在 end 帧发送输出计数。shape 提供原生值；body 决定是否输出及输出位置，任意未知客户端字段不会因本次修复被整体透传。

## 线上验收方法与剩余边界

固定模型、源、账号和上游缓存作用域，准备达到该模型缓存门槛的稳定长前缀，在有效期内重复发送。对 Anthropic 区分首次创建与后续读取，对 Gemini 使用有效资源引用，对 Chat/Responses 保持前缀与缓存键/保留期一致。应同时保存脱敏后的最终上游请求、上游原始 usage、客户端 usage 和持久化统计，才能定位“0”发生在哪一段。

路由审阅发现模型亲和性按模型名称保留，而后续 API Key 展开仍可按 priority、round-robin 或 random 选择；不同上游账号或资源作用域可能分散缓存。相关代码为 [relay_plan.go](../backend/server/relay_plan.go) 和 [relay_retry.go](../backend/server/relay_retry.go)。这不是已证实的零命中根因，本次未改变路由策略。既有合成工具 ID 使用确定性生成，审计路径未发现本次转换引入时间戳或随机提示词。

本次保证的是：已支持、已正确声明映射的缓存语义到达适用上游，已返回的标准缓存计数到达客户端与统计系统。供应商是否支持某缓存字段、模型最低长度、缓存容量/淘汰、账号作用域、资源有效性、非法断点数量或客户端自带配置冲突，仍需真实请求确认。系统不会替作者补充未声明的缓存策略，也不会把一种协议的缓存资源转换成另一种协议的缓存对象。
