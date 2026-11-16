# 实施手册：第 1 与第 4 阶段

Status: not started. Written 2026-11-16.

范围：替换语义文档层（第 1 阶段），并削减它解锁的冗余遍历（第 4 阶段）。背景与实测数据见
`full-refactor-handoff.md`；本文档是构建顺序与验收门。第 3 阶段（生成执行器）是另一份
文档，不得在此启动。

硬约束：随程序发布的 `spec.json`/`mapping.json` 包与用户自定义包完全不动。Schema 4 /
DSL 2 不变。任何用户的协议都无需重新编写。

顺序固定：**语料 → 纪元 → 表示 → 遍历。** 四项遍历优化中有两项只有在纪元存在后才可靠，
这也是纪元不能推迟的原因。

---

## 第 0 阶段——黄金语料（前置条件）

仓库中不存在 `.golden` 文件。这个 oracle 必须任何行为变更之前建立，否则无从证明重构
保留了原有行为。

构建一个生成器，驱动每个内置包的两个方向，并逐字节记录：

- 短文档、长会话（`history256k` 形态）、工具密集与含图像的请求、多种帧数的流帧，以及
  四个内置协议之间每一对跨协议组合的两个方向，各自的完整线协议输出。
- 保留 Native 与不保留 Native **两条**路径。生成器不得清除 `Native`；见文末的 harness 说明。
- 错误情形：每个预期 `ConversionIssue` 的代码、严重级别、阶段与路径。记录问题清单，而非
  只有成功路径。错误文本或顺序改变即为行为改变。
- 限制边界：略低于与略高于各限制的输入，记录触发的是哪一项限制以及上报的数值。

存放于 `protocol/testdata/golden/`，每个用例一个文件。从当前工作树捕获，因此当下自然通过。

验收门：一个重放测试，逐条比对精确字节与精确问题清单；一个 `typed_limits_test.go:238`
风格的新鲜度测试，使对语料的改动无法悄然通过；生成器纳入版本控制且确定性可重跑。

在此项绿色并提交之前，不得开始第 1 阶段。

---

## 第 1 阶段——文档层

### 1.1 变更纪元，先做在既有结构体上

这是可独立验证的最小单元，且它解锁第 4 阶段。先于任何表示变更实施，以便单独验证。

- `Request` 与 `Event` 上各一个单调递增的代数计数器。
- 单一收口：一个 `touch()` 方法，由每个变更辅助函数调用。
- 转换所有写入点。它们散落在 `conversion_request_fields.go`、`core_control_*.go`、
  `package_model_controls.go`、`conversion_apply.go`（`applyClientOutputSettings`），以及
  `runtime.go` 与 `request_workspace.go` 中的克隆／打标路径。
- 一个测试证明不存在 `touch()` 之外的导出文档字段赋值。此处 AST 审计优于反射审计，因为
  它能捕获没有任何测试覆盖到的写入。

验收门：套件绿色、审计测试绿色、语料逐字节不变。

### 1.2 缩小 `Node`，改为 arena 存储

`Node` 为 296 字节——主要分配单元，也是递归单位。一份 64 消息会话在负载字节之前就占用
约 39.7 KB 结构体足迹。

- 把三个切片头替换为指向每文档共享 arena 的单一子区间，使一次遍历成为线性扫描，且内存
  缓存友好。
- 把罕用字段（metadata、cache intents、resources、attributes、未导出的关联字段）迁入由
  节点索引的旁表。
- 目标压到每节点 128 字节以下。

不要改动 `Value`。它是指向不可变原始 JSON 的 16 字节句柄，保留了 absent／null／zero 的
区别，且约 343 处测试字段读取与 `spanIdentity` 缓存依赖它。它是这一层中设计最好的类型。

保留未导出回执——`verdict`、`continuationInput`、`rangeOrigin`、`rangeAssociation`、
`auxiliary`、`controlReceipt`、`requestWorkspace`。它们是承重的正确性与安全状态，多个
测试断言它们的指针身份；它们迁入 arena，而不是消失。

### 1.3 重新生成并调和遍历器

`cd backend && go generate ./protocol`。`internal/coreplan` 与 `internal/semanticmeter`
在构建期反射文档结构体，写出 `plan_core_generated.go`、`semantic_clone_generated.go`、
`semantic_normalize_generated.go` 与 `typed_meter_generated.go`，因此会自动跟随新布局。

oracle 测试（`semantic_normalize_parity_test.go`、`semantic_clone_oracle_test.go`、
`typed_limits_test.go`）会把生成结果与手写反射实现比对。凡是断言了被移动字段之处，需要
行为一致而结构不同的重写。改变 oracle 的**观察方式**，绝不改变它**要求的内容**。

### 1.4 更新约 380 处外部调用点

`server` 338 处，`agent` 36 处。`agent` 直接构造 `[]protocol.Node` 字面量
（`agent/history.go:31,42,67,143`、`agent/context.go:131`）。在语义不变处保持字段名稳定，
使多数调用点无需改动。要警惕的失败模式是：某处仍能编译，但读到的已是不同字段；宁可新增
访问器方法，也不要拓宽结构体。

验收门：完整非 race 套件绿色、`go vet ./...` 干净、语料逐字节不变、
`internal/architecture` 绿色、分片套件仍在五分钟以内。

---

## 第 4 阶段——削减冗余遍历

纪元存在后这些才可靠。以下是实测事实，不是推断：`equalCoreSnapshot` 每请求被作为根调用
触达 **109 次**；`requestContextUnchanged` 运行 9 次，`requestCheckUnchanged` 2 次。

### 4.1 校验比较——最大收益

当文档自见证取得后未被触碰时，每次 `requestContextUnchanged`／`requestCheckUnchanged`
降为一次计数器比较；被触碰时回退到完整的 `equalCoreSnapshot` 遍历。

这完全保持了现有安全姿态。`request_verdict.go:10-12` 表明宁可多走一次，也不接受陈旧的、
与安全相关的校验；被触碰的文档依然走完整遍历。

验收门：语料逐字节不变；一个测试在取得见证后故意修改一个字段，并证明完整遍历仍然发生；
给出前后实测。

### 4.2 逐帧摘要携带

`digestSemanticEvents` 每个流帧**完整序列化事件批次两次**。一旦 `convertEvent` 能通过
纪元报告「无导出字段变更」，且接受的事件数量与顺序与帧一致，解码侧摘要即可替代重算。

验收门：全部流式用例语料逐字节不变；一个测试证明被修改的事件仍会强制重算。

### 4.3 放宽 warn 效应检查

风险低、收益小：当 `options.Diagnostics == nil` 时放宽 `canReuseOutput` 中的 warn 效应
检查，因为 warn 的重数是它唯一守护的可观测量。需要一个测试证明该配置下诊断路径确实未被
使用。

### 4.4 不要尝试以下两项

记录下来，免得有人花一周重新发现。

**`preserveMappedNative` 中的第二次编码（同协议编码的约 17%）保持原样。**
`canReuseOutput` 对四个内置协议全部失败——不是败在 plan 标志上（所有 `encode_request`
plan 上这些标志均为 false），而是败在 `equalRequestSnapshot`，因为转换依法设置了
`ClientOutput.CollectUsage` 与 `ResponsesStorage`。随程序发布的 openai-chat 程序在条件
分支内读取 `/clientOutput/collectUsage`、`/clientOutput/rawStreamOptions`、
`/clientOutput/responsesStorage`，因此这些差异在语义上是活跃的；输出在基准夹具上相同，
只因该夹具是非流式。移除它需要在 `compiledOutputPlan` 上做编译期读集分析，证明 plan 无法
观测任何差异字段，且覆盖枚举根的算子（`keys`、`type`、`entries`、`project`、`walk`、
`visit`、空指针 `read`）——而对 openai-chat 该分析仍然失败。这道门在做真正的工作。

**最终线协议重复校验保持原样。** `ValidateRequestOutput` 使用 `upstream-request`；编码
路径使用 `generation-request`。校验器不同，因此尽管字节相同也并非冗余。另注意
`plan_encode.go:109-112` 的 `mapping.output.validate` 对 package 方向为 nil——那是空操作，
不是一次遍历。

验收门：语料绿色、套件绿色，并逐项报告实测增量。

---

## 纪律

- 绝不为让某阶段通过而削弱断言、放大限制、删除样本或跳过用例。
- 未测量的写「未测量」。不要估算。
- 完整非 race 套件保持五分钟以内且内存有界；分片运行器为此存在，其预算属于契约的一部分。
- 在每个阶段说明中记录套件 wall 时间与 RSS，沿用既有 Agent Note 格式（`.agents/notes/
  implemented/performance/` 下的 zh／en 对）。

## 采信端到端数字前必须修复的 harness 缺陷

`scripts/benchmark-performance.mjs` 会清除 `Native`，因此同协议第二次编码的约 17% 代价
从不出现在端到端测量中，此前引用的每一个同协议端到端数字都低估了真实生产工作量。在用它
判断第 4 阶段之前先修复：让 harness 走保留 `Native` 的路径，或新增一个保留 `Native` 的
同协议用例。

## 未承诺的事项

第 1、4 阶段没有附带任何吞吐量投影。唯一实测数字是第二回合的 A/B 结果（几何平均 1.06x、
峰值堆 −15% 至 −37%）、profile 占比、调用次数、结构体大小，以及校验比较的调用次数。
每个阶段都要实测，并如实报告它实际交付了什么。
