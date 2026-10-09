# dev.27 本地修复及真实测试暂停记录

本记录接续 [上一轮审计](protocol-live-audit-2026-10-10.md)。用户最新要求在下一轮修改后暂不执行真实测试，以便调整 API。因此停止了仍在运行的真实审计，本轮后续检查只使用合成输入、回环地址和临时数据库。没有修改 `config.local.json`，没有推送提交。

## 证据与执行状态

先前已经执行的更新渠道直连基线位于被忽略的 `scripts/protocol-audit/results/api-updated-baseline-20261010/`。12 个子项中 11 通过、1 失败；DeepSeek 的 Anthropic 模型发现返回 404，生成接口可用。该进程在发布 Markdown 报告时遇到 Windows `EPERM`，JSON 报告仍标记 `running`，不能写成一次成功完成的审计。

`api-updated-round1-20261010/` 使用 `bc80076` 构建的网关，尚不包含缓存计数和本次可见思考修复。按用户暂停要求终止后，其报告也保持未完成状态。已经保存的单项记录可以用于定位，不属于最终代码的真实验收证据，也不改写为成功或已关闭。

在已有失败记录中确认：

- DeepSeek Chat 返回 `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`，原实现将它们当成未知字段。
- Anthropic 可见思考转 Responses 时，编码器原先只支持摘要，错误地拒绝了可以用 `reasoning.content` 表达的正文。
- Responses 的 `content_filters:null`、消息 `phase` 等仍存在识别和归属缺口。
- 渠道的思考模式拒绝强制工具选择，这是上游约束；跨协议错误编码还可能遮挡该原因。没有通过关闭思考或改用其他工具策略来隐藏这个失败。

## 分批修改

| 提交 | 修改及边界 |
|---|---|
| `bc80076` | 报告发布仅对 Windows 临时文件锁执行有界重试；不重试 HTTP，不重新生成模型结果。 |
| `2557422` | 映射 DeepSeek 缓存别名，检查别名一致性和算术关系；保留原生字段形态和计数来源。引擎语义提升至 `2.0.0-dev.27`。 |
| `aa1dc47` | Chat、Anthropic、Gemini 的可见思考输出为 Responses `reasoning.content`，生成 `response.reasoning_text.*` 事件；补正文类型、事件关联和结束顺序校验。 |
| `3e9d67d` | 缓存未命中计数晚到时，先合并同一请求的原始用量快照；支持嵌套缓存命中别名参与精确推导，重复快照不求和。 |
| `42369d0` | 固定版本 OpenAI 与 Vercel OpenAI SDK 完整消费可见思考 JSON/SSE，并检查隐藏错误事件。 |
| `c02e2d2` | 原生回放夹具补全思考项及内容块开始/完成事件，保留原始保真断言并检查 trailer；另断言孤立思考增量必须拒绝。 |

可见思考回归覆盖真实预置、兼容与严格模式、最终线格式、增量/快照、事件完成顺序、下一轮请求历史和认证恢复摘要。签名恢复测试使用合成签名，只验证本地认证、原文关联及篡改拒绝，不证明供应商接受签名。

SDK 用例验证完整消费、正文及终止结果，并拒绝隐藏 error 事件；它不证明各 SDK 或客户端界面都会展示 `reasoning_text` 正文。

组合验证单独验证可见思考输出样例。整个 reasoning 绑定仍可能因原生 Responses 摘要历史没有 Anthropic 等价映射而被拒绝；没有把这个负向结果改成全面支持的证据。

## 本地检查

当前本地源码为 `c02e2d2`，凭据和原始调用结果均未暂存。

- `go test ./... -count=1` 全量重跑通过（server 399.771 秒）。定向 race、`go vet ./...`、事件回放 fuzz 15 秒通过。
- 审计自测 **87/87**，无跳过；包括新增的两个 SDK 可见思考用例。
- Chromium / WebKit **20/20** 通过。协议编辑、预览、验证、启用、回滚及 Agent 草稿用例连接真实临时后端；其他历史页面用例使用模拟 API。
- 交付构建命令为 `node scripts/build-standalone.mjs`，包含前端类型检查、生产构建及 Windows/Linux/macOS 的 amd64、arm64 六目标编译。产物在 `dist/standalone/`，构建执行日志为 `.tmp-dev/audit-dev27-delivery-builds.log`。

本地日志保留在被忽略的 `.tmp-dev/`：`audit-current-go-tests.log`、`audit-current-go-confirmed.log`、`audit-native-reasoning-tests.log`、`audit-current-vet.log`、`audit-current-reasoning-race.log`、`audit-current-selftests.log`、`audit-current-browser-confirmed.log`。首次 Go 全量失败来自缺少思考开始事件的旧原生回放夹具；新校验拒绝了孤立增量。补全合法事件序列后，网关专项通过，再执行全量检查；没有恢复错误透传，也没有将旧失败日志改写为成功。首次浏览器启动脚本受 PowerShell 对 stderr 的处理影响返回了误导性的退出码；重跑使用文件描述符捕获日志，实际 Node 退出码为 0，20 项均通过。

## 未完成的能力与下一轮顺序

1. 本轮新映射支持单个普通 `reasoning_text` 正文项。多段正文、摘要与正文并存、正文附加厂商扩展的 JSON 原生重建仍保留完整形状，但跨协议仍明确拒绝；复杂 SSE 尚未形成完整映射。后续需要显式表示各类有序内容，并同步处理事件、回放、恢复和比较，不能直接串接正文、删除摘要或放宽往返比较。
2. Responses 的 `content_filters`、`phase`、`access_programs`、`tool_usage`、`usage.attribution` 需分别核对类型、节点归属、历史或计量语义。不能统一视为可删除元数据。
3. 供应商强制工具选择限制应保留为直连证据；错误转换应保留可读的上游原因及转换诊断。适用模型的自动工具选择只能作为另一个明确标记的测试场景。
4. 缓存未命中计数晚到已有回归，但仅提供未命中计数且始终没有可确定总输入/缓存拆分的场景仍没有完整映射；不能假定未知缓存命中为零。
5. 用户调整 API 期间不开始下一轮真实审计。配置就绪后需重新建立直连基线、重测受影响方向，再执行六组完整审计。实际 VS Code 插件验收仍按用户要求等待更新问题解决；用户没有使用 Cherry Studio，不将其验收作为用户前提。

状态：本轮修复按根因提交；本地证据与真实验收分开记录。四协议总体功能及原渠道故障尚未全部关闭。
