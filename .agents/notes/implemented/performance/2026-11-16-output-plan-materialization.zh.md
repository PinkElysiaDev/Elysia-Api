# Agent Note: 输出计划物化存储

Status: implemented

## Problem

`buildIterationOutput` 为每个迭代项单独分配一个 `JSONOutput` range 变量：把 `&item` 赋给子求值状态会迫使该变量逃逸到堆，即使当前项只是原始叶值。这属于逐请求的物化执行，而不是编译期的计划构建。

`buildOperandValues` 还会在每次 `merge_deep` 和 `merge_usage` 调用时分配结果切片，尽管两个调用方都同步消费其元素。其他操作已经使用接受调用方栈存储的操作数辅助函数。

## Decision

`buildIterationOutput` 按索引迭代，借用 `&items[index]`，不再获取 range 变量的地址。原始数组使用 `plannedArrayItems` 产生的切片；构建、捕获和 walk 产生的数组使用既有的元素切片。借用范围限于同步执行 body 的调用。读取和返回的容器复制 `JSONOutput` 值；结果不保留求值状态指针，源切片也不会被修改。

`buildDeepMerge` 和 `mergeUsage` 各自拥有四元素的栈数组，通过 `selectOperandStorage` 选择存储，再由 `buildOperandValuesInto` 填充。始终分配的 `buildOperandValues` 包装函数被移除。嵌套求值和并发请求不能共享这些数组。更长的操作数列表保留原有的 `make` 回退路径，容量仍为四。求值与操作数顺序、计量、诊断和声明式分发均保持不变。生产代码合计减少六行。

## Alternatives considered

**请求 provenance memo——否决。** `core_read_context_test.go:330` 有意在 memo 所有权扩展到请求时以 “memo owner expanded beyond synchronous response encoding” 失败。`provenanceReadCapacity` 为二，且捕获权限可以给 provenance 根打标，因此根节点复用还涉及权限约束。本次不把 `provenanceReadMemo` 接入 `*Request`，也不扩大 memo 所有权。

**避免复制 evaluation 结构体——否决。** 逃逸分析推翻了该提案所声称的堆分配机制：`child := state` 不逃逸，复制留在栈上。因此保留该复制。真正逃逸的是被取址的 range 变量。

**逐求值或共享的操作数 scratch——不采用。** 调用方的操作数仍然存活时，嵌套操作数求值就可能执行。复用一个可变切片需要显式管理所有权和嵌套层级。调用方局部数组复用既有辅助函数、代码更少，而且不会与其他调用的存储产生别名。

**扩大栈容量或优化其他迭代操作——暂缓。** 移除本次已验证的分配不需要这些改动。四元素容量及 filter/group-run 的迭代实现保持原样；本次并不声称消除所有输出计划分配。

## Consequences

所有实测工作负载的每操作分配次数均降低。最大降幅为 anthropic/history256k：39,323 降至 37,844，即每操作减少 1,479 次分配（3.76%）。测量不能证明整体延迟改善：五项耗时中位数上升，两项 bytes/op 上升。两个修复各自的独立效果未测量。

`plannedArrayItems` 中原始数组的 `make([]JSONOutput, len(values))` 仍然逃逸，输出容器及其存储也仍然逃逸。通用操作数辅助函数仍报告 `make` 回退路径逃逸。本次消除的是迭代中独立的逐项堆对象，不是全部切片或容器分配。两个新操作数调用点的逃逸输出将内联的 `make` 标记为不逃逸，两个局部 `storage` 数组也都没有被移动到堆。

回归覆盖原始和构建源、递归 walk/捕获源、跨另一次执行保留的 map/flatmap/walk 结果、元素身份与精确顺序、源存储不变性、首个错误路径、字节/步数/取消的行为等价，以及操作数数量为二、四、五时的嵌套与并发使用。既有测试和断言没有修改。

## Measurements

环境：Apple device，macOS。工作树：`/Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r3-meters`；干净基线版本：`4efe8fb64bd0a394fcac215036f197ccdb5b161e`。

以下完全相同的工作负载命令在编辑前执行一次，在生产改动和回归测试完成后再执行一次。前后测量严格串行，不并发执行。表中各数值分别取三次样本的中位数；变化百分比为 `(after / before - 1) * 100`。这些是本次新测量，不是此前提供的参考中位数。

```sh
cd /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r3-meters
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol/builtin -run '^$' \
  -bench 'BenchmarkRequestWorkloads/(same|anthropic)/(short|history256k|tools32)' \
  -benchtime=2000x -count=3 -parallel=2 -benchmem
```

修改前：

| 用例 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| same/short | 94,466 | 51,606 | 707 |
| same/history256k | 5,639,786 | 5,885,511 | 26,504 |
| same/tools32 | 1,482,630 | 754,955 | 8,802 |
| anthropic/short | 92,636 | 59,386 | 911 |
| anthropic/history256k | 5,704,679 | 6,530,666 | 39,323 |
| anthropic/tools32 | 1,231,292 | 628,503 | 8,625 |

修改后：

| 用例 | ns/op | B/op | allocs/op | 耗时变化 | 字节变化 | 分配次数变化 |
|---|---:|---:|---:|---:|---:|---:|
| same/short | 91,336 | 51,226 | 699 | -3.31% | -0.74% | -1.13% |
| same/history256k | 5,741,766 | 5,893,028 | 26,055 | +1.81% | +0.13% | -1.69% |
| same/tools32 | 1,521,542 | 766,610 | 8,634 | +2.62% | +1.54% | -1.91% |
| anthropic/short | 94,970 | 58,562 | 881 | +2.52% | -1.39% | -3.29% |
| anthropic/history256k | 5,865,066 | 6,528,177 | 37,844 | +2.81% | -0.04% | -3.76% |
| anthropic/tools32 | 1,269,162 | 624,606 | 8,467 | +3.08% | -0.62% | -1.83% |

第一次干净基线尝试在 anthropic/tools32 开始前被外部终止，不纳入统计。在任何受版本控制的文件编辑前，完整重试成功结束（86.386s）；完整修改后测量耗时为 88.700s。每项三次样本的分配次数完全相同，唯一例外是修改后的 anthropic/history256k（37,844 / 37,845 / 37,844）。

这是有其他应用运行、未隔离的活跃 macOS 会话。完整基线开始时的一分钟负载均值为 2.82，修改后测量开始时为 1.40，验证结束后为 3.67。这些快照不能确定哪个基准区间的竞争更强。额外竞争使 ns/op 偏高：若更多影响修改后，则偏向不利于本次改动；若更多影响修改前，则偏向有利于本次改动。不能用它否认实测变慢。bytes/op 也在样本间波动；不宣称整体延迟或字节数下降。

逃逸分析的生成命令，修改前后各执行一次：

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend build -gcflags='-m=1' ./protocol
```

修改前：`protocol/plan_output.go:490:13: moved to heap: item`。

修改后：`plan_output.go` 中没有任何 `moved to heap: item` 或 `moved to heap: child`。索引借用位于第 491 行。仍存在原始元素切片逃逸：`protocol/plan_container.go:110:15: make([]JSONOutput, len(values)) escapes to heap`。`plan_merge_deep.go:23:34` 与 `plan_usage.go:43:34` 的内联操作数存储选择报告 `does not escape`；辅助函数的通用 `make` 回退仍报告 `escapes to heap`。

原始基准和逃逸日志保留在被忽略的 `.tmp-dev/output-plan-{before,after}.txt` 与 `.tmp-dev/output-plan-escape-{before,after}.txt`；中断尝试保存在 `.tmp-dev/output-plan-before-interrupted.txt`。

## Testing

所有要求的包均通过：

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol ./protocol/builtin ./internal/architecture -count=1 -parallel=2 -timeout=20m
```

结果：protocol 8.127s；protocol/builtin 31.764s；internal/architecture 0.477s。

全后端 vet 通过，无诊断输出：

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend vet ./...
```

针对性回归在普通模式和竞态检测器下均通过（0.453s 与 1.584s）：

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol -run '^(TestOutputPlanIteration|TestMergeOperandStorage)' -count=1 -parallel=2 -timeout=5m
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test -race ./protocol -run '^(TestOutputPlanIteration|TestMergeOperandStorage)' -count=1 -parallel=2 -timeout=10m
```
