# 补充源码审读与后续语义修复

审读截止代码为 `64c3f9a`。本表补充冻结的 976 项台账；33 个发布后新增文件阅读了完整现行内容，`.gitignore` 核对了 `fa140fb → 64c3f9a` 全部增删行。测试名称表示对应责任，不表示每一行已被测试执行，也不表示整个调用链已穷尽。

| 路径 | 人工审读重点 | 对应验证与边界 |
| --- | --- | --- |
| `.github/workflows/protocol.yml` | 只读仓库权限、Go/Node 版本来源、race 证据、定时 fuzz、独立后端与浏览器任务 | 本轮运行本地命令，不声称 GitHub Actions 已执行 |
| `.gitignore` | 恢复 lockfile 跟踪；运行配置、源码快照及生成登录资产边界 | 源码快照清单及边界自测 |
| `backend/protocol/authoring.go` | 预览与运行共享转换；源帧需序列输入及固定选择身份；历史修订有界保留；数组作为整体差异 | 预览/组合验证及启动身份测试；独立编码器验证仍保留最终 wire 检查 |
| `backend/protocol/builtin/json.go` | JSON 容器保留缺失；字符串/null 分流；未知字段合并冲突；诊断路径不含正文 | 未知扩展与精度专项；不能把显式 null 当作缺失删除 |
| `backend/protocol/builtin/cache.go` | TTL 所有者与原值覆盖、tools→content→request 遍历、nil 诊断接收器 | 缓存 TTL 编辑/顺序/null/冲突测试；警告不是供应商验收 |
| `backend/protocol/builtin/tool_choice.go` | Gemini 单函数 ANY、并行开关、各目标函数形状、未知模式拒绝 | 单函数工具选择及工具往返；不按渠道错误文案改 Chat 形状 |
| `backend/protocol/builtin/responses_tool_usage.go` | 只提取声明过的计数叶；残留未知嵌套字段保留；精确整数与算术检查 | Responses hosted usage 与整数精度回归 |
| `backend/protocol/builtin/stream_message.go` | 消息元数据、子项目顺序、完成后正文摘要与元数据不变 | 消息边界回归；新发现的等价转义角色误拒见下文 |
| `backend/protocol/builtin/stream_visible_reasoning.go` | 推理项目/文本/内容部分分别终止；追加而不覆盖；有界缓存 | 可见推理合法/非法序列及续传摘要回归 |
| `backend/protocol/diagnostic.go` | 完整诊断去重保留顺序；nil sink；取消 cause；证据当前性与通过状态分离 | 诊断去重与取消原因测试；sink 仍按单调用归属使用 |
| `backend/protocol/evaluation_state.go` | 模块状态按编译定义和方向隔离；原生观察不产生额外终止；初始化限额 | 流生命周期、原生帧与组合验证；状态不跨并发调用共享 |
| `backend/protocol/event_replay.go` | 父子身份、工具关联、终止后仅 usage、EOF 必须有终止 | 事件重放/长流回归；动态 Role/ID 的等价转义误拒已单独复现，见下文 |
| `backend/protocol/typed_limits.go` | 固定类型计划只读；字节/节点/深度一致；不支持的类型回退原编码器；字符串转义计量 | TypedMeter 对照与 fuzz 种子；未运行长时新 fuzz campaign |
| `backend/protocol/value_container.go` | 已验证 JSON 上的 span 扫描；子值复制避免保留完整大帧；null/空数组差异 | JSON 容器及资源上限回归；不能将这些内部扫描器用于未验证输入 |
| `backend/relay/stream_idle.go` | 只计网络读取空闲；定时取消请求；停止定时器时等待回调，保留错误 cause | headers/usage tail 超时和消费者耗时排除测试；等待回调是同步屏障，保留 |
| `backend/protocol/session_config.go` | 帧/队列限额、关闭/ping/空闲时限、双向适配器完整性、二进制格式一致性 | Session 编译与兼容性检查；不提供转码或自动重连 |
| `backend/protocol/session_handshake.go` | 声明字段白名单、重复 header/query、凭据与传输头隔离、映射后再次校验 | handshake 保留值及拒绝覆盖测试；编译期和映射后校验属于不同边界 |
| `backend/protocol/session_transport.go` | 队列包含正在写出的帧；读/写/ping 独立工作者；关闭、取消、EOF 与 drain | RunSession 和队列测试；工作者 join 与关闭时限屏障保留 |
| `backend/relay/protocol_import.go` | 旧配置转草稿；无法证明等价的特性给阻断诊断；原定义只保留作修复证据 | legacy import 回归；旧默认 null/空模型语义是迁移目的，不能按冗余删去 |
| `backend/relay/protocol_module.go` | 编译器只登记模块与能力，不持有客户端及凭据 | 内置模块/编译测试；小型装配入口仍有合法用途 |
| `backend/relay/protocol_transport.go` | 动态 timeout 客户端、流 idle 独立、任务/模型请求拒绝重定向、URL 错误脱敏 | HTTP/流传输及取消回归；没有删除拨号地址校验 |
| `backend/relay/protocol_websocket.go` | secure client 共享、拒绝凭据重定向、帧限额、直接关闭底层连接打断握手 | 二进制/heartbeat/取消、关闭超时、阻塞写及重定向测试 |
| `backend/protocol/value_schema.go` | 数值精确比较且指数有界、缺失/null、schema 递归、排序后的稳定诊断 | 数值/schema/容器回归；精确内容比较和原生字节比较目的不同 |
| `backend/protocol/semantic_compare.go` | 对照前只去除适配器记账；能力按节点和工具生命周期观测；原生字节另验 | 语义等价及组合验证；动态工具名称/ID 原始拼写索引应随转义比较专项继续核对 |
| `backend/protocol/usage_attribution.go` | 已知账本计数非负、有界、分桶不超额；未知嵌套保留；ID 不当作工具关联 | attribution 回归；缺失计数不推断为观测零 |
| `packages/webui/src/lib/protocol-document.ts` | 有界 JSON span、重复键拒绝、字符串解码、任意数字原文、局部替换 | 文档与策略精度浏览器回归；不改签名和任意 JSON 值 |
| `packages/webui/src/lib/conversion-document.ts` | 表单只读类型化字段；任意 value 不经普通 JSON 重建；规则排序按原片段 | 策略非法输入/大整数/排序回归；形状检查不是服务端语义验证替代 |
| `packages/webui/src/lib/agent/tool-inputs.ts` | 历史缺失 input 按同轮 callId＋工具名回填；每轮清空索引 | Agent 历史/工具页面回归；只参与展示，不参与工具执行或模型参数生成 |
| `packages/webui/src/lib/agent/fences.ts` | 仅为流式 Markdown 补围栏的展示帮助方法 | 当前 Agent 展示覆盖；混合围栏与完整 CommonMark 语法未专项穷尽，不宣称实现通用解析器 |
| `packages/webui/src/pages/protocol-designer/conversion-fields.ts` | 阶段文案完备映射与公共表单样式 | TypeScript 阶段枚举检查；pretty 只用于可接受普通 JSON 展示的值 |
| `packages/webui/src/pages/protocol-designer/conversion-rule-editor.tsx` | 任意 value 保留于文档；只有显式动作切换重设相应 value；类型化字段局部修改 | 转换策略页面及精度回归；索引用于当前规则排列，不缓存旧规则副本 |
| `packages/webui/src/pages/protocol-designer/conversion-preview.tsx` | 输入快照归属、卸载保护、过期预览隐藏、精确 JSON 展示 | 本地后端预览；不能把同输入缓存等同于供应商结果 |
| `packages/webui/src/pages/protocol-designer/conversion-activation.tsx` | 修订与适用范围独立选择；提交由父组件负责；验证报告展示 | 激活/回滚及旧修订验证流程；本组件不执行证据持久化 |
| `packages/webui/src/pages/protocol-designer/use-conversion-document.ts` | 原文为唯一编辑状态、合法 policy 才允许字段操作、规则创建及移动 | 页面非法 JSON 与大整数回归；parse 结果不用于重建未知字段 |

原表中的 698 项 pending 是冻结时的记录。本表 34 项均来自这些 pending 路径；其余 664 项不因此改变状态。删除责任仍由独立补查表说明；没有把当前文件阅读扩张为全库审查完成。

## 已复现但本轮不改动的协议边界

使用真实内置 Responses 定义与 `DecodeFrame`，依次输入以下完全合成帧（没有供应商请求）：

```json
{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"object":"response","status":"in_progress","output":[]}}
{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}}
{"type":"response.output_item.done","output_index":0,"item":{"id":"msg","type":"message","role":"\u0061ssistant","status":"completed","content":[]}}
```

第三帧返回 `unsupported_capability at /item/role: message role changed`；只把第三帧角色改回未转义的 `assistant`，同一路径成功。原因是 `finishResponseMessage` 直接比较两个保留原始拼写的 `Value`。已有 dev.69 的 `IsString` 修复覆盖常量分派，尚未覆盖所有两个动态字段之间的比较。

进一步绕过编解码器，直接向 `EventReplay` 传入同一 message 的 ItemStarted→ItemSnapshot：只把 Role 从 `assistant` 改成等价转义，或只把 ID 从 `msg` 改成 `\u006dsg`，分别在 `/item/role` 和 `/item/id` 返回 `invalid_association: message identity or role changed`。所以不能假定只修改 Responses 编码模块中的一个位置就完成。源帧 `checkResponsesItemID` 同样使用原文比较，后续须核对“真实重复 ID 拒绝”和“相同 ID 不同拼写”的边界。

最小实验通过 Go overlay 注入临时测试，不改工作树；日志位于 `%TEMP%/elysia-quality-dev69/escaped-completion-probe.log`。此处“通过”表示普通值成功、等价转义值误拒均已复现，**问题仍未修复**。现有工程回归全绿也不会关闭它。

后续独立修复步骤：

1. 固定双向转义、相同值、真实变化、缺失/null/错误类型的回归。分别覆盖解码器、EventReplay、网关、预览及组合验证，并加入完整正文/工具/usage/终止流。
2. 仅对已经完成类型校验的控制字符串做解码后比较；逐一核对 role、status 和身份字段的契约。不得全局规范化 `Value`，或用通用 JSON 重建改变原生字节、签名与续传摘要。
3. 保留真实角色/身份改变的拒绝及原路径诊断；完成帧重复、终止顺序和流资源限制保持不变。
4. 单独提交语义修复，在当时编译器版本上递增并通过现有机制重验证据。现有定义、草稿和手动绑定保持原状；本轮 dev.69 不变。

这是本地合成证据，尚无真实渠道或实际客户端复现，不扩张为普遍线上故障。
