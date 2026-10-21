# 后端性能基准

[English](backend-performance.en.md) · [Hertz 迁移及性能简报](backend-hertz-migration.md)

使用 Node.js、Go 工具链和本地回环网络运行，无需真实供应商凭据。
仓库保留简报和可复用工具；原始测量、profile 和源码快照写入指定输出目录，
不提交到源码树。旧版算法优化报告可从 [Git 历史](https://github.com/PinkElysiaDev/Elysia-Api/tree/ba6f856/docs) 查询。

```sh
# 完整采集：微基准、HTTP 矩阵、独立 profile、取消诊断。
node scripts/benchmark-performance.mjs --output=.tmp-dev/performance-baseline

# 常用 HTTP 路径；输出目录必须新建且为空。
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,burst4,stream256,stream256-cross,image2 \
  --requests=128 --repeats=3 --idle-ms=1000 \
  --output=.tmp-dev/performance-measure

# CPU 与分配归因独立采集，避免影响正式计数。
node scripts/benchmark-performance.mjs --mode=profile \
  --profiles=chat-short,stream256-cross,image2 --seconds=15 \
  --output=.tmp-dev/performance-profiles
```

支持 `--source=/path/to/worktree` 指定对照源码。Hertz 默认使用 Sonic；
采集器为 Gin 自动添加 `-tags=sonic`。所有构建关闭 PGO，默认
`GOMAXPROCS=4`、`GOGC=100`、`GOMEMLIMIT=2GiB`，可用环境变量覆盖。

## 场景与口径

- 短请求：`chat-short`、`anthropic-short`、`responses-short`、`gemini-short`。
- 跨协议/复杂请求：`chat-anthropic`、`chat-responses`、`prefix32k`、`history256k`、`tools32`、`candidates4`、`reload-chat`。
- 并发：`burst4`、`burst4-body`；流式：`stream16`、`stream256`、`stream1024`、`stream256-cross`、`stream256-burst4`。
- 图片：`image2`、`image2-media`。图片固定每轮四请求，重载固定一请求，不受 `--requests` 覆盖。
- 客户端、网关、假上游相互隔离；分配、进程 CPU、GC、Heap/RSS 只计网关，包含请求生命周期、鉴权和异步 SQLite 用量写入。
- 正式计数等待用量写入完成，HTTP 延迟只计到消费响应结束。`off` 关闭正文捕获，不关闭用量落库。闭环吞吐不等于最大服务器容量。
- 冷请求单列，正式测量不强制 GC。峰值每 20ms 采样，可能漏掉短尖峰；HeapAlloc、累计分配和 RSS 不能互相替代。
- 对照在同一机器、相同参数下串行 ABBA 执行，不并行压测或构建。保留相反结果，不把少量样本的尾延迟或微小波动当作普适收益。

## 证据

输出包含环境/命令/二进制 SHA256 的 `report.json`、含未跟踪文件的
`source/backend/` 源码快照及 `source-manifest.json`、逐轮和逐请求
`measurements.json`、恢复时间线、profile、top 归因及校验清单。
`summary.md` 汇总分配、CPU、时延及内存。profile 使用独立诊断运行，
分配 profile 是采样结果，不是精确逐对象账本。

```sh
go tool pprof -top -sample_index=alloc_objects \
  -base .tmp-dev/performance-profiles/memory-stream256-cross/alloc-before.pprof \
  .tmp-dev/performance-profiles/memory-stream256-cross/alloc-after.pprof
```
