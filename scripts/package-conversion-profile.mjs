import { readdir, readFile, writeFile, copyFile, mkdir, stat } from 'node:fs/promises'
import { resolve, join, dirname, basename } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'

// Export only this synthetic investigation's evidence; never include binaries,
// databases, request logs from real audits, or arbitrary workspace files.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = join(root, '.tmp-dev/conversion-flames')
const destination = join(root, 'docs/artifacts/conversion-flamegraphs-dev72')
const digest = data => createHash('sha256').update(data).digest('hex')
const representative = new Set(['cpu-image2-1', 'cpu-image31-2', 'cpu-nodes-1', 'cpu-events-1', 'historical', 'final-memory-image2-1', 'final-memory-nodes-1', 'final-memory-events-1', 'phases'])
const manifest = { baseline: '31ea3a9f618dbe282b5e2b02960d751973cd0890', compiler: '2.0.0-dev.72', created: new Date().toISOString(), binaries: {}, profiles: [], files: [] }
await mkdir(destination, { recursive: true })
// The repository defaults to text normalization. Preserve both compressed
// profiles and source logs byte-for-byte so checkout retains their SHA256.
const attributes = '# Generated investigation evidence: preserve exact bytes.\n* -text\n*.pprof binary\n*.png binary\n'
await writeFile(join(destination, '.gitattributes'), attributes)
manifest.files.push({ path: '.gitattributes', bytes: Buffer.byteLength(attributes), sha256: digest(attributes) })
async function copy(relative) {
  const exported = basename(relative).length > 100 ? `archive-${digest(relative).slice(0, 20)}${relative.slice(relative.lastIndexOf('.'))}` : relative
  const from = join(source, relative), to = join(destination, exported)
  await mkdir(dirname(to), { recursive: true })
  await copyFile(from, to)
  const data = await readFile(to)
  manifest.files.push({ path: exported, sourcePath: relative, bytes: data.length, sha256: digest(data) })
}
for (const name of ['server.test.exe', 'server-matrix.test.exe', 'server-timing.test.exe', 'server-verification.test.exe', 'builtin.test.exe', 'protocol.test.exe', 'historical/builtin.test.exe']) {
  try {
    const data = await readFile(join(source, name)), metadata = await stat(join(source, name))
    manifest.binaries[name] = { localPath: `.tmp-dev/conversion-flames/${name}`, sha256: digest(data), bytes: data.length, lastModified: metadata.mtime.toISOString(), build: 'go test -c -pgo=off', distributed: false }
  } catch (error) { if (error.code !== 'ENOENT') throw error }
}
const topFiles = /^(?:summary|ledger-.*|report-.*|initial-blanket-memory-gate|matrix-memory-blocked-.*)\.json$|^(?:clone|events-cgocall)-traces\.txt$/
for (const entry of await readdir(source, { withFileTypes: true })) {
  if (entry.isFile()) { if (topFiles.test(entry.name)) await copy(entry.name); continue }
  const selected = /^(?:cpu-|final-memory-|final-measure-|timing-measure-|verified-measure-|execution-)/.test(entry.name) || ['historical', 'phases'].includes(entry.name)
  if (!selected) continue
  for (const file of await readdir(join(source, entry.name))) {
    if (!/\.(?:pprof|txt|json|log|png)$/.test(file)) continue
    if (file.endsWith('.png') && !representative.has(entry.name)) continue
    await copy(`${entry.name}/${file}`)
  }
}
for (const item of JSON.parse(await readFile(join(source, 'flamegraphs.json'), 'utf8'))) {
  if (!representative.has(item.scenario)) continue
  manifest.profiles.push({ ...item, binary: item.scenario === 'historical' ? 'historical/builtin.test.exe' : basename(item.binary) })
}
const ledgerInfo = await readFile(join(root, '.tmp-dev/conversion-ledger/instrumentation.json'))
await writeFile(join(destination, 'ledger-instrumentation.json'), ledgerInfo)
manifest.files.push({ path: 'ledger-instrumentation.json', bytes: ledgerInfo.length, sha256: digest(ledgerInfo) })
await writeFile(join(destination, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
const html = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>转换链路火焰图 · dev.72</title>
<style>body{font:16px/1.65 system-ui,sans-serif;max-width:1500px;margin:30px auto;padding:0 24px;color:#17252f}img{max-width:100%;border:1px solid #dce3eb}code{background:#eef2f6;padding:2px 5px}nav a{margin-right:18px}details{margin:20px 0}summary{cursor:pointer;font-weight:bold}small{color:#536272}</style>
<h1>转换链路火焰图 · dev.72</h1><p>生产代码基线 <code>31ea3a9</code>；合成数据、本地模拟上游，PGO 关闭。CPU 四场景各 3 × 60 秒；此页选择代表图，全部原始采样见目录。</p>
<p>宽度是采样权重，横向位置不是时间顺序。CPU 单位为秒；alloc_space 是累计分配；inuse_space 是采样时仍存活对象的分配位置，不能用它独自证明持有者或泄漏。过滤图的分母可能改变。</p>
<p>历史短采样及主动 GC 的阶段快照均另有标注。heap-active 在请求开始约 250 毫秒时请求采样，小事件可能已经结束；它不是精确峰值。</p>
<nav><a href="../../audit-conversion-flamegraphs-dev72.md">调查报告</a><a href="../../audit-conversion-next-optimization-dev72.md">下一批计划</a><a href="summary.json">完整统计</a><a href="manifest.json">SHA256 / 二进制信息</a></nav>
<p>交互查看：在仓库根目录执行 <code>go tool pprof -http=127.0.0.1:8081 -no_browser -sample_index=cpu docs/artifacts/conversion-flamegraphs-dev72/cpu-image31-2/cpu.pprof</code>，浏览器打开 <code>http://127.0.0.1:8081/ui/flamegraph</code>。内存图改为 <code>alloc_space</code> 或 <code>inuse_space</code>；源代码逐行查看还需匹配二进制与源码。</p>
${manifest.profiles.map(item => `<details><summary>${item.scenario} / ${item.profile.split('/').at(-1)} / ${item.sample} / ${item.focus ? 'protocol 聚焦' : '完整视图'}</summary><p><a href="${item.profile}">原始 profile</a> · <a href="${item.profile}.top.txt">top</a> · <a href="${item.profile}.list.txt">list</a> · <a href="${item.png}">PNG</a></p><a href="${item.png}"><img loading="lazy" src="${item.png}" alt="${item.scenario} ${item.sample} flame graph"></a></details>`).join('\n')}</html>`
await writeFile(join(destination, 'index.html'), html)
console.log(JSON.stringify({ destination, profiles: manifest.profiles.length, files: manifest.files.length, bytes: manifest.files.reduce((sum, file) => sum + file.bytes, 0) }))
