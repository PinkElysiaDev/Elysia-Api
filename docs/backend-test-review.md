# Go 后端测试裁剪记录

本轮按测试实际验证的行为审查，而不是按文件名、行数或单次耗时决定删除。
详细协议转换由协议层验证；HTTP 层集中验证路由、传输、授权、取消、证据和持久化。

## 已删除的测试与依据

### #50 依赖迁移的整体复审

| 调整 | 依据与当前验证位置 |
| --- | --- |
| 删除 `tools/expr-evaluation/` | Expr 已决定不采用，549 行实验代码无生产调用者；结论与历史提交入口保存在评估报告。 |
| 删除 `storage/sqlc_generate_test.go` | 运行时已嵌入同一份 SQL；再次执行 `CREATE TABLE IF NOT EXISTS` 无法证明列定义一致。分组结果由真实存储业务测试验证。 |
| 缩减 MCP HTTP 矩阵 | 删除 SDK 自有的 method/name 元数据、Content-Type、Accept、batch 校验断言；保留网关版本策略、Origin、无状态方法、body 上限及官方客户端授权/执行链路。 |
| 简化 SSE 写入错误测试 | 每个包装入口验证一次错误传播，不再绑定依赖内部恰好调用 7 次/4 次 `Write`。完整帧 EOF、取消和消费者错误仍覆盖网关适配逻辑。 |
| 删除原子文件并发读写压力测试 | 80 次文件替换主要重复验证依赖及操作系统的 rename 语义；显式权限、失败保留/清理、symlink 策略及配置/密钥/目录缓存/快照集成仍有覆盖。 |
| 精简重复并发循环 | Schema catalog 隔离只验证两份可编辑结果；指针所有权只验证实际存储；CLI 保留两个并发调用，版本内操作校验保留两个并发读者。需要压力重复时使用 `-count`。 |
| 统一 Schema 测试依赖 | 使用生产 runtime validator 校验反射库的输出，不再直接依赖 MCP 的另一个 Schema 校验库。 |

生产实现同步收敛：操作输入按名称使用版本内预编译 Schema，删除请求期 Schema 序列化和临时编译兼容路径；
Sonic 指针选择删除针对旧解析器深度上限的回退；缓存键转义交给 `url.Values.Encode`。
缓存代际、A2A 审批/订阅生命周期、字面 CLI 子集、SSE 完整帧和精确数值预算属于网关语义，继续由应用适配层负责。
同时修复源码快照清单：补入迁移遗漏的 28 个 Go 文件，移除 5 个已删除路径，并将抽出的 SQL 源文件纳入发布快照。

### 此前测试裁剪

| 类别 | 删除内容 | 判断依据与保留的验证位置 |
| --- | --- | --- |
| 过期优化对照实现 | `protocol/conversion_baseline_test.go`、`conversion_differential_test.go`、`conversion_workloads_test.go`、`conversion_performance_test.go` | 测试内保留了 927 行旧转换实现，并继续复用当前生产业务函数；它不是独立的业务预期。删除 650 个冻结实现对拍组合、旧版对照 benchmark 和按 `Context.Err()` 调用次数取消的测试。转换规则、原生字段、精度、恢复、取消由对应行为测试负责。并发请求所有权断言移入 `builtin/conversion_isolation_test.go`。 |
| 非生产原型 | `protocol/conversion_scan_investigation_test.go` | 被测扫描器明确只存在于测试中；其单测、fuzz 和基准没有生产调用者。生产 JSON/typed meter 的边界测试及 fuzz 仍在。 |
| 一次性性能调查 | `builtin/conversion_investigation_test.go`、`large_request_profile_test.go`、`TestRequestMemoryPhases`、`scripts/run-conversion-investigation.mjs` | 调查任务已产出历史报告，主要记录 GC/堆快照或比较试验边界，不是持续回归的业务契约。保留请求 pipeline、工作负载 benchmark 和当前 HTTP profiling 工具。默认跳过的调查项被删除不会被计作普通测试的耗时收益。 |
| 固化内部表示的快照 | `TestRequestMemoryDifferential` 及 `request_memory_baseline.json` | 将旧提交的中间语义 JSON、错误包装链、最终输出一起固化，重复已发布协议样例并约束内部重构。保留公开 wire 结果的 typed/JSON fallback 对照；不再比较内部中间表示和错误链类型。 |
| 复制 SQL 的查询计划检查 | `storage/usage_plan_test.go`、`usage_covering_test.go` | 执行的是测试中复制的 SQL，而非生产查询；生产 SQL 改坏时仍可能通过，同时硬编码索引名和 SQLite 规划器文本。真实统计、筛选、分页、时间边界、rollup/raw 一致性和事务回滚测试继续验证实际存储行为。 |
| 提示词措辞和实现数量 | `TestCLIPromptResponsibilities`、`TestOpsToolRegistry`、`TestTokenToolRegistry` | 逐词检查提示词、禁止某些帮助措辞、要求工具数量恰为 18/4，不代表权限或执行正确。保留 CLI 解析、命令可达性、实际门控、工具执行和文档同步测试。 |
| 标准序列化包装 | `TestToolResultMarshal` | 仅检查简单 map/string 的 JSON 输出；工具结果的非法数据、截断、脱敏和持久化已有实际行为测试。 |
| 反复验证预置及测试样例 | `server/presets_validate_test.go`、`preset_parity_test.go`、`TestVerificationEnvelopeTextCombinations` | 协议包验证已发布样例，集成测试在安装自定义协议时也会验证；不再单独为测试辅助定义再跑整套组合证明。 |
| HTTP 层重复字段矩阵 | `gateway_tool_strict_test.go`、`gateway_gemini_system_test.go`、`gateway_gemini_safety_test.go`、`gemini_thinking_total_test.go`、`gateway_redundant_error_test.go`、`gateway_new_api_error_test.go` | strict 默认值、system role、safety 枚举、reasoning token 推断、错误字段归一化由 `builtin` 中同主题测试直接验证。HTTP 层保留通用请求投影/拒绝、错误转译、计费持久化和工具往返集成测试。 |
| 启动帧的重复多维矩阵 | `TestGatewayChatStartupIdentityMatrix` | 文本/推理/工具 × 三种时间戳 × 四客户端的 36 组 HTTP 测试，再重复调用 wire validator 和离线组合验证。保留 source-event 边界单测、collector 的原始帧/诊断保留测试，以及带启动帧的真实工具往返。共享帧构造移至 `protocol_source_stream_fixture_test.go`。 |
| 重复完整链路 | `gateway_encrypted_reasoning_test.go` | 首次投影断言并入 `gateway_encrypted_reasoning_roundtrip_test.go`，同一套设施验证恢复载荷、日志证据和第二轮恢复。 |

没有用 `t.Skip` 或 build tag 隐藏上述测试；删除项已从源码及其专用辅助代码中移除。

## 已缩减的组合穷举

以下数量表示该矩阵中实际枚举的组合，不把同一子测试内的多个断言算作独立测试。

| 测试 | 原组合数 | 当前组合数 | 选择方式 |
| --- | ---: | ---: | --- |
| 缓存 HTTP/wire 矩阵 | 32 | 16 | 四种原生转发及每客户端一条跨协议路径，均覆盖 JSON/SSE；包含不支持的跨协议缓存拒绝。 |
| thinking HTTP/SSE 矩阵 | 32 | 8 | 跨协议循环覆盖每个入口和上游一次，均覆盖 JSON/SSE。 |
| thinking 候选隔离 | 8 | 4 | 两种控制分别使用 compatible/strict，均保留候选顺序反转。 |
| 自定义协议四个公开入口 | 12 | 6 | 每入口至少一次，HTTP JSON/SSE/NDJSON 均有覆盖。 |
| 单工具往返 | 32 | 16 | 原生与跨协议循环；每用例仍执行两轮。 |
| 并行工具往返 | 32 | 8 | 跨协议循环；每用例仍验证多工具关联及两轮恢复。 |
| 带启动帧的并行工具往返 | 8 | 2 | Chat → Responses，覆盖 JSON/SSE。 |
| CLI 改名权限接线 | 5 | 2 | 保留审批/恢复和旧名称拒绝；always/never/plan 已由 Agent 门控和计划模式 E2E 验证。 |
| Gemini system wrapper role | 32 | 7 | 四种 role 的原生往返，加 SDK user role 的三个跨协议目标；不重复 lossless 场景的 strict/compatible 轴。 |
| Gemini modality usage | 320 | 160 | 零值和大整数分布到不同 modality，取消每个映射组合都重复两遍数字边界。 |
| request schema 分派对照 | 205 | 20 | 四协议 × 五种 schema/after 分派，各选一个文本样例；完整内容目录由 shipped verification 验证。 |
| usage 尾帧显式零值 | 3 | 1 | 保留自定义修订的完整持久化路径；去掉只换平台别名的重复链路。 |

此外，typed 模块对照去掉了重复的 typed 全样例验证和四次 identity-after 全验证，只保留 JSON fallback 的真实样例验证。两组重启幂等性用例由十次重复操作减为两次。

## 缩小数据而保留边界

- 增量编码器测试使用 64 KiB 缓冲区和 128 KiB 总输入，仍跨越配置上限，替代数十 MiB 的重复发送。
- replay 保留量测试使用 16 KiB 预算和四倍预算输入，替代 4096 个 4 KiB 分片；迟到 usage 断言保留。
- 缓存合并、刷新去重、Agent 停止使用同步屏障或 `testing/synctest`，减少依赖真实等待的测试。

授权、秘密隔离、数据损坏、事务原子性、取消/关闭、尾帧计费和真实并发所有权等测试有明确失败后果，本轮保留。

运行、并行边界和准备数据缓存的说明见 [backend/TESTING.md](../backend/TESTING.md)。

## 本机验证结果

- 相对修改前仓库：顶层 `Test...(*testing.T)` 从 1125 个减为 1092 个，净减少 33 个；删除 20 个测试文件，Go 测试代码从 57053 行减为 54104 行，净减少 2949 行。函数重命名和移动不计作额外删除收益。
- 相对上一轮已通过的并行优化版：完整运行的叶子用例从 3422 个减为 2568 个，减少 854 个（约 25%）。按 `go test -json` 的唯一包名/测试名统计，排除父测试和 skip；普通循环中的断言不计入该数字。
- `go test -count=1 -timeout=20m ./...` 全部通过，单次墙钟耗时 240.3 秒；上一轮完整通过版为 258.1 秒。关闭测试结果缓存，保留编译缓存。原始串行版本在 server 包 600 秒时超时，因此没有伪造原版完整耗时或精确加速倍数。
- 裁剪前的完整 `-race -shuffle=on` 已通过；本轮改动的矩阵、fallback、所有权和缓冲区测试又执行了随机顺序的两轮竞态检查，全部通过（65.3 秒）。
- `go vet ./...`、`git diff --check` 通过。源码快照清单同步新增/删除项并清理原有失效路径；使用临时 Git index 表示预期提交文件集进行清单测试，4 项通过，未修改实际暂存区。
