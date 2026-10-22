# Windows 断连与 Responses 历史请求修复记录

## 范围与证据

本批基线 `a31f81ce`，修复分支 `fix/windows-disconnect-responses-history`。只处理 Windows 客户端断连取消和 Responses 请求历史 `reasoning.content:null`，性能优化仍暂停。没有调用真实供应商，也没有使用、提交私人提示词或加密历史。

修复代码分为两个提交：`1c596bc1`（Windows）和 `449fccbc`（Responses、dev.76）。最终全量检查和六平台产物对应代码提交 `449fccbc`；后续验收记录提交仅修改本文档。

Windows 原错误来自 Hertz v0.10.6 在每个新连接上注册 connstate，而其依赖的 Windows 实现不支持该功能。监听服务没有因此停止，但等待首响应或后续流内容的请求不能及时获知客户端关闭。修改前重新运行 `TestHertzDisconnectCancelsBeforeFirstResponse` 和 `TestMCPCLICancellationIsStateless`，均在原有 3 秒窗口内失败。

用户提供的日志导出是完整的日志 JSON，但内嵌请求正文已经截断。完整可读的 285 个 input 项中，61 个 reasoning 项包含 `content:null`、数组 summary 和加密内容，`/input/40/content` 是其中之一。错误发生在本地请求解码，尚未发往上游。该解码判断在 v1.6.1、v1.6.2 和本批基线均存在，与近期性能优化无关。不能通过补括号或删掉后半段把该导出当成原请求重放。

## Windows 实现及边界

- Windows 关闭 Hertz 不支持的内置 connstate 注册，使用已有 OnAccept 保存的原始 TCP 连接查询 `SIO_TCP_INFO v0`；其他平台维持原机制。
- 每个引擎仅在有活动请求时运行共享观察循环，间隔 100 ms。它不读取或 Peek 请求数据，不改变 socket 的 deadline，不使用 unsafe，也不保存池化 RequestContext。
- 每项观察绑定当前请求的 cancel。由既有 `onHTTPComplete` 注销，覆盖 handler 返回后的流式响应体及 hijack。迟到的旧查询只能涉及旧请求。
- TCP 状态明确表明关闭，以及已关闭、reset、abort、not-connected 错误，触发取消；FIN_WAIT 只证明本端发送 FIN，不作为远端关闭依据。未知查询错误不能被写成客户端取消。
- 无法使用查询接口时，每引擎仅告警一次，保留普通 HTTP 可用性。当前 Windows 11 的回归要求探测实际成功，不能用降级状态冒充通过。
- 薄 transporter 包装分别处理 Close、Shutdown 和异常监听退出。正常 Shutdown 关闭 listener 时仍观察尚未排空的请求，避免把优雅退出变成立即取消。
- Hertz 根据具体 transport 包名处理 `IdleTimeout=0`。包装后必须显式保留 standard 的原 sentinel 行为，已有同连接两请求回归。
- 观察项、生命周期同步和实际 TCP 查询分别组织；测试观测接口仅存在于 `_test.go`，没有增加生产请求的测试字段或公共配置。

本机验证的是 Windows amd64 和 Hertz `WithTLS` 下的底层 TCP 关闭。仅 TLS `close_notify`、TCP 仍为 ESTABLISHED 的情况未验证、也不能由 TCP 状态单独识别。外部自定义 listener 若不暴露原生 socket，会明确降级。Windows arm64/Linux/macOS 的交叉编译不等于对应平台运行验证。

微软接口资料：[SIO_TCP_INFO](https://learn.microsoft.com/en-us/windows/win32/winsock/sio-tcp-info)、[TCP_INFO_v0](https://learn.microsoft.com/en-us/windows/win32/api/mstcpip/ns-mstcpip-tcp_info_v0)、[TCPSTATE](https://learn.microsoft.com/en-us/windows/win32/api/mstcpip/ne-mstcpip-tcpstate)。

## Responses 兼容契约

新增分支仅命中 `DecodeRequest`、`content` 字段、JSON null 这三个条件。它表示没有可见 reasoning parts，保留 `ReasoningContent == nil`，不改成空数组。summary、ID、加密资源、scope 和 Native 均沿用原处理。

同协议兼容/严格模式保留这一无内容损失的历史表示；原生协调及仅修改模型时保留未修改的 `content:null`。显式移除根 Native 后仍按语义重建，可以省略该字段，不从节点快照强行恢复。跨协议仍受原来的加密资源、来源和恢复限制约束。

`summary:null`、content 的其他非数组值及非法 parts 继续失败；JSON 响应和 SSE 事件的 null 仍按原行为拒绝；用户显式数组 schema 和映射限制继续有效。请求侧 reasoning 数组类型错误补充 `decode_request` 阶段，不伪造规则 ID、策略哈希或修改全局错误包装。

官方 Responses 字段说明是可选数组，没有明确声明 content nullable。因此这是针对实际观察到的历史表示的有限兼容，不是“官方标准要求接受 null”。编译器由 `dev.75` 升为 `dev.76`，通过现有机制重验协议与绑定证据；没有新增规则、API、配置或数据库字段。

保真测试还确认了一个既有边界：根容器修改模型并重新编码时，字面 `<>&` 会按原规则 HTML 转义。null 与缺失字段的对照结果相同；预转义内容、数字拼写 `1.00`、大整数和节点 Native 原字节得到验证。本次没有改变该编码行为，不能宣称任意原请求只发生模型位置的字节变化。

## 本地回归

新增测试复用现有测试服务器、预置帧、存储和验证器，没有增加真实渠道重放框架。

- Windows 首响应前及 SSE 停滞各执行四并发五批，共 40 次模拟上游取消，逐批核对处理退出与观察项清空；没有把运行时全局 goroutine 的瞬时变化当作泄漏结论。
- 覆盖上传中断、未读数据、FIN/RST、IPv6、TLS 底层关闭、keep-alive、零 idle timeout、Close/Shutdown/外部 listener 关闭、延后 body/hijack 和完成回调隔离。
- 保留原 MCP 取消后不执行后续命令的契约，以及 A2A 任务可独立于 HTTP 订阅继续运行的契约。
- 实际网关首响应前、合法 SSE 首帧后两种取消路径均验证：模拟上游收到取消，仅结算一条 `499/client_canceled` 日志，零重试、一次上游调用，准备容量及活动请求归零。
- Responses 使用 41 项合成历史，第 40 下标为 null reasoning；兼容/严格 × JSON/SSE 四组均完整执行工具调用和第二轮，保留工具参数大整数和未知字段。
- 每组成功调用只发送一次；非法 `content:{}` 在本地返回 400，上游命中为零。每组落库两个成功、一个失败、零重试，成功计量合计 44 tokens；诊断不含合成加密值。
- 协议层覆盖缺失/null/空数组/合法 parts、非空摘要、非法类型、原生重建、自定义 schema、通用映射、after 和跨协议与缺失字段的对照。

## 工程检查

检查环境为 Windows 11 amd64、Go 1.27.2。相关 race 使用 `CGO_ENABLED=1` 与 `D:\DevTools\msys64\ucrt64\bin\gcc.exe`；常规检查使用 `GOMAXPROCS=4`。原始检查和构建记录保存于 `.tmp-dev/windows-responses-fix-2026-10-23/`。

| 检查 | 结果 |
|---|---|
| Go 全量，`-pgo=off -count=1 -timeout=10m -json ./...` | **3170 通过、1 失败、13 条件跳过**，含子测试；server 包 90.476 秒 |
| `go vet ./...` | 通过 |
| 断连、生命周期、MCP/A2A 相关 race | 18 项顶层测试通过，0 跳过，0 DATA RACE；14.050 秒 |
| 实际网关取消与 null 历史调用 race | 2 项顶层测试及 6 个分支通过，0 DATA RACE；29.646 秒 |
| 审计工具自测 | 161 通过，0 失败／跳过 |
| 源码快照边界自测 | 4 通过 |
| 前端类型检查及 Vite 构建 | 随标准 standalone 构建通过；本批未改前端源码 |
| Windows/Linux/macOS × amd64/arm64 | 六目标构建全部通过；非本机架构仅交叉编译 |
| Windows amd64 产物启动 | 隔离数据库健康检查、11 次新连接健康请求、内嵌 `/ui/` 返回 200；没有 connstate/probe 不支持日志，正常退出 0 |
| staticcheck `v0.6.1` | 工具与当前 Go 导出格式不兼容，未通过 |
| staticcheck `v0.8.2` 补充检查 | 46 条现存诊断，未通过；诊断所在文件相对基线全部未修改 |

全量失败是 `TestA2ARegistryEvictionAndPagination`：多个任务获得相同 `createdAt`，生产淘汰逻辑没有定义并列任务的顺序，而测试固定要求移除 `finished:0000`。它不经过 Hertz/断连观察器，相关 A2A 文件本批未修改。前轮 `.tmp-dev/performance-review-2026-10-22/checks/confirm-failures-2.json` 已记录该测试 20/20 失败；既有诊断显示活动任务存在、容量及 dedup 数量正确，失败的是特定 ID 假设。保留失败，不把它写成整体 Go 全量通过。本批未更改 A2A 生产行为或该测试。

13 项跳过包含 2 项平台条件、2 项需要独立客户端安装的分支，以及 9 项需显式启用的采样／负载／真实渠道测试或子进程入口。详见检查目录的 `go-skips.json`；没有将这些项目计为通过。

仓库历史记录的 staticcheck `v0.6.1` 报错为 `export data version 5 is greater than maximum supported version 2`。固定 `v0.8.2` 补查的完整结果是 8 条 U1000、1 条 ST1019、21 条 SA4006、14 条 SA1012、2 条 SA1019，集中于已有未使用方法、重复导入、生成计量器赋值、Hertz 日志 nil context 和测试 HeaderMap。`staticcheck-summary.json` 记录了诊断及相关文件相对基线未修改的核对结果；没有修改检测配置来屏蔽它们。

### 复核入口

- `.tmp-dev/windows-responses-fix-2026-10-23/checks/go-full.jsonl`：完整 Go 测试事件；`go-full-summary.json` 为统计。
- `checks/windows-race.log`、`checks/gateway-race.log`：两组 race 命令和输出。Windows race 的原始混合编码副本另行保留，供复核。
- `checks/go-vet.log`、`checks/staticcheck.log`、`checks/staticcheck-v0.8.2-final.log`：检查输出，失败记录未覆盖。
- `checks/build-six-targets.log`、`build-results.json`：构建输出、六个产物的 SHA-256、大小及 Go 构建信息。
- `binary-smoke/result.json`：Windows 产物启动、健康接口、页面和正常退出结果。该目录内配置与数据库仅供本地隔离冒烟，未提交。
- `dist/standalone/`：六目标二进制；Windows amd64 的 SHA-256 为 `0b2abc31db7e87c37d9332a9e001743cf88bd199a440e2c49d07f99362dc98a2`。

构建期间生产源码已提交，仅本文档尚在整理；Go 构建信息可能因此显示 `vcs.modified=true`。`build-results.json` 保留原值，不能把它改写为干净构建标识。

## 真实环境状态

本地验证不能证明真实上游接受该历史表示，也不能证明原 Codex 长对话整体通过。真实供应商及原客户端复测仍待完整、未截断请求。默认不进行付费测试；本批仅本地提交，不推送。
