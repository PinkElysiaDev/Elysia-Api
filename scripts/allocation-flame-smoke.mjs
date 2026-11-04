import assert from 'node:assert/strict'
import { readFile, writeFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { join, resolve } from 'node:path'
import { chromium } from 'playwright'

// Browser validation runs outside every performance/profile measurement window.
const root = resolve(process.argv[2])
const figures = JSON.parse(await readFile(join(root, 'manifest.json'), 'utf8'))
const pages = new Map([['/index.html', await readFile(join(root, 'index.html'))]])
for (const figure of figures) pages.set('/' + figure.file, await readFile(join(root, figure.file)))
const server = createServer((request, response) => {
  const page = pages.get(request.url.split('?')[0])
  response.writeHead(page ? 200 : 404, { 'Content-Type': 'text/html; charset=utf-8' })
  response.end(page)
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const browser = await chromium.launch({ headless: true })
const report = { status: 'running', browser: browser.version(), figures: figures.length, checked: [], errors: [] }
try {
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
  page.on('pageerror', error => report.errors.push(error.message))
  const base = `http://127.0.0.1:${server.address().port}`
  await page.goto(base + '/index.html')
  assert.equal(await page.locator('a').count(), figures.length)
  const kinds = new Set()
  for (const figure of figures) {
    if (kinds.has(figure.kind)) continue
    kinds.add(figure.kind)
    await page.goto(base + '/' + figure.file)
    const boxes = page.locator('#stack-chart .boxbg')
    await boxes.first().waitFor()
    assert(await boxes.count() > 1, 'flame graph has no sampled call tree')
    await boxes.nth(1).hover()
    assert(await page.locator('#current-details-left').textContent())
    await boxes.nth(1).click()
    assert(new URL(page.url()).searchParams.has('p'), 'flame pivot did not update')
    assert(await boxes.count() > 0, 'pivot erased the graph')
    report.checked.push({ file: figure.file, kind: figure.kind, interactions: ['render', 'hover details', 'pivot'] })
  }
  assert.deepEqual(report.errors, [])
  report.status = 'passed'
} catch (error) {
  report.status = 'failed'
  report.errors.push(error.message)
  throw error
} finally {
  await writeFile(join(root, 'browser-smoke.json'), JSON.stringify(report, null, 2))
  await browser.close()
  await new Promise(resolve => server.close(resolve))
}
