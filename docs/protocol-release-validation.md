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
