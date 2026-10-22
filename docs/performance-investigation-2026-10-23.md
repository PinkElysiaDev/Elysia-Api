# 当前版本 CPU / 内存调查复现入口

生产基线固定为 `5ff2b1c8`、编译器 `2.0.0-dev.76`。本轮分支 `research/performance-2026-10-23` 只提交调查工具和文档；不改变转换行为、发布构建或 PGO 配置。大体积证据保留在独立日期目录，不覆盖 2026-10-22 档案。

完整交付：

- [中文报告与详细优化方案](../.tmp-dev/performance-investigation-2026-10-23/REPORT.md)
- [可筛选热力图与趋势面板](../.tmp-dev/performance-investigation-2026-10-23/index.html)
- [五类 pprof 火焰图](../.tmp-dev/performance-investigation-2026-10-23/capture/flamegraphs.html)
- [全部正式结果](../.tmp-dev/performance-investigation-2026-10-23/capture/summary.json)
- [环境及测量身份](../.tmp-dev/performance-investigation-2026-10-23/capture/environment.json)

这些 `.tmp-dev` 链接指向本机档案，不在 Git 中；移动机器需单独备份该日期目录。仓库只保存摘要、复现命令和工具，不能仅克隆仓库就声称原始证据已齐全。

## 已确认的当前成本

主矩阵34组，加上追加的8组identity-after分边界对照，共42个场景分组、210批次、22,700个完整请求，每组五次。正式运行不开profiler、不主动GC；每20ms采样网关内存。CPU含日志结算，延迟以客户端完整消费为终点。

| 合成场景 | CPU ms/请求 | 完整消费 ms | 累计分配 MiB/请求 |
|---|---:|---:|---:|
| 31 MiB 图片，正文日志关闭 | 390.63 | 589.75 | 321.60 |
| 31 MiB 图片，完整正文日志 | 2,171.88 | 2,039.63 | 1,192.45 |
| 31 MiB 图片，媒体外置日志 | 2,015.63 | 2,126.31 | 732.48 |
| 2 MiB 图片，默认转换 | 15.63 | 17.16 | 21.37 |
| 同图片，恒等 after | 171.88 | 128.48 | 764.18 |
| 同图片，JSON 条件/set | 121.09 | 96.36 | 474.60 |
| Responses 客户端→Chat 供应商，1024帧，无概率 | 101.07 | 76.53 | 62.34 |
| 同方向、同帧数，携带 logprobs | 598.63 | 467.68 | 361.43 |

数据是当前版本不同条件的比较，不是新旧优化涨幅。默认 `BodyMaxKB=0` 不保存正文；日志对照夹具使用每段64MiB。默认四客户端的准备活动峰值只有2；关闭准备限流的隔离对照为4，峰值不能混为一种并发条件。详尽范围、采样堆/工作集和热点归因见报告。

追加对照中，31MiB无after请求累计分配321.75MiB、延迟645.88ms；仅decode after为9,082.47MiB/1,949.71ms，仅encode after为7,971.92MiB/1,754.79ms，两侧after为12,815.13MiB/2,367.67ms，采样堆峰值1,691.14MiB。两侧累计分配约39.83倍，峰值约4.74倍，不能混称“同时占用涨40倍”。

源码和profile证明：恒等read本身直接返回原值；`after != nil` 却同时关闭typed、原生基线和首候选编码复用。原生协调的大成本主要在重建基线，第二次完整编码约占2MiB after分配样本42%。TR0MX没有删除after功能；其合并规范化与共享计量的经验支持消除重复工作。

## 下一批优化顺序

| 顺序 | 局部修改 | 不应付出的代价 |
|---|---|---|
| 1 | 解耦恒等after的首候选编码复用资格；再依次接入typed编码、解码和基线复用 | 不把共用workspace门控一次放开而略过真实表达式；不新增执行器 |
| 2 | 日志正则增加必要条件；直接字符串JSON解码；同配置MarshalToString | 不提前截断、关闭正文或削弱脱敏；保留尾部/数字/字符串配置 |
| 3 | 通用规则变化判断改原字节比较，同值省去变更保护遍历 | 不动认证、规则、签名、续传和owner摘要 |
| 4 | 原生比较局部解读、复用同预算规范化事实 | 不删除最终验证，不降低逻辑资源计数 |
| 5 | Collector与目标编码器分步改logprobs增量积累，省私有无usage更新克隆 | 不改变帧顺序/flush/错误时点/Partial与usage契约 |
| 6 | Windows嵌入式CLI关闭控制台专用hook；缓存固定schema模板 | 不共享可变命令树或返回map，不混入生成请求收益 |

恒等单leaf表达式在已验证正节点预算下，步数超限不可达，可省去该计数器；已有相同不可变结果和预算事实也可省重复扫描。实际更严字节限额、模块后取消和最终输出约束仍有作用。逐项证明，不机械保留所有guard，也不机械删除所有“第二次校验”。量化目标、代码位置、差分矩阵和保真要求见完整报告。

## 采集命令

在仓库根目录执行，使用新的空目录，保留所有失败结果。Go由环境提供，本轮为1.27.2；工具固定GOMAXPROCS=4、GOGC=100、PGO关闭，按场景选2GiB或8GiB GOMEMLIMIT。构建、负载、CPU/内存采样、基准及渲染串行执行。

```powershell
$capture = '.tmp-dev/investigation-new/capture'
node scripts/performance-review.mjs prepare --output=$capture --versions=current --current=5ff2b1c8 --date=2026-10-23
node scripts/performance-review.mjs build --output=$capture --versions=current --current=5ff2b1c8
node scripts/performance-review.mjs measure --output=$capture --versions=current --current=5ff2b1c8 --cases=chat-short,anthropic-short,responses-short,gemini-short,chat-anthropic,chat-responses,prefix32k,history256k,tools32,nodes,candidates4,candidates16,burst4,burst32,stream16,stream256,stream1024,stream256-cross,logprobs-responses,logprobs-responses1024,responses-chat-stream256,responses-chat-stream1024,image2,image8,image16,image31,image31-sse,image31-body,image31-media,image2-after,image2-json-rule,image2-candidates16,image31-four --pairs=5 --requests=256
node scripts/performance-review.mjs measure --output=$capture --versions=current --current=5ff2b1c8 --cases=image31-four --pairs=5 --label=unrestricted --preparation=off
node scripts/performance-review.mjs cpu --output=$capture --versions=current --current=5ff2b1c8 --cases=chat-short,image31,nodes,stream256-cross,logprobs-responses1024,image31-body,image31-media,image2-after,image2-json-rule --pairs=3 --seconds=60
node scripts/performance-review.mjs cpu --output=$capture --versions=current --current=5ff2b1c8 --cases=image2 --pairs=3 --seconds=60
node scripts/performance-review.mjs cpu --output=$capture --versions=current --current=5ff2b1c8 --cases=image31-four --pairs=3 --seconds=60 --label=cpu-unrestricted --preparation=off
node scripts/performance-review.mjs memory --output=$capture --versions=current --current=5ff2b1c8 --cases=image2,image31,nodes,stream256-cross,logprobs-responses1024,image31-body,image31-media,image2-after,image2-json-rule --pairs=1 --requests=8
node scripts/performance-review.mjs memory --output=$capture --versions=current --current=5ff2b1c8 --cases=chat-short --pairs=1 --requests=64
```

CPU图每重点场景保留三次原始profile；中间重复用于PNG，不按最好结果挑选。`profile-index` 报样本不足10CPU秒时，在新标签下延长窗口，保留原样本。31MiB四并发启动前检查至少12GiB可用物理内存、16GiB剩余承诺额度；不足记录环境阻塞。

网关、客户端、上游是三个进程。采集二进制来自固定生产树和登记的既有测试适配，仅网关计入资源；它不等同于发布bootstrap的全应用验收。`memory` 故意让模拟上游延迟750ms，不能用于正式延迟结论。

继续复用已构建二进制里的既有夹具，无需再构建一套执行器：

```powershell
node scripts/performance-review-supplement.mjs held --output=$capture --versions=current --reuse-base --pairs=1
node scripts/performance-review-supplement.mjs tools --output=$capture --versions=current --reuse-base --pairs=5
node scripts/performance-review-supplement.mjs scan --output=$capture --versions=current --reuse-base --pairs=5 --benchtime=1s
node scripts/performance-review-supplement.mjs cli-hook --output=$capture --versions=current --reuse-base --pairs=5 --benchtime=1s
node scripts/performance-review.mjs measure --output=$capture --versions=current --current=5ff2b1c8 --cases=image31,image31-body,image31-media --pairs=1 --repeats=10 --label=sustain --idle-ms=1000
```

`held` 为五批四条停滞流的取消/回收诊断，包含主动GC，不放进正常延迟/峰值。组件基准、regex预筛和CLI hook开关实验是解释成本的局部实验，不能直接换算成完整请求收益。

追加after分边界仅扩展已有`*_test.go`夹具；正常`go build`发布不包含这些文件。为保留原二进制身份，在独立后缀下构建相同生产源码，所有387个生产文件均按Git blob校验：

```powershell
node scripts/performance-review-supplement.mjs build --output=$capture --versions=current --suffix=after-study
node scripts/performance-review.mjs measure --output=$capture --versions=current --current=5ff2b1c8 --binary-suffix=-after-study --label=after-study --cases=image2,image2-after,image2-after-decode,image2-after-encode,image31,image31-after,image31-after-decode,image31-after-encode --pairs=5
node scripts/performance-review.mjs memory --output=$capture --versions=current --current=5ff2b1c8 --binary-suffix=-after-study --label=memory-after-study --cases=image2-after-decode,image2-after-encode,image31-after --pairs=1
node scripts/performance-review-supplement.mjs bench --output=$capture --versions=current --suffix=after-study --pairs=5 --benchtime=1s
```

没有编写新转换执行器或线上双跑机制。合成数据覆盖指定回归，不能证明全部转换行为；真实用户历史和渠道仍需独立验证。

## 汇总、可视化和复核

```powershell
node scripts/performance-review-summary.mjs $capture
node scripts/performance-review-components.mjs $capture
node scripts/performance-review-retention.mjs $capture
node scripts/performance-review-profile-index.mjs $capture
node scripts/performance-review-evidence.mjs identity $capture
node scripts/performance-review-render.mjs $capture
python scripts/performance-investigation-charts.py .tmp-dev/investigation-new --refresh-profile-tops
node scripts/performance-review-evidence.mjs manifest $capture
```

Python只需调查用环境的matplotlib/numpy，PNG截图用已有Playwright/Chromium，不新增生产依赖。火焰图来自pprof `/ui/flamegraph`；不是调用图。热力图只将叶函数flat样本互斥归类，内存差分如有负值保留原表，不伪装成正占比。

打开任一profile交互查看：

```powershell
node scripts/performance-review-open.mjs $capture cpu-image31-1-current cpu.pprof cpu
node scripts/performance-review-open.mjs $capture memory-image31-0-current alloc-difference.pprof alloc_space
node scripts/performance-review-open.mjs $capture memory-image31-0-current heap-active.pprof inuse_space
```

画廊的协议聚焦图使用过滤后的分母；完整图使用整个网关分母。每张图标明场景、源码、样本类型及阶段。存活堆是采样时分配来源，不足以单独证明泄漏；需要活动请求、队列、生命周期和回收数据共同解释。

本轮没有生产修改，不把调查工具检查写成生产全量验收。后续优化仍需字节差分、诊断/错误/usage/Native/签名保真、取消和候选隔离回归，再运行对应工程检查。真实客户端和真实供应商仍待单独验证。
