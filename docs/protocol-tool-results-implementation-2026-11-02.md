# 工具结果处置实施记录（dev.111）

本批继续 dev.110 的工具选项，接通工具结果的目标处置。包 4、DSL 2、核心 2、策略 2 不变，生产预置仍未切换。仅本地提交，不调用真实供应商。

## 三部分的职责

DSL 的 tool_result 选择复用已有节点遍历、核心读取、资源预算、调用者隔离和诊断。写权限仅限 payload、status、显式消费 children，以及插入文本前缀 childrenPrefix；不能修改名字、callId、资源、来源、Native 或整个节点。children 的非 omit 写入在编译时拒绝。

四份 Mapping 声明稳定规则 tool-results，请求顺序 150。对象序列化、文本块合并和文本包装属于明确的兼容降级，严格模式拒绝；禁用规则后编码器不重复这些取舍。无损的目标包络映射仍属于双向编码，不需要有损规则授权。

- Chat／Responses 将对象结果编码为 JSON 文本；true 错误标记使用既有 `[Tool error] `，false 规范化。Responses 保留内容块和媒体，错误结果通过前置文本块表达，不统一字符串化。
- Anthropic 直接保留 is_error 与可表达的内容块；对象结果转文本需声明的损失授权。
- Gemini 保留对象。普通文本通过规则包装到 result，不再尝试 parse_json。错误状态通过 error 包络表达，带保留键的成功对象进入 output。来源的单 error／output 包络映射到独立状态及载荷，普通对象保持原形。
- Chat 的工具结果只支持文本；Gemini 本批对象结果不消费媒体。纯文本块合并前要求没有缓存、资源、未知属性或其他附加语义。不能将媒体／未知语义拼接后丢弃。

Chat 和 Responses 的结构化结果解码补齐 children，使媒体真正进入核心，不仅保留在原始 payload 中。核心结果校验接受 payload 或非空 children，仍拒绝两者皆无，并保留先有调用、重复结果和权限检查。

## 真实入口暴露的缺口

组件测试通过后，真实网关组合验证仍拒绝工具历史。本批保留其检查，修复了三项实际缺口：

1. Chat 对仅含工具结果、无附加语义的用户／工具消息包络解开为工具消息；混合正文、资源或未知属性不借此删除。更一般的混合历史布局留给后续批次。
2. 开发验收的工具语义比较将已声明 disabled 与显式 strict:false 规范为同义；automatic、true 和未知选项仍参与比较。生产请求不执行这项比较，不整体忽略 Options。
3. Anthropic 目标不能表达工具项自身独立 ID；response-shape 对 tool_call 明确诊断并处置该 ID，保留 callId。完成状态仍须与外层终态一致才可规范化，严格模式拒绝身份损失。

集成夹具同时显式启用模型与分组工具权限。此前检查因夹具未授权工具而拒绝，不能通过关闭路由权限检查解决。

## 验证

检查原文、设置及实施中的失败过程保存在忽略目录 `.tmp-dev/protocol-three-part-2026-10-31/checks/`。未提高包节点、表达式深度或业务等待限额。Responses 通过提取真实重复的读取谓词和媒体表达式控制配置大小；token 为 Chat 96,683、Responses 99,843、Anthropic 88,899、Gemini 70,318。

已通过的检查：

- phase-nine-results-core-confirm：protocol 和 builtin 全量，含四包正式激活证据。
- phase-nine-results-directed-confirm：受限写权限、目标处置、严格模式、规则禁用、JSON 文本、错误、媒体和纯文本块。
- phase-nine-results-live-fifth：Anthropic→Chat／Responses／Gemini 的真实本地 JSON/SSE 请求，实际出站错误表示与 callId、上游一次调用、完整正文消费、usage 和终态。

| 最终检查 | 结果 |
|---|---|
| phase-nine-results-go-all | Go 全量通过，总计约 505.1 秒；server 414.4 秒、protocol 7.1 秒、builtin 43.8 秒 |
| phase-nine-results-race | 核心、builtin、真实网关工具结果及 Agent JSON/SSE 两轮相关 race 通过，约 310.2 秒；无数据竞争报告 |
| phase-nine-results-vet | Go 全量 vet 通过 |
| phase-nine-results-staticcheck | 固定 v0.8.2，24 条存量诊断，与 dev.110 逐行相同，无新增；退出 1 |

平台为 Windows amd64、Go 1.27.2。全量使用 GOMAXPROCS=2、GOGC=50、GOMEMLIMIT=1GiB、PGO 关闭；race 使用 4／100／4GiB，构建并发均为 1。没有前端修改；最终审计、前端、页面与六目标仍按总计划执行，不将本批检查冒充最终交付。

## 范围与性能

本批不宣称四协议所有能力闭合。一般混合历史布局、多根输出、多段 reasoning、复杂媒体结果、缓存与错误配置化、入口和资源持有仍有后续工作；当前成功集成不替代这些能力的验收。Gemini 本批明确覆盖普通对象及单 error／output 包络，混合包络的完整语义仍需后续检查。

没有正式性能配对数据。新增目标规则沿用现有同步遍历；不新增逐帧 goroutine、换引擎兜底或扩大缓存。不能以源码结构或功能套件耗时宣称性能无退步。版本 B、三版本性能、profile 与最终切换仍未执行。
