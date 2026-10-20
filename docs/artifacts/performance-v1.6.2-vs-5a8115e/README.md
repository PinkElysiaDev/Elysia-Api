# v1.6.2 → 5a8115e profiling 持久归档

[分析报告](../../backend-performance-comparison-v1.6.2.md) · [汇总 JSON](../performance-v1.6.2-vs-5a8115e.json) · [火焰图册](index.html) · [完整对比表](comparison/comparison.md)

本归档保存 2026-10-20 的同机对照：25 个场景、每版 5,615 次正式请求、12 份 CPU profile、24 张火焰图，以及 memory profile、逐请求记录、恢复时间线、微基准、日志和采集器源码。只保存这次新采集，不恢复已被上游移除的旧 dev72 归档。

- **baseline**：tag `v1.6.2`，`8f50807c295f9d6f2669d79794e58c89188722b3`，原依赖、标准 JSON 构建，加测试专用采集适配。
- **current**：`5a8115e7008e1c9f6035629bbeb58ad119bc2a43`，Sonic 构建。
- 迁入默认分支时上游为 `8e3c62a828be4da2f433ac13afe2029ca360b08e`，它已合入 `5a8115e`，两者源码树一致。**归档不会将历史采样标成对未来默认分支提交的测量。**

## 目录

| 目录／文件 | 内容 |
| --- | --- |
| `baseline/`、`current/` | 两版主矩阵、chat-short／stream256／image2 的独立 CPU 与 memory profile、微基准、取消诊断 |
| `baseline-legacy/`、`current-legacy/` | nodes／events／image31 补充负载 |
| `comparison/` | 完整比较 JSON、top 文本、原始图册与采集／汇总脚本 |
| `index.html`、`gallery.json` | 使用归档相对路径和免二进制 pprof 命令的可迁移图册 |
| `archive.json` | 源版本、原始 manifest 指纹、目录映射和未纳入 Git 的二进制指纹 |
| `summary.original.json` | 迁移前汇总的原始字节；公开汇总已改用归档路径 |
| `manifest.json`、`verify.mjs` | 全归档校验清单及校验程序 |

五组原始证据及其 manifest **逐字节保留**，包括原图册中的历史绝对路径、原始报告中的命令和采集环境。它们是不可变的出处记录，不应被当作迁移后的运行路径。使用本目录的 `index.html` 与下面的命令查看。原采集／渲染脚本保留历史默认路径，不是会自动定位本归档的维护工具。

两份 `server.test` 合计约 105 MiB，不纳入 Git；它们仍留在原 `.tmp-dev` 路径，大小、SHA256 和路径记录在 `archive.json`。profile 内嵌函数与位置符号，不依赖这两份本地二进制即可查看 CPU／分配排名和交互火焰图；精确反汇编仍需原匹配二进制，源码行级复核还需要对应版本源码和旧版采集适配。

## 查看与校验

从仓库根目录执行，无需启动网关、模拟上游或重新压测：

```sh
# 校验全部原始证据与完整归档；也可从其他工作目录执行同一脚本。
node docs/artifacts/performance-v1.6.2-vs-5a8115e/verify.mjs

# 当前版本 31 MiB 图片 CPU：只读 profile，不需要原 server.test。
go tool pprof -http=127.0.0.1:0 \
  docs/artifacts/performance-v1.6.2-vs-5a8115e/current-legacy/cpu-image31/cpu.pprof

# 当前版本 2 MiB 图片累计分配差分。
go tool pprof -http=127.0.0.1:0 -sample_index=alloc_space \
  -base docs/artifacts/performance-v1.6.2-vs-5a8115e/current/memory-image2/alloc-before.pprof \
  docs/artifacts/performance-v1.6.2-vs-5a8115e/current/memory-image2/alloc-after.pprof
```

`node …/verify.mjs --index` 另外检查 Git 暂存区的归档字节，防止压缩 `.pprof` 被换行规范化破坏。`--seal` 只用于生成迁移包装文件的校验清单，不会跳过 357 个原始证据文件与原 manifest 指纹校验，不能用来接受被修改的原始采样。

归档数据全部为合成本地夹具，不含生产数据库或真实供应商凭据。CPU 的累计子树可能嵌套；图宽不是墙钟时间；分配图不是峰值堆分解。统计口径、环境差异、内存回退与未覆盖项见分析报告。
