# 发布删除路径：替代责任补查

本表补充原 `fa140fb → d2ed8c2` 台账，现行实现核对至 `64c3f9a`。原 976 项 CSV 保持冻结；以下不自动把现行文件标为整文件审读，也不把“找到替代入口”当作旧功能完全等价。仅列实际阅读了旧文件全部删除内容的九个路径；其余 87 项删除路径尚未在本轮补查。

| 原删除文件 | 已核对的替代职责 | 测试责任与未关闭范围 |
| --- | --- | --- |
| `backend/server/agent_bash_tool.go` | `agent_cli_tool.go` 的 `elysiaCLITool`、`agentCLIContext`、`ProbeGates` 承担入参、工具定义、门控及执行适配；命令执行在 `agent_cli.go`。名称变更为 `elysia_cli`，旧 `bash` 不作为别名执行 | `TestCLIPromptResponsibilities`、`TestCLIRenamedToolPermissions` 显式验证发布工具、ask/always/never/plan 以及旧名字的 unknown-tool 路径；解析、门控、脱敏和批处理分别由 CLI 回归承担。替代责任已对应，不能说名称变更前后接口相同 |
| `backend/server/usage_maheshvara.go` | `gateway_usage.go` 的估算、`updateRecordProtocolUsage`、`observeHostedTools`，以及 protocol 的 Counter/MergeUsage 承担原用量映射；原来的零值折叠已改为指针区分缺失和观测零，工具计量依据实际结果 | `TestProtocolUsageDetailsReachStatisticsAndKeepTailZeros`、`TestProtocolEstimateCountsUnicodeFilesAndToolHistoryOnce`、`TestHostedToolAccountingDeduplicatesAndHonorsSource`、HTTP/SSE 计量落库回归承担替代责任。此为已发生的语义迁移，不是本轮等价重构 |
| `packages/webui/src/pages/agent/activity-view.tsx` | `message-stream.tsx`、`tool-call-group.tsx`、`tool-call-row.tsx` 在会话时间线提供运行状态、参数、结果、持续时间与复制入口；不再提供独立近期执行侧页 | Agent 浏览器工具结果顺序、展开详情、一次性原文/脱敏历史覆盖当前职责。旧“最近 12 条”侧页布局已取消，不声称仍保留该界面 |
| `packages/webui/src/pages/agent/shortcut-settings.tsx` | 快捷键选项移到运行配置 `agent-defaults-section.tsx`，仍调用 `send-key.ts` 的同一组 setter/hook；composer/审批消费者保留 | 已核对全部旧选项和值，以及新设置入口；本轮浏览器覆盖默认发送和设置提交期间阻止发送。跨 tab、存储抛错、Ctrl/Cmd/IME/所有 Esc 组合没有完整专项验收，因此只关闭“找到替代实现”，保留交互覆盖缺口 |
| `backend/server/relay_plan.go` | 已查 `prepareGatewayPlan`、`bindGatewayRequest`、`collectGatewayCandidates`、`serveProtocolRequest`：鉴权、候选、绑定能力、计量预留、限流和失败处理分属明确路径 | 网关矩阵、在途修订、取消、候选/预算及能力测试存在。旧多模态软过滤、组级 MaxTokens 和不同入口覆盖语义仍须逐项与当前契约对照；本项保持部分核对，不关闭替代责任 |
| `packages/webui/src/pages/sources/source-form/key-models-panel.tsx` | 已查源表单、`compileApiKeysPayload`、sources 的按 key 分组。现 UI 区分自动发现与手动模型 Key 分配，不再逐 key 复用旧面板 | 旧 fetchedModels/allowedModels 的未拉取、空数组、全选/反选及自动/手动切换语义需要补完整对照与页面专项；本项保持部分核对，不因类型检查通过关闭 |
| `backend/relay/constants.go` | 旧 SSE 扫描器、Claude 输出预算、effort 档位、计量来源和 MIME 缺省来自混合用途常量区；现限额、推理能力、计量来源、媒体格式分属协议模块 | 已确认旧文件不再被引用；尚未完成每个旧默认值的产品语义对照，不能仅因新模块也有默认值就认定完全接替。保留部分核对状态 |
| `backend/relay/custom_match.go` | `legacy_config.go` 只保留旧 Match 的反序列化结构；执行层改为 `expression_eval.go`，旧配置经 `ImportLegacyProtocol` 产生草稿或阻断诊断 | 原 nonEmpty/isFalse 对缺失、false、0 的特殊语义不能直接套入新严格布尔表达式；新数值比较使用有界精确数值。已读旧实现并核对新迁移/执行入口，旧配置逐操作符迁移矩阵尚未穷尽 |
| `backend/relay/maheshvara_helpers.go` | 旧角色折叠、思考信封、data URL 与 budget/effort 帮助方法已分别由类型化编解码、作用域续传、媒体编解码和 reasoning 控制承担相关职责 | 这些是协议语义迁移，不能按“移动帮助方法”认定等价；续传与推理测试存在，但旧跨供应商信封/角色宽松规范化的每个行为是否明确拒绝或保留仍需逐项对照 |

补查中另见两处需要后续验证的质量问题：`send-key.ts` 的 localStorage 写失败注释声称内存仍更新，但当前 getter 仍从 localStorage 读取；CLI 文档生成器前言仍称 Key 帮助写着“创建后不可改”，与已经修正的命令说明不一致。本轮没有修改这两条路径；前者应先用存储抛错的浏览器用例确认行为与产品预期，后者应连同生成器和生成文档单独同步，避免只修成品后被再生成覆盖。

结论：三项替代责任与当前回归已明确、一项找到替代入口但交互覆盖不完整、五项仍需语义对照；其余删除路径继续待审读。不能将此表写成 96 项删除责任全部完成。
