# 发布缺口验证与性能实验

本轮从 `5d43b83` 开始，只进行本地开发、测试和提交，没有推送、发布或部署。真实请求限定 `https://moyuu.cc` / `gpt-6.1-sol`；本地压力测试只访问回环地址。后续章节按实际证据补充，不将未执行项目视为通过。

## C19：并发检查

便携工具链为官方 LLVM-MinGW `20260922`、UCRT x86_64，编译器报告 clang `23.1.2`，Go 为 `go1.26.5 windows/amd64`。工具链仅加入子进程环境，没有修改系统 PATH。工具包位于仓库父目录 `.cache/toolchains`。

- 下载：<https://github.com/mstorsjo/llvm-mingw/releases/download/20260922/llvm-mingw-20260922-ucrt-x86_64.zip>
- 发布元数据：<https://api.github.com/repos/mstorsjo/llvm-mingw/releases/tags/20260922>
- 官方资产 SHA-256：`e3ad77d117a4bea19a7a3b333341824d79a5a371004a10e25b8504e7b3047666`。

`node scripts/verify-protocol.mjs race` 先确认 race runtime，再执行 `go test -race ./... -count=1 -timeout=60m`，包含 Agent。随后执行 GOMAXPROCS 1、2、12 的并发专项，每档 20 次、随机顺序；报告保存种子。`--stress-only --seed=1208900205` 可单独复现专项，但不能作为全包执行证据。

| 实验 | 结果 | 父目录 `.cache` 下证据 |
| --- | --- | --- |
| 首次工具链下载 | 环境未就绪，不能判为通过 | `protocol-race-1791026812194/report.json` |
| 首次全包 race | Go 默认 10 分钟超时，无 DATA RACE 报告 | `protocol-race-1791026994250/` |
| 延长时限后的全包 race | 通过，1,403.890 秒，包含 Agent | `protocol-race-1791027827529/race-all.log` |
| 首次单核专项 | 失败：WebSocket 关闭超过期限；不是 DATA RACE | 同目录 `race-stress-1.log`，种子 `1208900205` |
| 修复后原关闭专项 | 单核 100 次通过 | `c19-close-regression/report.json` |
| 修复后单核/双核/12 核专项 | 三档各 20 次通过；442.885 / 326.502 / 285.984 秒 | `protocol-race-1791030131115/report.json` |
| 新确定性关闭回归、完整后端、vet | 关闭回归 race 20 次通过；完整后端与 vet 通过 | `c19-final-checks/report.json` |

关闭缺陷位于 WebSocket 适配器：库的优雅关闭占有 closing 状态后，`CloseNow` 会等待握手，无法保证立即释放连接。没有活动 reader 时，取消 reader context 也不能中断握手。现在适配器持有底层升级连接，在强制关闭时先关闭传输，再等待 WebSocket 清理；正常握手仍发送原关闭码。Accept 保留 Gin 的即时响应头写入行为，Dial 继续复用原代理、TLS 和地址检查。

`TestWebSocketForceCloseInterruptsActiveHandshakeWithoutReader` 等到真实 close 控制帧写出后才触发强制关闭，不依赖 sleep。把该用例叠加到 `5d43b83` 的隔离源码，约 5 秒后失败；修复后通过。基线快照清单在 `c19-close-before/snapshot.json`。没有通过放宽超时、降低并发或跳过用例消除失败。

统一验证报告记录提交、工作区源码摘要、Go/引擎/工具链版本、平台、参数、退出码与日志路径。状态区分 passed、failed、inconclusive、not_run；只保存检查元数据，不记录进程环境中的密钥。中断和超时终止自己创建的进程树，避免遗留测试进程污染后续测量。CI 已纳入 Agent 及失败证据上传，本轮没有触发远程 CI。

C19 的全包 race 证据在关闭修复前产生；修复后有全包普通测试、专项 race 和确定性 race 回归。后续执行内核优化仍须重新执行完整 race，不能用此表替代候选版本验证。

## C20：真实协议与缓存实验

2026-10-03 使用指定站点与模型串行执行，共计 254/256 次真实上游调用（包括早期测试设施失败和预检）；没有重置预算、充值、压力测试或自动生成重试。默认输出限制 128 tokens，但站点是否遵守限制仍以原始 usage 为准。临时凭据只经隐藏输入传给测试子进程，不进入参数、SQLite、源码或证据。隔离 SQLite 使用观察代理占位凭据。

`node scripts/verify-protocol.mjs live --preflight` 只预检；`--suite=cache|matrix|extended|custom|breakpoint|diagnostics` 运行相应实验。`ELYSIA_LIVE_TARGET` 限定目标，`ELYSIA_LIVE_PREFLIGHT_REPORT` 可显式引用已完成预检；复用前检查站点、模型、编译器、定义哈希及 JSON/SSE 成功记录，并保存证据路径与摘要。未执行项不记为通过。累计预算持久化在仓库外 `.cache/protocol-live-budget.json`，独占锁阻止并行付费测试。

测试代理记录最终出站正文、原始上游响应、下游响应及 SQLite request ID/usage。正文仅含人工生成内容，写入前检查凭据；报告保留字段路径、字节数、SHA-256、计数和证据文件名，不捕获鉴权头。独立 JSON 读取器核对 usage，缺失与零值分开。同协议 SSE 检查原生帧回放与 usage 尾帧，不能通过删除未知字段取得成功。

| 目标 | 真实 JSON / SSE 文本 | JSON / SSE 两轮 function | 自动前缀缓存读取及计数链路 |
| --- | --- | --- | --- |
| Chat `/v1/chat/completions` | 通过 | 通过 | 直连及网关均观察到 4,224；下游及 SQLite 一致 |
| Responses `/v1/responses` | 通过 | 通过 | 同上，4,224 |
| Anthropic `/v1/messages` | 通过 | 通过 | 同上，4,224；输入总量存在下述站点口径疑点 |
| Gemini `/v1beta/models/gpt-6.1-sol:generateContent` | 通过 | 第二轮 HTTP 400；流式工具契约失败 | 4K/8K/16K 阶梯均明确零；交换直连/网关顺序后仍为零 |

缓存阶梯中的最大 Gemini 实际输入为 16,709/16,710 tokens，估算长度未冒充实际 token 数。Chat/Responses 带缓存键与 `24h` 保留期时，两条路径都观察到 7,296 个读取 token；Anthropic 系统块和工具断点携带 `1h` 时，两条路径观察到 7,424。相同组内重复请求及只修改末尾问题均保留出站前缀；重复请求字节稳定。不保证每次重复都命中，有已观察到的零值；不据此猜测站点路由或底层供应商。

Anthropic 返回 `input_tokens=4421`、`cache_read_input_tokens=4224`，同时附加 `billing_usage.semantic="openai"` 及 `prompt_tokens=4421`。标准 Anthropic 归一化产生 8,645；不能将此当作已经证明的实际提示输入量，也不能全局改写 Anthropic 输入语义。原始字段和计费元数据的口径需要站点澄清；如确定为另一口径，应在该站点的自定义定义中显式映射。缓存读取 4,224 本身在三层计数中一致。

四入口 × 四目标矩阵保留了真实失败：站点返回的未知原生扩展没有跨协议映射，多数组合明确拒绝转换，未静默丢弃。任意 ID 的四种预置副本 JSON/SSE 文本全部通过。从零定义的 `verification-envelope` 在纯文本/usage 组合离线通过后，Responses、Anthropic、Gemini 的非流式真实转发通过；Chat 返回超出声明的内容，四种 SSE 返回未映射扩展，均保留阻断诊断。早期自定义样例缺少纯文本请求、路径模型上下文或输出限制导致的组合失败属于测试设施问题，不能作为生产转换回归。

声明 `cache.breakpoints` 的 Chat 副本实际将系统块 `cache_control={type:ephemeral,ttl:1h}` 送到 Anthropic，上游读取曾为 8,320；下游因 `/wire:claude` 未映射字段拒绝转换。这只证明请求侧及上游读取，不能算完整端到端通过。Gemini 无已验证的显式缓存资源创建/引用条件；TTL 到期对照未执行，两项继续列为缺口。

确认并修复了两项生产兼容缺陷：Chat `tool_calls:null` 现在解释为没有调用，同时保留原生 null；非法非数组仍拒绝。Gemini `finishReason:null` 不再提前终止流，实际 STOP 及尾帧 usage 得以保留。测试在 `5754293` 隔离快照修前失败、修后通过，证据在 `c20-final-checks/before-regressions.log`。编译器版本升为 `2.0.0-dev.10`，由现有启动重验机制更新定义证据。

另一个 Responses 加密推理校验失败源于测试检查器缺少账号作用域；生产转发本来已传入作用域。修正检查器后真实实验通过，并增加了有来源载荷的本地回归。Gemini 工具第二轮请求中的调用与结果 ID 一致，但上游报 `No tool output found for function call call_1`；流式随后返回空 name 和 `args.arguments` 字符串片段，没有已声明的关联规则。保留原始证据及拒绝回归，不猜测合并、不替换成空参数；仍未证明 Gemini 两轮工具正向可用。

关键原始证据位于仓库父目录 `.cache`：

- `protocol-live-1791030580289`：四端点预检及缓存阶梯、四层计数。
- `protocol-live-1791030876155`：入口与目标矩阵，含明确拒绝的组合。
- `protocol-live-1791031683055`：修正工具提示词后的工具、任意 ID 副本、缓存策略。
- `protocol-live-1791032315909`：带作用域的 Responses 缓存键/保留期复验。
- `protocol-live-1791032521055`：Gemini 缓存执行顺序交换。
- `protocol-live-1791032813311`：修正离线覆盖后的真实自定义入口。
- `protocol-live-1791032933161`：Chat 声明断点到 Anthropic。

完整后端回归与 vet 记录在 `c20-final-checks/report.json`；结果以步骤状态为准。HTTP 200、部分矩阵和本地模拟均不能替代尚未通过的真实正向验证。

## C21：可复现性能基线

命令 `node scripts/verify-protocol.mjs performance --legacy-c01 --revision=d77aac7` 测量旧内核可等价处理的缓存请求；`performance --revision=5d43b83` 在隔离源码快照执行 C18 微基准和端到端负载。快照仅覆盖测试设施，清单记录每个覆盖文件哈希和实际编译器版本。当前目录可直接执行 `performance`；`--micro-only` 仅运行微基准。微基准每项 10 轮、每轮 300ms、无剖析；比较脚本采用固定种子 10,000 次 bootstrap 中位数比值区间。

本次主机 Ryzen 5 7600X、Go 1.26.5、Windows amd64，GOMAXPROCS 为 Go 默认 12。没有并行运行 race、其他测试或真实请求。C01 缓存转换中位数 **17.871 µs / 17,997 B / 322 allocations**；C18 为 **82.588 µs / 53,856 B / 752 allocations**，CPU 比为 **4.62**（95% 区间 4.01–4.82），绝对增加 **64.717 µs**。这次同机采样替代先前约三倍的粗略值；不同时间采样不能直接混算。

端到端负载覆盖四路径、五种内容、即时/10ms 延迟上游、并发 1/8/32，共 120 组合 × 3 轮，每轮 128 请求；每组合另有 32 次预热。所有 **46,080 次计时请求零失败**。测量含真实回环 HTTP、鉴权、路由、转换及 SQLite usage 结算。CPU 是网关、负载发生器和本地上游所在进程总成本；不能当独立生产网关 CPU。首帧记录完整 SSE 帧，非流式则记录完整响应正文；保留内存记录 GC 后差值，不以单次负差值宣称没有泄漏。

| C18 场景（三轮中位数） | 并发 | 请求/秒 | CPU/请求 | P95 |
| --- | ---: | ---: | ---: | ---: |
| Chat 同协议短文本、即时 | 1 | 814.7 | 1.465 ms | 2.001 ms |
| Chat → Anthropic 短文本、即时 | 8 | 907.8 | 1.953 ms | 17.859 ms |
| Chat → Responses 256 KiB 历史、即时 | 8 | 240.3 | 29.175 ms | 37.230 ms |
| 声明式入口 → Chat 32 KiB 前缀、即时 | 8 | 1,020.7 | 4.761 ms | 11.974 ms |
| Chat 同协议 64 KiB 流、延迟分片 | 8 | 239.0 | 10.986 ms | 36.978 ms |
| Chat → Anthropic 短文本、10ms 延迟 | 1 | 84.5 | 2.808 ms | 12.508 ms |

64.7 µs 的转换差额不等于整条网关请求慢 4.62 倍。短文本网关延迟包含调度与 SQLite 结算，长历史则已有明显 CPU 和分配成本：C18 Chat → Anthropic 256 KiB 转换约 15.009 ms、14.53 MB；适合检验资源检查序列化优化。没有业务 SLO，以上数字不能证明生产容量达标。

证据：`protocol-performance-1791033212892`（C01）、`protocol-performance-1791033219174`（C18）、`c21-c01-c18.json`（比较）。每份报告记录命令、参数、源码/测试摘要、真实引擎版本、退出状态及原始样本。三轮端到端数据用于初筛；候选出现可重复超过 5% 的恶化时须补样本定位，不凭单个百分位下结论。
