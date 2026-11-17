# Agent Note: 恢复审计发现的测试完整性缺口

Status: implemented

## Problem

独立审计发现四处测试完整性缺口。Finding #1：进程编译缓存使准备前后指针相同，发布函数即使成功返回却不发布，准备到发布的交接测试仍可通过。Finding #2：廉价 Agent 夹具绕过 `saveSource`，Anthropic 与 Gemini 缺少源作者路径到 Agent 调用的正向回归。Finding #3：精确嵌入字节播种不再触发当前预置中省略字段与显式默认字段之间的良性重编码恢复分支。Finding #4：缓存污染哨兵仅检查固定预置，而 Responses 容量测试直接修改 `CompileSources` 共享图的限制。

## Decision

- #1 保留所有原有准备状态与指针断言，新增 `service.View() != previous` 断言。`RegistryView` 固定不可变快照；正常的无持久化变更发布仍须替换快照，而不应要求缓存图指针改变。此断言直接拒绝 unchanged-store 路径上的成功空操作。
- #2 新增 `TestAgentCallerAuthoredAnthropicAndGeminiSources`，每个平台仅一个独立服务/存储与一个本地流式上游。初始创建与元数据编辑均经过生产 `saveSource`。测试检查源记录与已验证绑定入库、显式能力及源策略保留、模型子绑定继承证据重建，以及成功保存对刷新协调器的通知，最后执行一次真正的 Agent 调用并断言文本及上游调用次数。模拟进行中的刷新只登记受锁保护的状态，不启动后台任务，并在清理时移除。两个合成别名都显式声明可禁用的 budget 能力；`seedAgentSource` 和廉价矩阵保持不变。
- #3 新增 `TestPresetRecoveryAcceptsReencodedCurrentDefaultsWithoutWrites`。对一个当前 Anthropic 预置执行完整类型解码/重编码，明确断言其与嵌入 JSON 不满足结构等价，但编译后的身份/hash 相同。用这些字节替换当前修订与草稿，检查真实初始化成功、预置可执行、配置/证据基线与行数不变，以及修订/草稿字节和时间戳原样保留。非等价 JSON 与当前草稿 hash 的先决断言保证会进入修订和草稿的编译回退分支；保留原始存储结果证明没有误修复或归档。
- #4 `TestMain` 在测试结束后通过已有私有缓存结构读取每个仍存活的 weak 条目，在释放锁后以该条目的编译限制独立重新编译并比较完整结构指纹。固定预置也必须在枚举结果中，不能被悄悄遗漏。可选 feature 仅控制准入，不改变图结构；参考编译器启用两项可选 feature，仍使用刻意不缓存的 `compilePackage`。仅排除原先已同步、允许变化的验证 memo。负向测试用局部缓存中的真实自定义图和非默认编译限制，证明头部限制与深层 plan 修改都会被拒绝。Responses 容量测试先通过已有 helper 分离图头，再改变保留边界，并新增共享原图限制未变的断言；两个容量子例及其全部检查保留。

## Alternatives considered

**强制预置编译图指针每次改变。** 拒绝：这会与合法的进程缓存复用冲突；快照身份已能直接观察真实发布，无需增加无关的自定义修订夹具。

**把 Agent 全矩阵恢复为生产源验证。** 拒绝：重复组合验证会扩大日常运行成本。两个有明确断言的作者路径案例补回覆盖，其余矩阵继续使用廉价夹具。

**把全部日常预置种子改回类型重编码。** 拒绝：这会在每次初始化时重复执行良性回退编译。一个有非等价与无写入先决/结果断言的定向回归足以覆盖所报告的分支。

**增加生产计数器、准入 hook 或保留全部历史图。** 拒绝：本任务禁止生产变更；现有缓存条目足以检查它当前仍能交出的全部图，且无需破坏 weak 生命周期或增加常驻内存。

## Consequences

没有修改生产代码，没有删除/放宽既有断言、样本、表项或限制。代码中的删除仅是将原来的预置专用 `TestMain` 遍历与内联报错路径提取为可测试的全缓存检查器；原有独立重新编译和结构比较仍在，新增缺失固定预置的错误检查。另同步更新编译缓存说明的中英版本，使其准确描述当前哨兵。

仍未覆盖：哨兵不是实时生产检测器，无法观察扫描前已经恢复的临时修改；已回收 weak 图既无法检查，也已不能再次交给调用方。其他测试二进制不运行此 `TestMain`。两个平台的正向源案例不逐个平台注入事务回滚/CAS 冲突或所有刷新竞态；良性重编码只选择一个当前预置，而非枚举所有默认字段组合。race、真实计费上游与其他后端包未在本次任务中运行。

完整 `./server ./protocol` 尝试在 server 达到四分钟测试超时后失败；命令总墙钟 259.040 秒，进程组 RSS 峰值 891.609 MiB，protocol 在该尝试中通过。之后将新增 Agent 测试纳入通常的 `t.Parallel` 调度并通过定向集合，但未再重跑完整 server。完整仓库非 race 五分钟目标未获验证，不能宣称达成，也没有扩大超时掩盖这一点。最终 server 验证仅选择 `^(TestAgent|TestProtocolRefresh|TestPresetRecovery|TestSourceSave|TestSourceBinding)`；该集合外的 server 测试未完成最终整包验证。集合内原有 `TestAgentSnapshotPerformance` 因未设置 `ELYSIA_AGENT_SNAPSHOT_OUTPUT` 而保持 opt-in 跳过。

## Measurements

单次观测，环境为运行 macOS 的 Apple device，并非性能对比。每个 Go 命令都以绝对 `-C` 指向本次 auditfix worktree 的 backend；设置 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`。测试公共参数为 `-count=1 -shuffle=on -parallel=2 -timeout=4m`。临时 watchdog 每约 50 ms 汇总本命令进程组 RSS，在 295 秒或 2 GiB 时终止整个组，并清理残留进程。脚本及日志在提交前删除；RSS 为采样峰值而非单进程 high-water mark，Go 内存限制本身是软限制。

| 命令选择 | 结果 | 墙钟秒 | 进程组 RSS 峰值 MiB |
| --- | --- | ---: | ---: |
| `test ./server -run '^TestProtocolRefreshCompilationPublishesPreparedPointers$'` | pass | 18.145 | 888.672 |
| `test ./server -run '^TestPresetRecoveryAcceptsReencodedCurrentDefaultsWithoutWrites$'` | pass | 6.322 | 559.766 |
| `test ./server -run '^TestAgentCallerAuthoredAnthropicAndGeminiSources$'`，最终调度 | pass | 37.855 | 896.672 |
| `test ./protocol -run '^TestCompilationCacheSentinelRejectsMutatedGraphs$'` | pass | 13.090 | 1001.297 |
| `test ./protocol -run '^TestResponsesPackageSourceCapacity$'` | pass | 1.581 | 280.109 |
| `test ./server -run '^(TestAgent\|TestProtocolRefresh\|TestPresetRecovery\|TestSourceSave\|TestSourceBinding)' -json`（正则中的竖线不转义） | pass，64 个顶层测试 / 含子测试共 135 个通过，1 个原有 opt-in skip | 91.102 | 1119.469 |
| `test ./protocol` | pass，含最终缓存哨兵 | 12.116 | 338.906 |
| `vet ./server ./protocol`，最终代码 | pass | 2.713 | 228.125 |
| `test ./server ./protocol`，上述调度调整前 | fail，server 测试超时，protocol pass | 259.040 | 891.609 |

新增源测试的首次开发运行失败（20.442 秒，604.328 MiB）：原先借用的通用合成 Gemini level 能力不能编码禁用状态。改为该测试显式声明双方映射都支持的 budget 能力后通过；未改变生产行为或移除任何断言。
