# DSL 阶段验收记录（2026-10-30）

本记录对应运行代码 `7963d0de`，工程检查提交 `cef9f6e3`，打包提交 `24618cc0`。
后两者只补文档与源码清单，后端／前端生产源码相同。分支为 `refactor/protocol-dsl`，
协议编译器仍为 dev.78，Go 1.27.2，PGO 关闭。所有提交仅保存在本地。

## 已完成的阶段功能

- 四协议请求／响应草案继续差分；Gemini 闭合 system 包装扩展与 thinkingConfig 的正常双向行为。
- v3 单响应文本事件可以通过独立 Spec 流契约运行，核心版本、原生协调、收集和离线序列验证接通。
- 减少条件布尔值解码、wire 根对象重复解析和当前 item 重复解析；未新增全请求任意对象缓存。
- 新代码按编译、读取、载荷处理及状态提交分组；没有新增供应商 Go 分支、生产双执行器或错误后重跑。

四个生产预置仍使用旧模块，生产加载器尚未接受 v3。字段处置完整性、错误顺序、来源差异、
供应商流、任务／会话／Agent、编辑和激活闭环、删除旧模块均未完成。
完整状态见 [迁移记录](protocol-dsl-migration.md)，流的支持范围见 [流契约说明](protocol-dsl-streams.md)。

## 工程结果

| 检查 | 本次结果 | 原始日志 |
|---|---|---|
| Go 全量 `go test ./... -count=1` | 通过，约112秒 | `checks/stage-cef9-go-all.*` |
| `go vet ./...` | 通过 | `checks/stage-cef9-vet.*` |
| staticcheck v0.8.2 | 命令失败；仍为既有45条，无新增或移除 | `checks/stage-cef9-staticcheck.*` |
| protocol/builtin race | 通过 | `checks/stage-cef9-race.*` |
| 审计工具自测 | 161项全过，无跳过 | `checks/stage-cef9-audit.*` |
| 前端 TypeScript、lint | 通过 | `checks/stage-cef9-webui-types.*`、`stage-cef9-webui-lint.*` |
| 前端生产构建 | 通过 | 两次构建日志中均有完整结果 |
| 六目标构建 | 修正源码清单后全部通过 | `checks/stage-build-retry.*` |

staticcheck 对照排除源码行号位移，但保留诊断正文与出现次数；结果为新增0、移除0。
既有问题包括生成计量代码的未使用赋值、旧未用函数及 Hertz 的 nil context 调用。
本阶段没有为清零诊断顺带改写这些旧路径。

首次六目标流程在前端成功后被源码清单检查拒绝：新流文档没有登记到清单，尚未开始六目标 Go 编译。
补齐清单后重跑标准构建成功。原失败保存在 `checks/stage-cef9-six-targets.*`，不删除或改写。
一次 protocol/builtin 检查还遇到并行开发中的 Gemini 拒绝证据未固定；固定账本、删除临时采集代码
后重新通过，详见迁移记录。没有将这次中间态失败计为通过。

## 性能结果的适用范围

最新完整 HTTP 为固定 `d40e1940 → cef9f6e3`，五对交替，另对波动场景独立确认五对。
两组合计80批完整消费、usage／终态和上游次数检查通过；末次恢复采样的活动请求及队列归零。

2 MiB 真实小字段修改的 JSON／SSE，累计分配分别下降73.96%／73.87%，完整消费延迟下降
31.32%／31.72%，采样堆峰值下降58.38%／51.09%。短请求基本持平；1024帧和普通图片的
小幅回退及波动均保留，不能称为所有场景无损。详见 [全部数值和限制](protocol-dsl-performance-2026-10-30.md)。

草案组件优化另行测量。即使多轮下降，四草案仍未达到替换旧 codec 的性能门槛；
上述 HTTP 收益来自已在运行的路径，不代表新预置或全部 DSL 迁移完成。

31 MiB 并发、长期保留、取消和慢交付的最终性能矩阵，以及生产激活后的页面／客户端运行验收仍未完成。
本地模拟成功不替代真实供应商和原客户端复测。本阶段没有调用真实供应商。

## 产物和复核

六目标：Windows／Linux／macOS 的 amd64、arm64。Windows amd64 是本机测试平台；
其他目标仅交叉编译，未作平台运行验收。

源码、二进制及 SHA256 已归档至忽略目录
`.tmp-dev/protocol-dsl-2026-10-29/stage-24618cc0-artifacts/`，身份文件为 `identity.json`。
常规产物同时位于 `dist/standalone/`。原始验收日志及性能数据均在
`.tmp-dev/protocol-dsl-2026-10-29/`，未覆盖此前失败、未达标或历史样本。

后续成功计量复用等修改必须另行验证，不能沿用本阶段结果宣称新代码已通过。

## 后续请求 owner 阶段（54d54b45）

独立完成通用keys、深合并及条件字段证明，四份草案的请求foreign owner保护，以及Responses
自身未知根字段／缓存选项保留。原生产预置没有切换，新能力不作为v3生产激活完成。

在固定 `54d54b45` 上重新执行：

| 检查 | 结果 | 原始证据 |
|---|---|---|
| Go全量 | 通过，约103秒 | `checks/owner-stage-go-all.*` |
| vet | 通过 | `checks/owner-stage-vet.*` |
| staticcheck v0.8.2 | 命令失败，仍45条；去行号后新增0、移除0 | `checks/owner-stage-staticcheck.*`、`owner-stage-staticcheck-comparison.json` |
| 相关protocol/builtin race | 通过 | `checks/owner-stage-race.*` |

新增命名字段、未知数字及原生字节的成功比较通过。8项Responses拒绝边界和外族守卫诊断
仍与旧模块不同，未通过放宽静态gap或组合验证掩盖。后续 `05cf4117` 事件版本传播仅另行
跑了定向回归：`checks/event-envelope-core-version.*`；不能直接套用上表的全工程结果。

本阶段没有重跑前端、审计全套或六目标；`dist/standalone` 和已归档六目标仍属于24618cc0，
不能当作54d54b45或后续代码的二进制。最终稳定提交仍需完整工程和平台验收。

## 最新固定阶段（8a6f84b6，dev.79）

`f246eea3` 完成上一轮固定验收并另行归档；随后加入Gemini未知缓存余项、目标版本健康探测、
独立请求诊断修正、只读目标流位置及Native保护、Anthropic同族扩展深合并。
只有经用户确认的诊断修正升级至dev.79，旧编译器证据继续按现有机制重验。

| 检查 | 8a6f84b6结果 | 原始日志 |
|---|---|---|
| Go全量 | 通过，约98秒 | `checks/delivery-stage-go-all.*` |
| vet | 通过 | `checks/delivery-stage-vet.*` |
| staticcheck v0.8.2 | 命令失败，仍45条；新增0、移除0 | `checks/delivery-stage-staticcheck.*`、`delivery-stage-staticcheck-comparison.json` |
| protocol/builtin/server相关race | 通过，约112秒 | `checks/delivery-stage-race.*` |
| 审计工具自测 | 161项全过，无跳过 | `checks/delivery-stage-audit.*` |
| 前端类型、lint、生产构建 | 通过；类型检查包含在标准构建 | `checks/delivery-stage-webui-lint.*`、`delivery-stage-six-targets.*` |
| 六目标构建 | 全部通过 | `checks/delivery-stage-six-targets.*` |

Windows amd64是本机测试环境；Windows arm64、Linux与macOS仅交叉编译，不是运行验收。
最新产物在 `dist/standalone/`，源码、六个二进制和完整SHA256身份另存
`.tmp-dev/protocol-dsl-2026-10-29/stage-8a6f84b6-artifacts/identity.json`。
前一组 `stage-f246eea3-artifacts/` 保留，未覆盖更早产物。

本次真实失败记录也保留：Chat新增fixture曾误用不合法的嵌套tracking metadata，改为旧实现
确认可编码的JSON Schema后再通过；Native流状态反例曾返回错误序号2并绕过effects，修复后
明确拒绝未证明的状态相关协调。请求诊断改动独立捕获20项，只有14项旧侧归因字段变化，
详见 [诊断证据](request-reasoning-diagnostics-2026-10-30.md)。

原有v3原生／诊断差异没有因此全部闭合。新状态结构变大、草案组件仍慢于旧codec；
不能仅凭工程通过宣称性能无损。f246eea3→8a6f84b6完整HTTP首组及独立确认共100批、
82,800请求均完整消费；累计分配基本不变，SSE确认组堆峰+17.15%、普通PNG延迟方向反转，
不利结果与限制见 [阶段性能报告](protocol-dsl-performance-2026-10-30.md)。
尚未执行最新版的真实后端页面运行验收；生产加载／编辑／激活仍未接入v3，
不能将内部候选测试、健康探测helper或序列测试当成这些产品流程已经完成。

## 后续离线组合流与Chat effort切片

`3316b5f9` 以纯声明补齐Chat legacy effort取值及控制冲突，新增8个成功表示字节等价；
5个拒绝条件已对齐但完整诊断仍不同，未改写原gap。随后v3组合验证按最终wire批次模拟交付，
保留旧预置的编码／校验顺序。两项都未启用生产草案，没有修改生成请求热路径。

`checks/combination-output-position-fixed.*` 定向通过；`checks/combination-stage-protocol.*`
protocol/builtin全量通过。首次定向因测试使用不存在的Expression字段而编译失败，
`checks/combination-output-position.*`保留。该首次失败不是生产运行回归。
这些结果不替代后续最终稳定提交的工程、激活和性能验收；六目标匹配产物仍为8a6f84b6。

## 用户身份修复与typed键查询阶段（dev.80）

`f3a5fafa`独立实现用户批准的身份字符串等价修复；原30次入口观察恰有8次拒绝改成功，
其余22次完整输出及错误树不变。`d40e52d6`减少typed Request对象键查询的值规范化，
不增加运行态字段或缓存；配对完整编码耗时短参数确认−3.24%、2MiB参数−8.38%，
大参数分配仅−0.085%，没有HTTP或峰值结论。原反向样本与全部身份见性能报告。

首轮Go全量在原语目录的不完整keys节点中发生编译期nil访问，`dbb1c430`补齐资格条件，
没有改原测试。随后Go全量通过约103秒；不能将第一次失败写为环境问题。
staticcheck首次新增一条ST1008，来自新测试助手的error返回位置，已按Go规范调整；
原46条输出和修正后45条输出均保留，去行号比较修正后新增0、移除0。

| 检查 | 结果 | 证据 |
|---|---|---|
| Go全量 | 修正后通过；首轮失败保留 | `checks/keys-stage-go-all-fixed.*`、`keys-stage-go-all.*` |
| vet | 通过 | `checks/keys-stage-vet.*` |
| staticcheck v0.8.2 | 命令仍失败，原45条；无新增 | `checks/keys-stage-staticcheck-fixed.*`、`keys-stage-staticcheck-fixed-comparison.json` |
| keys、输出位置、package事件、身份和hint相关race | 通过 | `checks/keys-stage-race.*` |
| 审计自测 | 161项全过，无跳过 | `checks/keys-stage-audit.*` |
| 生成一致性 | 通过 | `checks/keys-generator-final.*` |
| 测试助手风格修正后keys定向 | 通过 | `checks/keys-test-style.*` |

上述检查的生产代码为dbb1c430；之后只调整测试助手返回顺序及文档，形成a4a8f275。
该固定提交的前端lint、包含TypeScript的标准生产构建和六目标编译均通过，日志为
`checks/keys-stage-webui-lint.*`和`checks/keys-stage-six-targets.*`。

六目标已在覆盖dist之前核验旧8a6f84b6归档哈希。新产物位于`dist/standalone/`，另完整归档至
`.tmp-dev/protocol-dsl-2026-10-29/stage-a4a8f275-artifacts/`。身份文件包含源码ZIP、全部二进制
SHA256、Go构建信息；六个二进制的vcs.revision均为a4a8f275，vcs.modified=false。
源码ZIP SHA256为`9c8e87410d2cd6f25bf9be876aeeba383c68ade1a24ed14f7b0ce785da609578`。
Windows amd64为本机测试平台；其他五个目标仅交叉编译，不构成运行验收。

尚未重做真实后端页面运行验收。四生产预置仍未切换，v3编辑、加载、激活、完整供应商流及
最终性能矩阵未完成；当前已有的SSE峰值反向结果继续保留，不将组件改善外推为全部请求无损。
