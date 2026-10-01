# C06：流式状态与用量审计

对照基线：C05 `f745143`。这里只记录本地回放和模拟 HTTP 结果，未连接真实供应商；这些新发现没有单独在 v1.4.0 上归因。

## 确认问题与修复

| 问题 | 触发条件 | 修复及证据 |
| --- | --- | --- |
| 独立 usage 尾帧丢失 | Anthropic / Responses 终态之后仍有用量事件 | 内置、自定义、Agent 共用排水循环；翻译后的终态推迟到排水结束。四来源 × 四目标的 16 个组合通过 |
| 零值不能覆盖旧计数 | 读取、创建或输出计数由非零更新为零 | 共用 `protocol.MergeUsage`，保留 presence 和 observed/inferred；明确零值到达客户端及 SQLite |
| 输入组件分帧导致总量错误 | Anthropic 读取/创建分开上报；Gemini prompt/tool、candidate/thought 分开上报 | 合并后按来源组件重算总量；缓存创建 TTL 桶跨帧合并，供应商明确总数优先 |
| 累计内容改写被重复追加 | 累计快照不再包含已输出前缀 | 摘要加字节数验证；不能撤回的前缀改写返回结构化错误 |
| 非法工具输入伪装成功 | 参数尚未构成 JSON 就收到工具完成/响应结束 | 片段期间允许不完整，完成时校验；不补 `{}`，不把非法 JSON 包成字符串；自由文本工具使用独立类型 |
| 工具跨帧关联碰撞 | Gemini 不同帧的工具位于相同 partIndex；Chat 多 choice 的工具使用相同 index | Gemini 使用流级工具槽，Chat 使用 choice/index 关联；Agent 不因迟到 ID 更换聚合键 |
| 重复事件和晚到内容被隐藏 | 相同序号重发、序号冲突、终态后新内容 | Responses 有声明序号时有界去重；冲突/回退报错，终态后只允许合法用量/错误，无序号事件不猜测去重 |
| 累计缓冲无界增长 | 长文本、超大参数、不断新增工具或 SSE data 行 | 文本前缀只保留摘要；参数和需要完整终态快照的渲染缓冲受统一限制；SSE 多行整帧也受限 |
| Agent 标准路径吞解析错误 | 单帧错误被记录后继续，非 Chat 终态后提前停止读取 | 使用相同排水循环，错误返回部分结果并停止，不重放已开始的请求 |

Anthropic 起始 `input` 在没有 JSON 增量时被保留，只有上游明确提供的空对象才作为空参数。Gemini 原生 functionCall 允许省略 args 的既有适配规则仍在协议边界处理。目标 Gemini 要求参数对象时，不再把标量偷偷包进 `value`。

## 回归与证据

- `backend/relay/stream_fidelity_regression_test.go`：零值/缺失、TTL 桶、Gemini 分量、16 个尾帧转换、前缀改写、非法参数、序号重复、SSE 帧上限。
- `backend/protocol/stream_state_test.go`：32 MiB 文本仅保留摘要、调用关联、自由文本、完成释放、重复完成、缓冲及条目上限。
- `backend/server/stream_usage_presence_test.go`：内置 Anthropic、预置、任意 ID 副本的真实本地 HTTP 转发和 SQLite 读取。
- 旧的两个工具完成测试补齐合法 JSON 尾片，另有专门失败测试保留未完成输入的拒绝场景；Agent 晚到文本测试改为明确契约错误。
- 修复前证据：工作区外的 `.cache/protocol-v2-c06-before.txt`，在隔离 C05 archive 运行新增 relay 回归。
- 修复后：完整 `go test ./... -count=1`、`go vet ./...`、`git diff --check` 通过；无前端修改。
- race 未通过环境门槛：默认 CGO 关闭；尝试启用并使用现有 clang 后，MSVC target 不支持 Go cgo 的 `-mthreads`。未伪称执行成功，C17 的 Linux CI 必须补验。

## 性能

Windows amd64，Ryzen 5 7600X，同主机顺序运行，各三次：

| 64 KiB 文本流解码 | C05 | C06 |
| --- | --- | --- |
| 耗时 | 1.96–2.32 ms | 0.83–0.94 ms |
| 分配字节 | 约 9,982,200 B | 约 1,017,300 B |
| 分配次数 | 8,242–8,243 | 8,499 |

摘要校验增加少量固定对象，消除了随累计文本反复复制的大块分配。请求转换基准的分配数仍为 Chat 343 / Claude 327 / Gemini 302 / Responses 291，未变化；单次 CPU 为 20.7–24.7 us，最终 C17 仍需完整比较。

## 过渡边界

C06 已把共享状态和用量规则接入生产路径；四种 wire 模块仍通过旧 Maheshvara 事件/请求结构投影，尚未完成 C07–C16 的单内核切换。需要完整结束快照的 Responses 翻译仍保留有界正文，超限明确失败，不声称可以无限缓冲。旧请求渲染器、旧定义 DSL、未声明扩展以及资源/签名的最终能力检查仍由后续阶段统一替换；本提交不是“任意协议已经无损”的证明。
