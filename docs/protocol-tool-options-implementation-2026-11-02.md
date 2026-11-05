# 工具约束与输入流提示实施记录（dev.110）

本批完成剩余计划批次 3 的工具选项部分；工具结果处置仍属下一子批。仅本地提交，四预置生产加载器尚未切换。包 4、DSL 2、核心 2、策略 2 不变。

## 实现与语义

DSL 增加请求 tools 的受限选择及三个选项的赋值，复用已有读取、计量、诊断和调用者隔离。Go 不按供应商选择 strict 或流式提示的转换策略。外部类型由 Spec 检查，核心输入中的已知选项在投影前检查；目标规则不重复同一检查。

四份 Mapping 完整保留 strict 原表示，缺失或 null 时增加 strictDefault：Responses 为 automatic，其他三个协议为 disabled。这是明确的新核心表示；迁移对照与往返样例据此补充预期，不忽略整个 Options。

稳定规则 tool-options（请求顺序 145）处理目标默认差异：向 Responses 转换 disabled 时显式发 strict:false；向 Chat／Anthropic 转换 automatic 时兼容告警、严格拒绝。Gemini 的 false 或 disabled 可无损省略，true 或 automatic 必须兼容降级或严格拒绝。规则不改工具 schema，也不声称目标执行了已省略的约束。

eager_input_streaming 只在流式 Anthropic 目标保留；非流式或 null 可规范化省略，其他流式目标的省略属于声明的损失。禁用规则后，编码器拒绝未解决意图，不重新降级。未知普通 options 继续拒绝。

本地 Agent 没有供应商 strict 缺省，Responses Mapping 显式声明其 disabled 意图。已有 Agent JSON／SSE 两轮检查增加实际出站 strict:false 断言，保持原调用次数、usage 和历史断言。

Responses 提取实际重复的 Mapping 表达式和 Spec 约束表达式，未提高 100,000 token 或深度限制。四包 token 为 Chat 95,326、Responses 99,617、Anthropic 88,558、Gemini 69,246。

## 验证

原始命令、设置和失败过程保存在忽略目录 `.tmp-dev/protocol-three-part-2026-10-31/checks/`。实施中的表达式深度、omit 合并及样例缺省表示问题已修；没有放宽限制或整体忽略工具字段。一次 race 检查发现测试策略浅拷贝污染后续用例，修复测试切片所有权后复核通过，未报告生产数据竞争。

| 检查记录 | 结果 |
|---|---|
| phase-nine-options-generate | 生成成功，无生成文件差异 |
| phase-nine-tools-evidence | 四包正式离线证据通过 |
| phase-nine-tools-final-confirm | protocol、builtin 全量通过，约 46.5 秒 |
| phase-nine-tools-agent-live | Agent JSON／SSE 工具两轮及实际 strict:false 通过 |
| phase-nine-tools-race-final | 受限写权限、strict／缺省／流提示与禁用规则相关 race 通过 |
| phase-nine-tools-vet | 全量 vet 通过 |
| phase-nine-tools-staticcheck | v0.8.2，24 条存量诊断，与 dev.109 逐行相同，无新增；退出 1 |

本子批没有重复运行 server 全量；最近全项目通过基线是 dev.109，本批真实 Agent 集成已定向验证。最终全项目、审计、前端及六目标仍按总计划执行。

## 尚未完成

本批没有正式配对性能数据，不能据功能检查耗时宣称性能无损。工具结果对象／文本／错误／媒体处置、一般布局、缓存与错误配置化、完整入口及资源闭合、性能归因、生产切换和旧链退役仍未完成。跨协议组合中的缺省等价表示仍需在其真实入口验证，不能将单包往返当作全部转换闭合。
