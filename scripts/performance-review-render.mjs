import { spawn, execFileSync } from 'node:child_process'
import { readdir, readFile, writeFile, mkdir } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { createServer } from 'node:net'
import { chromium } from 'playwright'

const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const filter = process.argv[3] || ''
const environment = JSON.parse(await readFile(join(root, 'environment.json')))
const galleryTitle = environment.baseline ? `${environment.baseline} → ${environment.target.slice(0, 8)} 火焰图` : `${environment.target.slice(0, 8)} 当前版本五类火焰图`
const text = (argv) => execFileSync('go', ['tool', 'pprof', ...argv], { encoding: 'utf8', windowsHide: true, maxBuffer: 32 << 20, stdio: ['ignore', 'pipe', 'pipe'] })
const escape = value => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;')
async function port() { const s = createServer(); await new Promise(r => s.listen(0, '127.0.0.1', r)); const p = s.address().port; await new Promise(r => s.close(r)); return p }
const figures = []
const browser = await chromium.launch({ headless: true })
try {
  for (const folder of await readdir(join(root, 'runs'))) {
    if (filter && !folder.includes(filter)) continue
    const directory = join(root, 'runs', folder)
    let run
    try { run = JSON.parse(await readFile(join(directory, 'result.json'))) } catch (e) { if (e.code === 'ENOENT') continue; throw e }
    if (!['passed', 'failed'].includes(run.status)) continue
    const files = await readdir(directory)
    // Preserve every CPU profile; render the predetermined middle replicate.
    const profiles = []
    if (files.includes('cpu.pprof') && run.pair === 1) profiles.push(['cpu.pprof', 'cpu'])
    const binary = run.binary || join(root, 'binaries', `${run.version}.test.exe`)
    if (files.includes('alloc-after.pprof')) {
      text(['-proto', '-base', join(directory, 'alloc-before.pprof'), '-output', join(directory, 'alloc-difference.pprof'), binary, join(directory, 'alloc-after.pprof')])
      profiles.push(['alloc-difference.pprof', 'alloc_space'], ['alloc-difference.pprof', 'alloc_objects'])
    }
    for (const file of ['heap-active.pprof', 'heap-drained.pprof', 'heap-held.pprof', 'heap-cancelled.pprof']) if (files.includes(file)) profiles.push([file, 'inuse_space'], [file, 'inuse_objects'])
    for (const [file, sample] of profiles) {
      const profile = join(directory, file)
      const prefix = `${file}-${sample}`
      const top = text(['-top', '-nodecount=60', `-sample_index=${sample}`, binary, profile])
      await writeFile(join(directory, `${prefix}-top.txt`), top)
      try { await writeFile(join(directory, `${prefix}-list.txt`), text(['-list=scanRaw|needsSemanticCompaction|encodeValueContainer|appendLogprobValues|MergeNodeMetadata|cloneSemantic|normalizeDocument|preserveMappedNative|equalNativeValues|forwardGateway|sanitizeBody', `-sample_index=${sample}`, binary, profile])) }
      catch (e) { if (!String(e.stderr).includes('no matches found')) throw e; await writeFile(join(directory, `${prefix}-list.txt`), 'No matching function sampled; this is not a zero-cost proof.\n') }
      const viewerPort = await port()
      const url = `http://127.0.0.1:${viewerPort}`
      const child = spawn('go', ['tool', 'pprof', `-http=127.0.0.1:${viewerPort}`, '-no_browser', '-relative_percentages', `-sample_index=${sample}`, binary, profile], { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
      try {
        await new Promise((resolve, reject) => {
          const timer = setTimeout(() => reject(new Error('pprof viewer startup timed out')), 20000)
          const receive = bytes => { if (String(bytes).includes(url)) { clearTimeout(timer); resolve() } }
          child.stdout.on('data', receive); child.stderr.on('data', receive); child.once('error', reject)
        })
        const page = await browser.newPage({ viewport: { width: 1800, height: 1050 } })
        for (const focus of ['', 'github.com/elysia-api/backend/protocol']) {
          const png = `${prefix}${focus ? '-protocol' : '-full'}.png`
          const focusedTop = focus ? text(['-top', '-nodecount=60', '-relative_percentages', `-focus=${focus}`, `-sample_index=${sample}`, binary, profile]) : top
          const topName = `${prefix}${focus ? '-protocol' : ''}-top.txt`
          if (focus) await writeFile(join(directory, topName), focusedTop)
          await page.goto(`${url}/ui/flamegraph${focus ? `?f=${encodeURIComponent(focus)}` : ''}`, { waitUntil: 'networkidle' })
          await page.locator('#stack-chart').waitFor()
          const measurement = file.startsWith('heap-active') ? 'active observed; GC-lagged allocation origin, not exact live peak' : file.startsWith('heap-drained') ? 'drained + two forced GC cycles' : file.startsWith('heap-cancelled') ? 'after cancel; active count may remain nonzero (see run status)' : file.startsWith('heap-held') ? 'four held streams + diagnostic GC' : file.startsWith('alloc-') ? 'before/after sampled cumulative difference' : `${run.environment?.ELYSIA_FLAME_SECONDS || 60}s CPU profile; predetermined middle replicate`
          const label = `${run.version}@${run.revision} | ${folder} [${run.status}] | ${sample} | ${measurement} | ${focus ? 'protocol-filtered denominator' : 'whole gateway'} | ${focusedTop.match(/Total samples = ([^\n]+)/)?.[1] || focusedTop.match(/Showing nodes accounting for[^\n]+/)?.[0] || ''}`
          await page.evaluate(label => { const n = document.createElement('p'); n.textContent = label; n.style.cssText = 'font:16px sans-serif;background:#eef;padding:12px'; document.body.prepend(n) }, label)
          await page.screenshot({ path: join(directory, png), fullPage: true })
          figures.push({ run: folder, status: run.status, version: run.version, revision: run.revision, sample, focus, measurement, profile: `runs/${folder}/${file}`, png: `runs/${folder}/${png}`, top: `runs/${folder}/${topName}`, binary })
        }
        await page.close()
      } finally { try { execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }) } catch {} }
      console.log(`${folder} ${sample} ${file}`)
    }
  }
} finally { await browser.close() }
await writeFile(join(root, `flamegraphs${filter ? '-' + filter : ''}.json`), JSON.stringify(figures, null, 2))
await writeFile(join(root, 'flamegraphs.html'), `<!doctype html><html lang="zh"><meta charset="utf-8"><title>性能火焰图</title><style>body{font:16px system-ui;margin:2rem;background:#f7f8fc;color:#14213d}img{max-width:100%}section{background:white;padding:1rem;margin:1rem 0}code{overflow-wrap:anywhere}select,input{padding:.6rem}h2{font-size:20px}</style><h1>${escape(galleryTitle)}</h1><p>横轴宽度表示所选指标的样本权重，不是时间顺序。累计分配不等于同时占用；存活堆图显示分配来源，不直接证明引用持有者。协议聚焦图的分母经过过滤。</p><input id="filter" placeholder="按版本、场景、指标过滤" style="width:70%"><p>交互查看：运行下列命令，在 pprof 页面选择 Flame Graph。图像选用预先固定的第 2 次 CPU 采样；所有原始采样独立保留。</p>${figures.map(f => `<section data-search="${escape(JSON.stringify(f))}"><h2>${escape(f.run)} · ${f.sample} · ${f.focus ? '协议层' : '完整网关'}</h2><a href="${f.png}"><img loading="lazy" src="${f.png}"></a><p><a href="${f.top}">top 报告</a> · <a href="${f.profile}">原始 profile</a></p><code>go tool pprof -http=127.0.0.1:0 -sample_index=${f.sample} "${escape(f.binary)}" "${escape(join(root, f.profile))}"</code></section>`).join('')}<script>const control=document.querySelector('#filter');function filter(){const words=control.value.toLowerCase().split(' ').filter(Boolean);for(const section of document.querySelectorAll('section'))section.hidden=!words.every(word=>section.dataset.search.toLowerCase().includes(word))}control.value=new URLSearchParams(location.search).get('filter')||'';control.oninput=filter;filter()</script></html>`)
console.log(`Rendered ${figures.length} figures`)
