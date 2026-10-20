import { spawn, execFileSync } from 'node:child_process'
import { createServer } from 'node:net'
import { readFile, writeFile, mkdir, readdir } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { chromium } from '@playwright/test'

const [beforeArg, afterArg, beforeExtraArg, afterExtraArg, outputArg] = process.argv.slice(2)
if (!outputArg) throw new Error('Usage: node profiling-render.mjs BEFORE AFTER BEFORE_EXTRA AFTER_EXTRA OUTPUT')
const directories = [beforeArg, afterArg, beforeExtraArg, afterExtraArg].map(d => resolve(d))
const output = resolve(outputArg)
await mkdir(output, { recursive: true })
const reports = await Promise.all(directories.map(async d => JSON.parse(await readFile(join(d, 'report.json')))))
if (reports.some(r => r.status !== 'passed')) throw new Error('Only render completed evidence; do not overlap with measurement')
const html = s => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;')
const browser = await chromium.launch({ headless: true })
const gallery = []
async function port() {
  const server = createServer()
  await new Promise((res, rej) => { server.once('error', rej); server.listen(0, '127.0.0.1', res) })
  const value = server.address().port
  await new Promise(res => server.close(res))
  return value
}
try {
  for (const [index, dir] of directories.entries()) {
    const version = index % 2 ? 'after' : 'before'
    const binary = join(directories[index % 2], 'server.test')
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      if (!entry.isDirectory() || !/^(cpu|memory)-/.test(entry.name)) continue
      const isCPU = entry.name.startsWith('cpu-'), folder = join(dir, entry.name)
      const sample = isCPU ? 'cpu' : 'alloc_space'
      const args = ['tool', 'pprof', '-no_browser', `-http=127.0.0.1:${await port()}`, `-sample_index=${sample}`]
      if (!isCPU) args.push('-base', join(folder, 'alloc-before.pprof'))
      args.push(binary, join(folder, isCPU ? 'cpu.pprof' : 'alloc-after.pprof'))
      const child = spawn('go', args, { stdio: ['ignore', 'pipe', 'pipe'] })
      try {
        let text = ''
        const url = await new Promise((res, rej) => {
          const timeout = setTimeout(() => rej(new Error(`pprof startup timeout: ${text}`)), 20000)
          const receive = chunk => { text += chunk; const m = text.match(/http:\/\/127\.0\.0\.1:\d+/); if (m) { clearTimeout(timeout); res(m[0]) } }
          child.stdout.on('data', receive); child.stderr.on('data', receive)
          child.once('error', e => { clearTimeout(timeout); rej(e) })
          child.once('exit', code => { clearTimeout(timeout); rej(new Error(`pprof exit ${code}: ${text}`)) })
        })
        const page = await browser.newPage({ viewport: { width: 1800, height: 1050 }, deviceScaleFactor: 1 })
        await page.goto(`${url}/ui/flamegraph`, { waitUntil: 'networkidle' })
        await page.locator('#stack-chart').waitFor()
        const label = `${version} · ${reports[index].commit.slice(0, 7)} · ${entry.name} · ${sample} · whole gateway (unfiltered)`
        await page.evaluate(label => {
          const banner = document.createElement('div'); banner.textContent = label
          banner.style.cssText = 'padding:12px;background:#eef;color:#112;font:18px sans-serif'
          document.body.prepend(banner)
        }, label)
        const png = `${version}-${entry.name}.png`
        await page.screenshot({ path: join(output, png), fullPage: true })
        gallery.push({ label, png, binary, args: args.slice(0, 2).concat(args.slice(4)), profileDirectory: folder })
        await page.close()
      } finally {
        if (child.exitCode === null) {
          const exited = new Promise(res => child.once('close', res))
          child.kill('SIGTERM'); await exited
        }
      }
    }
  }
} finally { await browser.close() }
await writeFile(join(output, 'gallery.json'), JSON.stringify(gallery, null, 2) + '\n')
await writeFile(join(output, 'index.html'), `<!doctype html><meta charset="utf-8"><title>v1.6.2 profiling comparison</title><style>body{font:16px sans-serif;margin:2em}img{max-width:100%}pre{white-space:pre-wrap}</style><h1>v1.6.2 → current profiling comparison</h1><p>Flamegraph width = sample weight, not wall time. alloc_space is cumulative sampled allocation, not peak or live heap. All images are unfiltered whole-gateway views. Use matching binary/profile for interactive inspection.</p>${gallery.map(g => `<h2>${html(g.label)}</h2><pre>${html(['go', ...g.args].map(s => JSON.stringify(s)).join(' '))}</pre><a href="${g.png}"><img loading="lazy" src="${g.png}"></a>`).join('\n')}`)
console.log(`Rendered ${gallery.length} real pprof flamegraphs: ${output}`)
