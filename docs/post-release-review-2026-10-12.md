# v1.5.0 后变更：修复与整理记录

本记录对应已批准的发布后优化计划。审查比较起点为 `v1.5.0/fa140fb`，实施起点为 `1f9a21d`；修改按根因提交到本地，不推送。协议编译器保持 `2.0.0-dev.64`，本轮没有扩大转换能力声明、重写用户定义或引入数据库表。

## 审查范围与证据

发布以来共有 910 项文件变更。已核对变更清单、静态检查和本计划涉及的调用链，但**没有完成全部 910 项的逐行人工审读**。本记录不能用作“完整 review 已完成”的证明。

证据分为四类：

- 人工审读：工具结果执行、审批恢复、历史回放；配置保存与发布；绑定继承、激活目录与刷新；策略编辑、异步归属、计量展示；CLI 数据事务；源码快照、协议长函数及原生壳拆分。
- 静态检查：Go vet、固定版本 staticcheck、TypeScript 和 ESLint。检查通过不证明业务行为正确。
- 最小复现：本轮回归使用合成秘密、大整数、故障注入、延迟响应及隔离数据库，不使用业务凭据或提示词。
- 集成测试：真实本地 Go 后端与浏览器、模拟 HTTP 上游、固定 SDK 回放。它们不等同于供应商真实渠道或 macOS 原生验收。

## 正确性修复

| 根因 | 结果与主要实现 | 回归依据 |
| --- | --- | --- |
| Agent 原始工具结果进入下一轮模型请求 | [结果处理](../backend/agent/tool_result.go)先校验 JSON，再生成一次性交付和脱敏模型／存储副本，脱敏先于截断；无效编码明确失败，不重新执行工具 | [工具结果测试](../backend/agent/tool_result_test.go)：串行、并行、审批、截断、历史回放、大整数、非法 JSON 及落库失败 |
| 配置返回错误前已部分生效 | [Config.Update](../backend/config/update.go)持锁构造完整候选、保存后发布；管理入口和 Reload 共用更新锁；Agent 参与保存与重载；根级及已知配置节内部的未知字段保留 RawMessage | [配置更新测试](../backend/config/update_test.go)及管理运行配置回归：非法请求、文件失败、并发、零值不限、嵌套未知字段和大整数 |
| 源策略变更遗漏子模型证据 | [绑定证据](../backend/server/binding_evidence.go)复用有效策略解析，使用 BindingKey 计算受影响集合；配置、绑定及组合报告在 generation 校验后的事务中写入；刷新比较实际 PolicyHash | [源继承测试](../backend/server/binding_evidence_test.go)、[事务测试](../backend/storage/binding_updates_test.go)：继承、解绑、旧 hash、重复刷新、CAS、子项写失败回滚 |
| 激活新修订移除了固定引用的旧修订 | 修订目录按策略 ID 与 hash 建立，激活选择独立更新，不删除仍被固定引用的修订 | [激活回归](../backend/server/conversion_activation_test.go)同时保留新激活与旧固定绑定 |
| 失败覆盖请求阶段诊断 | [AppendDiagnostics](../backend/protocol/diagnostic.go)与调用记录共用追加和去重，失败追加；不同协议、阶段、规则、策略、路径、级别及保真分类不合并 | 请求规范化与后续失败诊断同时落库的网关回归 |
| 策略大整数舍入、非法结构渲染 | [文档处理](../packages/webui/src/lib/conversion-document.ts)以原文为编辑源；任意 value、expression、绑定 overrides 使用 ProtocolDocument；类型不合法时保留原文并显示路径，不能进入表单渲染 | [文档回归](../packages/webui/tests/conversion-document.spec.ts)：`9007199254740993` 加载、编辑、排序、保存、重开、预览不变 |
| 旧异步请求覆盖新状态 | 绑定与策略修订使用代次，Agent 流和停止回调检查 session／连接归属；预览结果绑定精确输入；登录素材失败 Promise 从缓存移除 | model-binding-state、agent-stream-and-chart、conversion-document、login-assets-retry 浏览器用例 |
| 缓存创建计数重复相加 | [用量展示](../packages/webui/src/pages/usage-logs/log-detail/usage-tokens.tsx)使用规范化 Input + Output，缓存创建作为输入拆分；缺失显示未知，真实零保留；TTL 提示依据稳定诊断身份 | 输入 180、输出 200、缓存读 30／创建 50 的总量为 380；余下输入为 100 |
| CLI 创建覆盖已有配置 | 模型组与源创建使用事务内 INSERT 和明确冲突；查询失败单独返回 read_failed | 重复创建、并发创建、关闭数据库查询回归；原数据保持不变 |
| 手动模型列表与模型行分叉 | 源权威模型列表和模型行由 SaveBoundSource 同事务同步 | [手动目录测试](../backend/server/cli_source_models_test.go)及存储故障注入：刷新、重开数据库、清空列表、写失败回滚 |
| 源码快照边界过宽 | [快照清单](../scripts/source-snapshot.mjs)只读取明确随包清单；Git 检出要求与已跟踪源码一致；无 Git 时仍必须有清单；不递归回退 | [构建边界测试](../scripts/source-snapshot.test.mjs)：合成运行配置、未跟踪文件、缺失清单、重复／越界路径及目录链接 |

CLI 的两个数据一致性问题在 v1.5.0 已存在，不标为本次发布后的新增回归。手动模型目录事务不等同于为新增模型自动创建显式绑定；原有未绑定和手动能力设置仍保留。

收尾回归发现保存配置节时仍会删除内部未知字段，并覆盖文件中非法类型的配置节。保存现按类型的 JSON 字段声明替换受管理字段，保留其他字段的原始值；清空已知设置不会复活旧值，显式空出站禁止列表仍保存为 `[]`。非法配置节导致整次更新失败，文件和内存保持原状。这项补修单独执行配置全包、管理运行配置和留存回归，以及 config/server 对应 race、vet、staticcheck。

扩展登录动画回归还暴露了一个本轮修改前已存在的问题：进入过场时整个工具区被设为 `pointer-events:none`，用户无法点击暂停。现只隐藏品牌、主题开关和页脚，保留暂停入口。暂停冻结测试同时补上等待 CSS Animation.ready 的同步点，仍然精确比较后续快照，不放宽为数值容差。这两个用例连续三次通过，随后动画组 29 项全通过。

## 结构整理与兜底审查

| 调整 | 目的与不变量 |
| --- | --- |
| [转换动作目录](../backend/protocol/conversion_actions.go) | 动作名称、适用阶段、目标编码器与参数校验集中，编译器和管理 schema 共用；不新增执行框架 |
| wire contract 与 Responses 流 | 按请求、目标格式、项目开始、内容结束和响应结束拆分；最终 wire 检查仍在用户映射后执行 |
| 思考参数 | 分离预算、模式、级别和摘要投影；预算阈值与输出默认值具名；明确输出上限与严格拒绝保持不变 |
| 网关流 | 一个 gatewayStream 管理一条响应的状态；分离读取／转换、发送及终止；原始用量 replay 与客户端 replay 分开 |
| 协议刷新 | protocolRefreshPlan 集中修订、证据、草稿及变更状态；保留预置独立恢复、旧数据归档和事务内基线检查 |
| 日志迁移 | 分离 schema、正文回填、附件引用、文件格式转换；原事务边界、checkpoint 与完成回执顺序不变 |
| 用量查询 | 原始表与汇总表共用行解码；时间、失败记录、孤立时间戳等不同 SQL 条件各自保留 |
| 策略页 | 独立文档 hook、规则编辑、修订激活和预览组件；不通过重新序列化整个对象简化状态 |
| macOS 壳 | 后端、面板进程、导出下载和更新分到四个 controller 文件，以 AppDelegate 扩展保持单一状态所有者；构建和测试共用 Swift 源码清单。没有重写进程所有权或调度线程 |

删除兜底的判断依据：

- 最低 Go 版本是 1.25，crypto/rand.Read 不返回普通随机源错误：删除失败后退回时间 ID、空预览密钥等分支。加密文件写入、解密认证等真实错误仍处理。
- RFC3339Nano 解析已接受没有小数的 RFC3339 时间：删除第二次同义解析，非法日期仍按既有路径处理。
- `contains("embed")` 已包含 `text-embedding`：合并重复条件。
- Anthropic 手动预算处理的调用者已验证并建立输出参数容器：删除此处重复初始化；跨信任边界的请求和最终输出检查保留。
- staticcheck 确认且查无平台／脚本引用的 firstErrMsg、isVersionedDefinition、sortedDefinitionIDs 等帮助方法删除。测试中的重复 Contains、未使用赋值和错误诊断变量同步修正。
- 流结束 marker 列表表示可接受的替代项：发送方直接选首个，去掉循环中的无条件 break。

明确保留：合法的 nil DiagnosticSink、事务内 CAS、拨号时地址检查、用户 after／wire／载体处理后的验证、工具关联与签名作用域检查，以及关闭流程的任务登记屏障。后者的 Lock/Unlock 用于保证 WaitGroup.Add 已完成，只对这一处添加解释性的 staticcheck 豁免。没有按“上层有 recover”删除下层检查。

网关候选类型还被 Agent 与探测路径使用，不能在没有证明准备不变量的情况下全局移除 prepared 检查。错误原因不同的守卫也没有为缩短行数而合并。

## 验证记录

环境：Windows amd64，Go 1.26.5。没有调用真实供应商收费测试，也没有读取审计配置中的真实密钥。

| 检查 | 本轮结果 |
| --- | --- |
| `go test ./... -timeout 60m` | 全量通过，server 包 1897.054 秒；其后配置节补修单独通过 config 全包及 server 的 RuntimeConfig／Retention 回归 |
| `go vet ./...` | 通过 |
| `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` | 通过；保留必要任务登记屏障的单点豁免 |
| 关键 race | agent、config、storage、server 全通过；server 736.333 秒。配置节补修后另跑 config/server 的 ConfigUpdate、RuntimeConfig、Retention 回归并通过 |
| FuzzJSONMappingPresence，20 秒，2 workers | 通过，532125 次执行 |
| FuzzEventReplay，20 秒，2 workers | 通过，6089 次执行；该短时结果不代表穷尽事件序列 |
| TypeScript、ESLint、WebUI production build | 通过 |
| 真实本地后端 Playwright | 非动画组 76 个不同用例通过；其中部分 UI 用例 mock 管理响应，真实服务用例单独命名 |
| Chromium 登录动画项目 | 29 项通过，0 跳过；暂停相关 2 项另各重复 3 次通过 |
| 审计工具 `npm test --prefix scripts/protocol-audit` | 135 通过，0 失败，0 跳过，包括 SDK 实际消费及隐藏错误检查 |
| 源码快照边界 | 3 项通过 |
| `node scripts/build-standalone.mjs` | Windows／Linux／macOS × amd64／arm64 六目标均完成构建；包含登录暂停及配置节补修，构建内嵌提交为 `f0e9939` |
| 构建产物启动冒烟 | `718a626` 的 Windows amd64 二进制在新隔离库首次启动并连续重启 10 次；每次 runtimeReady=true、实际加载四预置，协议／策略全部表记录及备份文件清单保持不变；通过父进程管道 EOF 正常退出。最终 `f0e9939` 二进制另通过 `smoke-standalone.mjs`，检查构建身份、四预置有效修订及内嵌 UI。这些是新库验证，不称作历史库升级验收 |
| macOS 原生壳编译与运行 | 未执行：当前环境无 macOS SDK、AppKit／WebKit。六目标中的 darwin Go 二进制不覆盖 Swift 壳验收 |

最终构建的 Vite 仍提示 Agent API 同时被静态和动态导入，动态导入不能产生独立分包。PowerShell 的 stderr 重定向把此提示显示为 NativeCommandError；构建脚本随后完成全部六个目标，并输出 Standalone release built，最终二进制冒烟通过。没有通过关闭类型检查或省略目标处理该提示。

race 使用隔离的 Zig 0.13.0 Windows GNU 工具链，未更改系统编译器配置。默认 CGO 关闭、MSVC 目标 Clang 不支持 `-mthreads`；Zig 链接还需明确 Windows 同步导入库及外部链接模式。最终使用：

```powershell
$env:CGO_ENABLED = '1'
$env:CC = '<temporary-zig-path>/zig.exe cc -target x86_64-windows-gnu'
$env:CGO_LDFLAGS = '-lapi-ms-win-core-synch-l1-2-0'
go test -race -ldflags=-linkmode=external ./agent ./config ./storage ./server `
  -run 'ToolResult|Redact|Parallel|ConfigUpdate|RuntimeConfig|Retention|BindingUpdate|InheritedEvidence|StalePolicyEvidence|BoundSource|CreateGroup|Shutdown|SourceRefresh' -timeout 25m
```

新增源码文件需要先加入 Git，再运行 `node scripts/source-snapshot.mjs --update`。运行配置、密钥、审计结果、临时库及构建产物不提交。

## 剩余验收与维护边界

- 在 macOS 按 [原生验证指南](macos-testing.md)执行 `npm run test:macos-app`、托盘／生命周期／导出取消／更新回滚，以及 universal 原生壳构建。静态拆分审读和 Node 构建脚本语法检查不能替代 Swift 类型检查及原生运行。
- 真实渠道、原插件、原环境故障关闭不属于本轮已完成证据；本地模拟与 SDK 回放不能标为原环境验收通过。
- 后续 review 应继续记录逐行审读的具体文件和调用前置条件，不将静态检查、构建成功或普通聊天矩阵当作完整人工审查。

本轮本地提交中，正确性修复、行为不变的重构、构建、原生壳和回归分别提交。可按 `git log 1f9a21d..HEAD --oneline` 定位；没有推送远端。
