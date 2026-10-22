# 2026-10-22 性能审查复现入口

调查对象固定为 `v1.6.2 / 8f50807c → 760b54af`，另比较 `ba6f8563 → 93509b13 → 760b54af`。本轮工具提交不改变被测生产版本。完整报告和大体积证据保存在独立日期目录，仓库只保留简报、入口和采集器。

本轮结论见 [性能审查简报](backend-performance-review-2026-10-22.md)，本地完整报告见 [日期档案](../.tmp-dev/performance-review-2026-10-22/REPORT.md)。后者未进入Git，迁移机器时需独立复制。

从仓库根目录执行，使用一个**新的空目录**作为 `--output`，不要覆盖已有源码快照、二进制或失败结果。默认目录为 `.tmp-dev/performance-review-2026-10-22`；未推送的本地档案需要单独备份。

```powershell
node scripts/performance-review.mjs prepare --output=.tmp-dev/review-new --versions=release,merged,normalized,current
node scripts/performance-review.mjs build --output=.tmp-dev/review-new --versions=release,merged,normalized,current
node scripts/performance-review.mjs measure --output=.tmp-dev/review-new
node scripts/performance-review.mjs cpu --output=.tmp-dev/review-new --cases=chat-short,image2,image31,nodes,stream256-cross,logprobs
node scripts/performance-review.mjs memory --output=.tmp-dev/review-new --cases=chat-short,image2,image31,nodes,stream256-cross,logprobs
```

`prepare` 从 Git 提取固定生产树，只安装单独登记的测试采集器适配。两版保留各自依赖；发布版标准 JSON、中间 Gin 版生产 sonic 标签、当前 Hertz 不互换。构建固定 `-pgo=off`；工具设置 Go GC 和并行度，环境及命令写入档案。Go 版本由环境提供，本轮固定 1.27.2。

采集串行运行，不同时构建、压测、profile 或渲染。正式测量默认五对交替，每轮完整消费响应并验证文本、usage 和上游次数。CPU 默认三次60秒；`profile-index` 标记 CPU 样本不足10秒的运行，需用新标签延长至最多180秒，不能将不足样本隐藏。

四个大请求有物理内存 / 承诺额度守卫。`--preparation=off` 只用于隔离对照；它不修改部署配置。查看 `PeakActive/PeakQueued`，四个客户端不等于四个同时准备的请求。

```powershell
node scripts/performance-review.mjs measure --output=.tmp-dev/review-new --label=unrestricted --preparation=off --cases=image31-four,image31-four-sse
node scripts/performance-review.mjs measure --output=.tmp-dev/review-new --versions=merged,normalized,current --label=latest --cases=chat-short,history256k,image2,stream256-cross
```

工具往返、慢流取消和组件基准使用补充采集器。每次修改采集器后选用新的 `--suffix`，原二进制和身份文件不覆盖。

```powershell
node scripts/performance-review-supplement.mjs build --output=.tmp-dev/review-new --suffix=extra1
node scripts/performance-review-supplement.mjs tools --output=.tmp-dev/review-new --suffix=extra1
node scripts/performance-review-supplement.mjs held --output=.tmp-dev/review-new --suffix=extra1
node scripts/performance-review-supplement.mjs bench --output=.tmp-dev/review-new --suffix=extra1
node scripts/performance-review.mjs measure --output=.tmp-dev/review-new --binary-suffix=-extra1 --label=custom --cases=image2-after,image2-json-rule
```

`held` 主动 GC，仅回答保留关系，不进入正式峰值 / 延迟计算。`--repeats=10 --pairs=1` 为同一进程的持续回收诊断，不等于十个独立配对。`cpu --trace=1 --seconds=5 --pairs=1` 采集短时 trace；它不是 CPU profile，另用 `performance-review-trace.mjs` 分析等待。

汇总和可视化依次执行：

```powershell
node scripts/performance-review-summary.mjs .tmp-dev/review-new
node scripts/performance-review-components.mjs .tmp-dev/review-new
node scripts/performance-review-regressions.mjs .tmp-dev/review-new
node scripts/performance-review-retention.mjs .tmp-dev/review-new
node scripts/performance-review-profile-index.mjs .tmp-dev/review-new
node scripts/performance-review-hotspots.mjs .tmp-dev/review-new
node scripts/performance-review-trace.mjs .tmp-dev/review-new
node scripts/performance-review-render.mjs .tmp-dev/review-new
python scripts/performance-review-charts.py .tmp-dev/review-new
node scripts/performance-review-dashboard.mjs .tmp-dev/review-new
node scripts/performance-review-evidence.mjs historical .tmp-dev/review-new
node scripts/performance-review-evidence.mjs identity .tmp-dev/review-new
node scripts/performance-review-evidence.mjs inventory .tmp-dev/review-new
node scripts/performance-review-evidence.mjs manifest .tmp-dev/review-new
```

PNG 渲染需要本地 Playwright / Chromium；趋势图需要独立 Python 环境中的 matplotlib / numpy，不能为此修改生产依赖。火焰图渲染使用 Go pprof 的 `/ui/flamegraph`，不是 `-svg` 调用图。CPU 图片固定选第二次样本，全部原始 profile 保留。可交互放大某份 profile：

```powershell
node scripts/performance-review-open.mjs .tmp-dev/review-new cpu-image31-1-current cpu.pprof cpu
node scripts/performance-review-open.mjs .tmp-dev/review-new memory-image31-0-current alloc-difference.pprof alloc_space
```

`performance-review-ledger.mjs` 另建一次性插桩源码，统计逻辑跨度和物化字节；这些计数不是硬件访存字节，也不是正式耗时。`performance-review-checks.mjs` 在采样完成后运行语法、快照边界、vet 和 Go 测试并保留失败。真实供应商入口未启用，费用测试不在本轮范围。

```powershell
node scripts/performance-review-ledger.mjs .tmp-dev/review-new/ledger --run
node scripts/performance-review-checks.mjs .tmp-dev/review-new
node scripts/performance-review-visual-check.mjs .tmp-dev/review-new
```

`visual-check` 校验总览筛选、版本切换、画廊过滤、图片和本地证据链接。需先生成报告、图表和清单；它自身产生检查记录后再生成一次最终 manifest。`ledger --run` 在独立源码中编译插桩测试并分别统计 2 / 31 MiB；失败保留原目录，再试时换新目录，不能覆盖失败证据。

`checks` 遇到非空检查目录会拒绝覆盖，任一检查失败返回非零退出码；再次检查用新的输出目录，并在报告链接对应的结果。报告汇总与图表是派生产物，可以从原始档案重新生成，不能将其生成成功当成原始测试通过。

读取数据时先看 `result.json` 状态，再看原始测量。`comparison.md` 保留每版本全部范围；`summary.json` 同时保存中位数比值与逐对百分比。不要把不同场景、历史机器或限流配置的比例相加；历史失败及本轮 Windows 取消问题必须保留。
