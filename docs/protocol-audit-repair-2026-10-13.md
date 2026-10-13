# 2026-10-13 四协议审计修复与验收

基线为 `3e99ec1`，原始审计目录为
`scripts/protocol-audit/results/2026-10-13T04-31-20.856Z-b6e1d87f`。
编译器语义版本从 `2.0.0-dev.66` 升为 `dev.67`。已有定义、草稿、
绑定及手动配置继续通过既有证据重验机制处理。本文记录本轮实现及实测，
不将模拟供应商或 SDK 回放作为供应商验签、真实插件验收的证据。

## 已实现

| 根因 | 修复与保留边界 |
| --- | --- |
| Responses safety_buffering | 显式校验布尔类型。同协议保留；跨协议仅 false 可规范化，带规则／策略／路径诊断；true 拒绝。 |
| output_text 重复正文 | 比较有序消息中的可见文本，完全相同才投影一次。冲突、非法类型、缺少输出结构拒绝；工具和思考项目不参与正文拼接。 |
| 伪装文本增量的心跳 | 仅消费完整匹配的 SSE-Keep-Alive sentinel、空 delta、零索引及合法可选序号；不创建项目、正文、用量或原生重放。额外载荷仍拒绝。 |
| 错误详情 | 新增 response/event 的 error_metadata 规则。空 param 规范化；兼容模式把目标无法结构化表达的字符串细分码／参数附于消息，严格模式拒绝结构损失。未知对象及扩展继续保护。 |
| 原始失败被次生错误遮蔽 | HTTP 与 SSE 保留供应商主因。异常事件名按 /type 拒绝，不模糊匹配为合法终止；错误后的非法尾帧记次级问题，不二次结束或重试生成。 |
| 流身份诊断 | 源身份变化在投影前检查，记录源协议、字段及帧序号；目标 after 映射后的格式校验仍保留。未知扩展优先报告原始路径。 |
| 审计工具 | 主失败与次级问题分开；实际客户端 HTTP、可观测上游 HTTP、逻辑状态分开；HTTP 200 SSE 限流仍失败。生成失败后停止正文／工具服从断言。官方与 Vercel SDK 独立调度。 |
| Go 验收超时 | 默认命令为 go test -json ./... -count=1 -timeout=45m；外层 60 分钟，报告包进度。外层超时记 incomplete，保留进程树终止机制。 |

规则按实际内置模块启用；复用模块的自定义定义获得修复，手写编码器不会
因为名称或 family 被套用这些动作。没有新增公开端点或数据库表。
调用记录的 JSON 详情增加 upstreamHttpStatus，保存最后一次尝试真实收到的
HTTP 状态；每次尝试重新初始化，不能把旧尝试的状态归给本地编码失败。
回归验证了上游 HTTP 200、客户端 HTTP 200、逻辑限流 429 同时成立的情况。

## 真实复测带来的补充

并发 1 的首轮使用临时实例及脱敏合成请求，共 10 对直连／网关请求
（20 次生成），输出上限 32768，禁用自动重试。直连使用调用记录中的
网关出站原文，记录其 SHA-256，避免比较两种不同工具选择结构。

- Gemini → Chat 的 JSON、SSE 工具选择两对均通过，未改动 Chat 的
  tool_choice.function.name 编码。
- 首轮发现一项 Responses 同协议调用收到渠道的 `***.failed`，
  内层 code 为 upstream_error；网关已拒绝 /type 并保留原因，相同请求
  直连成功。随后补齐 code-only 的上游主分类回归。
- 首轮还发现 safety_buffering:false 不只位于 response.created：
  完整直连流证实它也出现在 response.in_progress、
  response.output_text.delta、response.completed。实现按这四个
  明确位置识别，未扩大为任意事件字段。补充进度快照及终止帧重建，
  终止元数据随等待晚到 usage 的终止事件一起发送。
- 首轮结果为 8 对通过、2 对失败；失败证据保留在
  `results/dev67-paired-live/`，后续复测不会覆盖它。

## 本地验证

- 审计工具自测：146 通过、0 跳过；包含官方 SDK 失败仍执行 Vercel、
  HTTP 200 内层限流、次级诊断、字段缺失／变化和工程超时用例。
- 实际四预置的 16 方向 × JSON/SSE 文本矩阵通过，注入完整流中的
  safety_buffering、output_text、心跳，并断言规范化诊断实际落库。
- 从真实本地网关输出生成的固定版本 SDK 回放通过。
- Go protocol 包全量、关键 protocol/server race、Go vet 通过。
  race 使用忽略目录中的 llvm-mingw 20261006 工具链开启 CGO，
  没有修改用户全局工具链。
- 前端类型、lint、构建及真实后端 Playwright 10 项通过。
- Windows/Linux/macOS × amd64/arm64 六目标构建通过。
  交叉构建不表示 macOS 原生验收。

首轮 Go 全量测试通过，server 包耗时 1827.696 秒；在该轮编译之后补充的
流字段及 HTTP 诊断修复已有定向回归，最终完整审计仍会重跑全量检查。
第二轮成对实测捕获直连及网关都返回 HTTP 524，并补充纠正 HTTP 错误
误标为 decode_event / stream 的诊断，改为 decode_response / decode.response。
最终真实审计结果将在运行结束后补入，未完成项不计通过。
本轮没有触发实际 VS Code／Claude Code 插件操作。

## 待渠道核查的脱敏问题单

仅保存在项目中，不自动向渠道发送数据：

1. Responses 失败事件名应为 response.failed；原审计及本轮实测均出现
   `***.failed`。保留内层错误类型以区分并发限制和上游不可用。
2. 同一 Chat 流中的既有 id 不应改变；原报告已捕获 resp- 与 resp_
   不同 ID。网关仍拒绝，不以覆盖身份修复。
3. Responses response.created 应携带必需字段；原报告直连存在
   model/created_at 缺失后才出现。本轮不将后出现描述为已建立值改变。
4. 原工具请求为 Chat 的嵌套 function.name 结构，却收到缺少
   tool_choice.name 的报错。本轮成对请求成功，不能断言渠道内部原因已解决；
   应结合各次路由及模型核查。网关不改成 Responses 的扁平格式。

特殊媒体、服务端工具、完整 Responses 历史恢复及供应商真实签名接受范围，
不因文本与标准函数工具通过而扩大声明。
