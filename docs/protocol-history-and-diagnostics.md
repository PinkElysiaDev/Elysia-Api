# 协议历史、绑定与故障诊断

## Claude Code / Anthropic 请求

编码前，调用记录会保存候选模型、模型源、目标协议、上游修订、目标路径和 `cacheSynthesis` 开关。`systemStructure.before/after` 记录缓存合成前后的 system/developer 节点路径、类型、元数据字段名和缓存数量，最多 64 个节点；不记录提示词、字段值、资源 ID 或凭据。该诊断不依赖正文采集开关，可在调用详情展开，并随完整日志导出。

不合法的 system 消息容器元数据会报出 `/content/<index>/<field>`，错误的 `protocol` 信息标识实际目标协议；合法的缓存仍位于内容块上，TTL 与扩展字段保留。不能清空元数据或关闭校验来绕过此错误。

原远端故障尚未确认修复。部署包含诊断的版本后，应使用原 Claude Code 请求与原模型源配置重试，检查 `targetFormat`、`upstreamRevision`、`cacheSynthesis`、`systemStructure` 和 `conversionIssues`。保留具体触发字段的脱敏结构，补成回归用例，再针对其产生位置修复。当前模拟上游用例通过，不代表原远端问题已消失。

## Agent 模型能力

模型选择器与实际调用共用可用性检查，区分模型工具能力未声明、绑定禁止工具、协议映射缺失、修订验证过期、未绑定和模型停用。结果包含模型能力来源、最终绑定层级、协议及修订。已保存会话也显示当前不可用原因。

“验证并启用工具调用”会实际请求所选上游，要求返回一次 `elysia_capability_probe` 调用和随机 nonce；不会执行返回的工具。成功后，在同一事务中设置模型手动工具能力、模型级绑定及新兼容性证据。上游验证失败或配置在验证期间发生变化时不提交。此操作可能产生少量模型用量。

手动设置优先于目录刷新；供应商名称与目录声明不能代替当前上游验证。

## 历史生命周期

从协议设计器进入 `/protocols/history`（控制台使用 hash 路由）。列表仅包含更新替换的预置修订和用户删除的自定义协议。

- 预置更新在提交新修订的事务内归档旧版本。升级时回填数据库中仍保留的旧修订；物理删除的数据无法回填。
- 自定义协议删除会归档全部保存修订和当前草稿，撤销激活，从正常协议列表移除。
- 删除前显示引用和受影响模型。可以替换为已启用协议，或明确取消绑定。替换会先验证所有待更新绑定的能力、操作及入口兼容性；任一失败不会部分更新。
- 取消绑定保留源和模型。模型级“明确未绑定”阻止回退到源级协议，刷新和重启也不会自动恢复。可在模型源内打开“编辑模型”，单独验证并保存新的模型级协议绑定；模型源协议可以在源编辑窗口重新选择。
- 恢复要求新的非预置 ID；保留协议族、线格式、映射和数值精度，重新编译验证，通过才启用。未通过则保留可编辑草稿。原历史项保留，现有源不自动改绑。
- 彻底删除要求输入原协议 ID 确认，删除该历史定义及其专属验证数据。服务端再次检查活动绑定、持久任务及进行中的请求/会话；有引用返回 409。既有数据库备份与调用日志保留。

批量归档使用状态摘要进行乐观并发检查；冲突返回 409，需要刷新引用后重试。提交成功刷新运行时及路由缓存，已开始请求继续使用已取得的不可变修订。

Agent 编辑会话只记录协议 ID，没有修订哈希。因此仍引用原 ID 的编辑会话会阻止该协议历史的彻底删除；详情列出会话名称，需先在 Agent 中删除相应会话。

## 管理接口

以下路径以 `/api/admin` 为前缀，沿用管理员鉴权。

| 方法 / 路径 | 输入 / 结果 |
| --- | --- |
| `GET /protocols/history` | `{items}` 历史索引 |
| `GET /protocols/history/:archiveId` | 定义、报告、当前修订差异、删除阻塞引用 |
| `POST /protocols/history/:archiveId/restore` | `{id,name}`；返回 `protocolId,activated,issues`，成功验证另含报告及激活结果 |
| `DELETE /protocols/history/:archiveId` | 无正文；有依赖返回 409 |
| `GET /protocols/:id/references` | `{baseline,references,affectedModels}` |
| `POST /protocols/:id/archive` | `{baseline,mode:"block"\|"replace"\|"unbind",targetProtocolId?}` |
| `GET /protocols/bindings` | 当前显式绑定；`unbound:true` 表示明确未绑定 |
| `PUT /protocols/bindings` | 保存单个模型/源/组绑定；已绑定必须提供完整能力、传输和当前修订，服务端重新验证证据 |
| `GET /agent/models` | 每个模型的 Agent 可用性、能力来源、绑定层级、修订和失败原因 |
| `POST /agent/models/verify-tools` | `{sourceId,modelId}`；验证成功后原子启用工具能力 |

`archiveId` 是不透明索引，客户端须 URL 编码。旧绑定未携带 `unbound` 时仍按已绑定处理。

## 本地验证

在 `backend` 执行 `go test ./...`；在 `packages/webui` 执行 `npx tsc --noEmit` 和 `npx playwright test tests/protocol-history.spec.ts --project=chromium --project=webkit`。浏览器用例使用模拟管理接口；Go 网关用例使用真实协议编解码、SQLite 和模拟上游。两者均不请求远端生产模型。
