# 请求 reasoning 诊断归因修正（dev.79）

本次修正请求 reasoning 校验误报为 `decode_event / stream` 的问题，不改变请求接受条件、
转换结果或策略。独立证据基线为 `65d38827e713181dcae4923f00d368548fcf5e11`，编译器由
`2.0.0-dev.78` 升为 `2.0.0-dev.79`，沿用现有编译器版本绑定与离线证据重验机制。
不修改预置、用户定义、草稿或绑定；本次也没有接入尚未闭合的声明式协议草案。

## 修正范围

`backend/protocol/reasoning_controls.go` 的 39 个错误出口错误复用了流事件的 `streamIssue`。
它们现改用私有请求校验错误构造器，阶段为 `request.validation`，建议语改为修正请求控制字段。
共享校验函数本身不指定 wire 方向，由既有公开 `DecodeRequest`／`EncodeRequest` 包装提供真实方向。
独立校验及原始 `Execute` 没有该包装时保留空方向，不为展示完整而虚构归因。

错误类型、错误码、路径、原因、错误文字、`CandidateOnly`、cause 链、包装层数及校验顺序保持不变。
已经正确归因的 `validateAnthropicDisplay`、通用 `streamIssue`、Mapping 错误构造与 runtime 包装
均未修改。其他请求控制辅助函数中可能存在的 `streamIssue` 误用仍属后续独立审查范围。

本改动只替换原有失败出口的构造器；正常成功路径未增加校验、复制或分配。
未运行性能基准，因此不声称有实测性能改善。

## 隔离采集与账本修正

前后源码均从同一个 Git 归档解出。after 仅覆盖 `reasoning_controls.go` 和 `definition.go`，
排除了工作区中其他流式与 DSL 修改。两份归档只在测试助手中增加实际错误树日志输出，
没有绕过静态期望，也没有增加自动更新期望的测试代码。

环境为 Go 1.27.2 windows/amd64、`CGO_ENABLED=0`、`GOMAXPROCS=4`、`GOGC=100`、
`GOMEMLIMIT=8GiB`、PGO 关闭。构建与执行顺序进行。

```text
go test -c -pgo=off ./protocol/builtin -o <phase>/builtin.test.exe
builtin.test.exe -test.run=^Test(Anthropic|Gemini)PackageDraftMigration$/(reasoning|thinking)/^reject- -test.v=true -test.count=1 -test.timeout=3m
```

由于中间段测试表达式未加锚，另捕获 4 个既有 Gemini 来源控制样例；保留为未变化对照。
共捕获 20 项：16 个 reasoning 拒绝样例及上述 4 项。14 项旧侧错误树发生 84 处字段变化，
全部限定为 direction、stage、suggestion。所有 20 项的草案错误树和 values 均未变化；
两个已有 Anthropic display 拒绝样例的旧侧也未变化。错误路径、错误文字及 cause 结构逐项核对未变。

| 执行 | 退出码 | 解释 |
|---|---:|---|
| before 对原账本 | 0 | 原行为与原快照吻合 |
| after 对原账本 | 1 | 14 项旧侧诊断已纠正，原快照按预期失败 |
| 首次 PowerShell 参数调用 | 2 | dotted 测试参数被拆分；原日志保留，后改用 Python argv 重跑 |
| 更新 14 项旧侧快照后的定向 | 0 | 真实纠正结果已固定，其他差异仍按原账本比较 |

更新静态账本前先确认对应 20 项均与捕获的 before 完全一致，再仅将 14 项 `legacyError`
换成实际 after 树。所有 `draftError`、values、case 集合、其他条目及最初的
`sourceCommit: c86b9d14` 保持原样。这个历史 sourceCommit 不是本次修正的采集版本。
原始账本和修正后账本各保留一份；Git 差异恰为 84 行替换。

| 产物 | SHA-256 |
|---|---|
| 基线源码归档 | `457b5bd5d16824876013f97f2e6fb771356567c611e666843ddd5edc68859613` |
| before 测试二进制 | `06aa420801911bb3998f7a3593982053bd1295ac69a032e72ef0817430605ab4` |
| after 测试二进制 | `a57350c9cebf66626bcd4b541a35bd86f5cb54184262df25beaadb23b1de2f42` |
| 原始账本 | `99071064925ce918783dc8004e06e6958fb3df96d0497ec5b8866cd977874588` |
| 修正后账本 | `d6e9196067c855eb5abda1e1b2f666cfaeebddf177ebd871e162b3f7c0f4cfe2` |

原始日志、完整错误树、84 处差异、源码、二进制、命令与环境记录保存在忽略目录
`.tmp-dev/protocol-dsl-2026-10-29/reasoning-diagnostics-dev79-2026-10-30/`。
以上账本哈希按工作区保存字节计算，Git 换行归一化后文件哈希可能不同。

## 定向验证与限制

在完成静态账本修正后，以下测试在当前工作区顺序运行并通过：

```text
go test -pgo=off ./protocol/builtin -run '^TestRequestReasoningDiagnosticDirection$|^TestAnthropicThinkingDisplayInvalidInput$|^Test(Anthropic|Gemini)PackageDraftMigration$' -count=1 -timeout=5m
go test -pgo=off ./protocol -run 'TestReasoning|TestPackageRawRequestReasoningValidation' -count=1 -timeout=3m
```

新测试直接检查公开请求解码、公开请求编码与原始 Execute 的归因边界；既有 display 拒绝、
完整 Anthropic/Gemini 草案迁移及 reasoning 规则测试继续运行。没有为了通过而改动草案结果，
也没有把双方均拒绝当作完整错误树相等。Gemini 已登记的 27 项差异仍未闭合。

本切片未运行 Go 全量、race、六目标构建、性能验收或真实供应商请求；不以这里的定向通过
代替整批工程验收，也不宣称所有请求诊断已经清理完毕。
