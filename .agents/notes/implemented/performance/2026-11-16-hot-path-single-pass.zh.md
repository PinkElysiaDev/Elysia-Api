# Agent Note: 热路径单次判定与保守复用

Status: implemented

## Problem

请求在解码、转换、路由和编码边界重复验证相同的语义内容。每条 owner effect 独立遍历整个内容树，同一 wire 的 native reconciliation 还重复完成基线解码和编码。成功路径为诊断拼接大量字符串，固定语义字符串和简单标量也重复经过通用 codec。

必须保持 external A → A Mapping → Core2 → B Mapping → external B，不引入 provider 专用 Go 分支。诊断的路径、代码、阶段、建议及首个错误优先级是兼容性要求，不是可以用性能换取的细节。

## Decision

`package_owner_walk.go` 对 node 和 metadata owner 做一次联合 DFS，在每个 owner 上按声明顺序执行局部条件。每条 effect 保留逻辑计费，成功结果在原声明位置提交。联合计算自身共享原有剩余 step allowance，不为每条 effect 扩张请求预算。任何拒绝、表达式失败、不完整结果或累计预算不足，都回到原来的 effect-major 检查；因此不同 owner 上同时失败、夹在 owner effects 之间的普通拒绝/警告、资源错误及 metadata 的来源路径仍按原顺序报告。冷失败可以重复部分工作；成功路径不为 owner 生成诊断路径。

`requestVerdict` 只在没有 scope resolver 的成功解码后发布，包含私有深拷贝 witness、完整 target capability 副本和有效 limits。公开 `Request` 的任意嵌套 map/slice 都可在下一次 API 调用前被修改，因此不能仅凭指针身份信任历史通过。复用先做精确的结构比较；复用 `CheckRequest` 还要求相同协议、方向、scope、capability 集和 limits。比较复用现有不可变 typed plans，不维护跨请求的摘要缓存。

两个内容 validator 均不读取 `ClientOutput`，因此 delivery-only 设置不使内容 verdict 失效；这是明确枚举的唯一字段排除。其余导出字段全部比较，受保护的 node range association 也比较。映射输出复用使用更严格的完整文档比较，不能沿用这个排除规则。新增 validator 输入时必须复查这条边界。

`conversionState.contextValid` 仅覆盖私有、连续拥有的文档。重新加载、JSON 规则边界及可能修改内容的 typed action 都使该判定失效；只有 witness 精确证明验证输入未变，才能保留解码 verdict。验证失败不缓存。scope resolver 在最初的 context 验证之后运行且可编辑内容，故它的输出不获得该早期判定，下一边界照常验证。安全相关的陈旧通过比多做一次遍历严重得多：无法证明内容和验证输入均未改变，就重新验证。

server 的 `CheckRoute`、binding 和 transport 检查没有删除。路由使用不同或更严格的 limits/capabilities 时，仍完整运行内容检查；没有把不同预算偷偷当成等价。

native workspace 另持有一个从未交给调用方的成功 finished decode。只有原 workspace 的 compiled/value/source/context/limits 证明均成立、没有可编辑的 resolver、有效 replay limits 相同，才只读复用该结果。缺少证明时保留原 rebuild。仍执行 `ReplayNative` 的授权/来源检查、carrier 检查、reconciliation 和最终 wire 验证。仅当当前请求与基线的完整语义文档、解码上下文和编码预算均相同，且程序不观察可变 state/delivery、不写 stream state、不含 warning effect 时，才让已生成 output 充当 baseline output。continuation、delivery 编辑、不同上下文、不同 limits、stateful 程序和 warning 都保留独立基线编码；尤其不能丢掉原来第二次编码产生的不同 warning。`DiagnosticSink` 仍按原规则折叠完全相同的 issue。

诊断路径使用借用的 `diagnosticPath` scratch，在产生 issue 或声明的 path binding 真正需要字符串时才拥有字符串。children、reasoningContent、tools、cache 和 resources 保持原顺序与指针拼写。callback walk 可能仍需要一个固定大小的每次遍历 scratch 分配；没有声称所有深度和所有 callback 都零分配，也没有扩大深度限制。工具 status 成功检查没有路径分配。

core controls 共享一个小型执行 frame：直接读取当前 typed document，不预先创建 root/read closure；预算和 value reader 在实际执行时绑定，bindings、context 编码和 context object index 按需初始化。`StringValue` 的封闭语义常量集合使用编译期 JSON 字符串，不建立保留任意用户文本的全局 interning map。作者 literal 已在编译图中持有不可变 `Value`。

scalar fast path 仅接受能证明完整成功的 ASCII string、boolean、规范整数和 null；复制字符串以保留 decoder 的所有权。Unicode、转义、溢出、错误类型、nil target 和畸形 JSON 全部回到原 sonic 路径，保留接受范围、错误类型、消息和目标的部分写入。absent 与 null 仍不同。

## Alternatives considered

**在联合 DFS 遇到首个 node 拒绝就返回。** 不采用：node-major 首个失败不一定是旧的 declaration-major 首个失败；metadata 与普通 effect 的穿插顺序也不能改变。

**给公开 Request 添加可变版本号，凭指针复用。** 不采用：调用方可以直接编辑嵌套容器，绕过 setter 或版本号。私有不可变 witness 的精确比较更保守，代价是仍有比较遍历和 witness 拷贝。

**省略路由检查，或把不同 limits 当成相同。** 不采用：binding/transport 是独立授权边界，且旧检查可能在较严格预算下拒绝。缺少预算等价证明就重新执行。

**所有 same-wire 都跳过 baseline 编码。** 不采用：delivery 设置、上下文、state、continuation 或 warning 都可能使重复执行可观察。当前只消除可证明相同的工作，不引入 unchecked native passthrough。

**独立实现 scalar 的错误解析器，或缓存所有字符串。** 不采用：前者容易改变 codec 错误与部分写入，后者无界保留用户文本。fast-success 与原 codec 冷回退、更小的常量集合保留兼容性。

## Consequences

成功 owner 检查共享一次树遍历，但逻辑资源计费和声明优先级保持不变。内容验证可以复用判定，但公共文档的精确比较仍然需要遍历；实际转换、resolver、不同 target/limits 继续重验。native finished decode 的 witness 增加了私有 snapshot 容器；可证明不变时减少原 finish 和 output plan 工作，其他路径继续 rebuild。失败、warning、深路径、复杂 scalar 和动态状态有意保留较慢的兼容路径。

新增回归覆盖 shipped owner 类别的全部 issue 字段、跨 owner 与普通 effect 的首错顺序、预算边界、有效与无效内容修改、resolver 修改、delivery-only 验证与严格 replay 的区别、baseline warning 次数、nested/reasoning/tool/cache 路径，以及 scalar 与旧 decoder 的成功、畸形、null、wrong-type、nil-target 和部分写入一致性。没有删除样例、table case、断言或协议 limits。

### Diff deletion review

- `capability.go` 与 `server_tool.go` 删除 eager 路径版本的遍历/分支文本，等价分支移入 `*Path` 方法；保留 string-call wrappers 供其他语义边界使用。metadata 的失败使用原 validator 和真实路径重新生成诊断。
- `conversion_request_projection.go`、`conversion_tool_results.go` 删除每个 owner 的 `fmt.Sprintf`、拼接与旧递归调用，改成借用 scratch；验证顺序和失败构造不变。工具 streaming option 只在失败时补上原路径。
- `conversion_cache_projection.go`、`package_cache_projection.go` 删除 eager callback 字符串和不再使用的 import，仅在已有诊断/拒绝位置 materialize 原路径。
- `core_control_node.go` 删除逐节点 `evaluation` 值复制和 eager `strconv` 路径递归，改为共享 state 指针和 borrowed-path 内部函数；原 string visitor 入口保留。
- `core_controls.go` 删除 eager context struct 编码、三个 root reader closures、独立 reader/budget/object reader/bindings 初始化；同样的文档类型与版本检查保留，工作移至 lazy frame 和直接 typed reader。
- `conversion_state.go` 的直接验证返回和 load 赋值被可失效的私有判定替代；原 validator 与 mutation 后检查保留。
- `package_execute.go` 的每 effect 无条件 walk 被成功 verdict 提交/原 walk 回退替代。原 effect-major 实现没有删除。
- `plan_input.go`、`plan_output.go` 替换仅检查 closure 的条件和 eager bindings 分配，以支持 typed reader 与按需 bindings；非 control 路径保留原分配和执行。
- `runtime.go`、`request_workspace.go` 的无条件 baseline finish/encode 仅在证明成立时被替代，旧 rebuild 分支保留。原 replay 创建条件扩大到所有有兼容 Native 的请求，以便严格相同的非 projection 请求也能复用；不是跳过来源检查。
- `value.go` 删除合并的 scalar decoder case，拆为 fast-success 加原 codec fallback；复杂类型仍走原 codec。其余删除均为上述重排产生的 import/空行，不删除测试或契约。

## Measurements

环境：Apple device、macOS。工具链由 `go -C backend version` 记录为 Go 1.27.3。所有 Go 测量使用 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`。以下命令以本任务 worktree 为仓库根目录；其绝对路径是 `/Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-hotpath`。

前后均由 `/usr/bin/time -l` 包裹同一命令，表内采用各 case 的重复运行中位数；百分比为 `(after/before - 1) × 100`。

```sh
/usr/bin/time -l env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 \
  go -C backend test ./protocol/builtin -run '^$' \
  -bench 'BenchmarkRequestWorkloads/(same|anthropic|responses)/(short|history256k|tools32)' \
  -benchtime=20000x -count=3 -parallel=2 -benchmem
```

最终后测仍在运行；最终前后表尚未测完，不估计性能百分比。

完整正确性与架构由以下最终命令产生，全部通过。

```sh
/usr/bin/time -l env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 \
  go -C backend test ./protocol ./protocol/builtin ./server ./internal/architecture \
  -count=1 -parallel=2 -timeout=20m
```

| 检查 | 结果 | Go 报告用时 |
| --- | --- | ---: |
| `./protocol` | PASS | 5.667 s |
| `./protocol/builtin` | PASS | 30.840 s |
| `./server` | PASS | 448.700 s |
| `./internal/architecture` | PASS | 0.503 s |

同一完整命令的 wall 为 **489.50 s**，峰值 RSS 为 **1,426,407,424 bytes**。`GOMEMLIMIT` 是 Go heap 的软限制，不是 OS process RSS 硬上限；这里报告实际 RSS。日志保留在忽略目录 `.tmp-dev/hotpath-full-final.log`。

baseline 的前期 focused checks 与测量存在短暂重叠；最终完整 suites 与后测顺序运行，不并行。wall timing 受系统活动影响，不能把 allocation 的下降直接当成延迟收益。没有测量 live-provider、race 或端到端服务吞吐；不声称这些结果，也不声称总体加速目标已达成。
