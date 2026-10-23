# 2026-10-23 性能优化实施结果

本批已实现精确 identity `after` 请求快路、原生比较与计量复用、logprobs 增量累积、日志处理、私有 usage 状态及 CLI/schema 优化。保留转换规则、资源预算、来源保护、最终输出校验、准备并发和 flush 行为；没有线上双执行器、PGO 或新增配置。

生产基线 `5ff2b1c8`，固定测试对照 `251bc5d2`（生产代码相同），最终生产修订 `a469e6e7`，编译器保持 **2.0.0-dev.76**。分支 `perf/identity-after-and-hotspots-2026-10-23` 按根因拆分本地提交；未推送、未调用真实供应商。`6801ba23` 及后续提交仅补充采集场景和文档。

## 实际测量

Windows / Ryzen 5 7600X / Go 1.27.2，GOMAXPROCS=4、GOGC=100，普通 GOMEMLIMIT=2GiB、大正文 8GiB。客户端、网关和模拟供应商独立进程，仅采集网关。43 个完成五对的分组（38 个主场景、4 个独立确认、1 个不限准备并发的对照），共 430 批、66,140 个完整请求。正式计时没有 profiler 或主动 GC，上游次数均核对通过。

以下为版本中位数；MiB=2²⁰。累计分配是过程申请总量，采样堆峰值是每 20 ms 观察到的最大值，二者不能互换。延迟包含完整响应消费，CPU 另含日志结算。

| 场景 | 分配 MiB/请求，旧→新 | 完整消费 ms，旧→新 | 采样堆峰值 MiB，旧→新 |
|---|---:|---:|---:|
| 31 MiB，双侧 identity after，JSON | 12,815.03 → 259.70（−98.0%） | 2,410.54 → 619.58（−74.3%） | 1,676.43 → 232.50（−86.1%） |
| 同场景 SSE | 12,846.27 → 259.72（−98.0%） | 2,433.67 → 621.72（−74.5%） | 1,590.70 → 232.78（−85.4%） |
| 31 MiB，普通 JSON | 321.74 → 259.60（−19.3%） | 700.13 → 601.43（−14.1%） | 356.63 → 263.62（−26.1%） |
| 1024 帧 logprobs，Responses→Chat | 362.52 → 85.82（−76.3%） | 519.54 → 146.19（−71.9%） | 22.36 → 22.89（+2.3%） |
| 31 MiB，保存正文日志 | 1,192.41 → 867.13（−27.3%） | 2,141.69 → 712.91（−66.7%） | 391.50 → 391.22（基本持平） |
| 31 MiB，媒体外置日志 | 732.46 → 406.65（−44.5%） | 2,101.12 → 635.96（−69.7%） | **334.44 → 403.19（+20.6%）** |

31 MiB 四客户端默认最多两个同时准备，峰值 614.70→501.44 MiB；夹具关闭准备限流、实际四个同时准备时为 966.06→749.27 MiB。两种口径分开，生产限流没有改变。

## 回退与未完成项

- 媒体日志的峰值五对均升高，属于**未解决回退**；通用 JSON 规则、1800 节点的峰值也有增加。降低累计分配不等于这些问题已解决。
- 普通跨协议 256 帧分配下降 9.3%，未达到原定 10% 目标；普通 1024 帧基本不变。1800 节点对象数只下降约 0.069%，未达到 5% 目标。
- 小批量 CPU、16/31 MiB SSE 延迟曾出现超过 10% 的回退；独立确认没有稳定重现。初组与确认组均保留，不选择最好结果。
- 十轮持续运行的轮末活动请求和日志队列归零，未见短期持续增长。五批四并发停滞流取消后，**主动 GC** 堆回到约 3.4 MiB；这不是自然回收速度或长时间无泄漏证明。

初版日志 `MarshalToString` 延长了过大编码缓冲的存活期，已撤回，统一使用精确长度的日志字符串。原生比较器的小片段复制和缺少短字段预检也曾导致回退，已纠正；初版证据仍保留。

## 验证状态

字节/错误差分、定向回归、原生比较和计量 fuzz、协议与相关网关 race 均通过。测试代码仅在测试中执行，不在真实请求上增加一轮转换。合成内容覆盖已知结构和拒绝边界，不宣称能证明所有未来输入等价。

| 检查 | 结果 |
|---|---|
| Go 全量 | 3398 个测试/子测试通过、1 失败、13 跳过；未全绿 |
| 唯一失败 | 既有 `TestA2ARegistryEvictionAndPagination`，相同时间戳任务的淘汰顺序；相关代码与基线相同 |
| vet | 通过 |
| staticcheck v0.8.2 | 45 条既有诊断，新增 0、移除 1；退出状态仍失败 |
| 协议/相关服务 race | 通过 |
| 审计自测 | 161 项通过 |
| 前端类型、lint、本地后端页面 | 通过；页面 14 项 |
| WebUI、六目标构建 | 通过 |

最终六目标二进制在 [dist/standalone](../dist/standalone/)，[大小、SHA256 和构建信息](../.tmp-dev/performance-optimization-2026-10-23/final-release-binaries.json)可核验。修改前的六目标二进制也已保留。Windows arm64/Linux/macOS 仅交叉编译通过，不是运行或性能验收；原用户完整长对话、真实客户端和真实供应商待验证。

## 完整证据与后续方向

- [中文完整报告、提交台账和下一批详细方案](../.tmp-dev/performance-optimization-2026-10-23/REPORT.md)
- [86 张 CPU／内存火焰图与交互启动命令](../.tmp-dev/performance-optimization-2026-10-23/final/flamegraphs.html)
- [热力图与十组比较图，PNG/SVG/CSV](../.tmp-dev/performance-optimization-2026-10-23/plots/final/index.html)
- [全部配对数据](../.tmp-dev/performance-optimization-2026-10-23/comparison-final.json)、[工程检查](../.tmp-dev/performance-optimization-2026-10-23/final-checks/results.json)、[持续运行与取消](../.tmp-dev/performance-optimization-2026-10-23/final/retention.md)
- [原始调查背景](performance-investigation-2026-10-23.md)

最终三类重点场景各有三次 60 秒 CPU profile，均超过 10 秒 CPU 样本。火焰图只用于定位：父子累计成本不能相加，活动堆图不等于峰值；未过滤 `alloc_objects` 有明显 pprof 自身分配污染，不能直接当作业务对象热点。

后续优先减少计量/规范化的重复扫描，定位媒体日志早期峰值，减少通用规则只读来源检查的片段复制，再研究受限正文读取、日志整记录副本和逐帧小对象。每项在完整报告中列明代码、证据、方案、保真边界与验收；不删除不同信任边界的有效检查，不改变持久性或即时 flush 来获取收益。

这些 `.tmp-dev` 链接是**独立本机档案，不在 Git 中**，需连同原始 profile 和匹配二进制单独备份。源码/二进制身份校验和目录清单分别保存在各 capture 的 `identity-verification.json` 与根目录 `archive-manifest.json`。

在仓库根目录打开已有交互 CPU 图：

```powershell
node scripts/performance-review-open.mjs .tmp-dev/performance-optimization-2026-10-23/final cpu-image31-after-1-current
```

在保留的固定源码/二进制上重测五对（使用新标签，避免覆盖已交付结果；大请求会消耗较多时间和内存）：

```powershell
node .tmp-dev/performance-optimization-2026-10-23/paired.mjs image31-after rerun-after default final
```

仅重绘已有数据、不运行负载：

```powershell
.tmp-dev/performance-review-2026-10-22/venv/Scripts/python.exe .tmp-dev/performance-optimization-2026-10-23/plot-results.py --input=comparison-final.json
```

重建图册时需保留上述采样污染说明；新测量输出应另建日期目录，不覆盖本轮归档。正式环境参数、命令和失败/跳过原因以原始记录为准。
