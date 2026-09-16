import { chromium } from '@playwright/test'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import './verify.mjs'

const output = fileURLToPath(new URL('../../.tmp-dev/login-browser-review/', import.meta.url))
const base = process.env.LOGIN_REVIEW_URL || 'http://127.0.0.1:5274'
const measureOnly = process.argv.includes('--measure')
const browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || undefined, headless: true })
const variants = [
  { name: 'desktop-light', width: 1440, height: 900, dark: false, ratio: 1, mediaTime: 2 },
  { name: 'desktop-dark', width: 1440, height: 900, dark: true, ratio: 2, mediaTime: 9 },
  { name: 'mobile-loop', width: 390, height: 844, dark: false, ratio: 2, mediaTime: 15 },
]
const manifest = JSON.parse(await readFile(new URL('../../packages/webui/public/assets/elysia-character-trace.json', import.meta.url), 'utf8'))
const report = { browser: browser.version(), videoSha256: manifest.source.sha256, traceSha256: manifest.payloadSha256, annotationSha256: manifest.annotationSha256,
  measurement: 'Headless local browser, not physical mobile hardware; presented canvas callbacks, not GPU completion timestamps.', variants: [] }
await mkdir(output, { recursive: true })
try {
  for (const variant of variants) {
    const mobile = variant.width < 700
    const context = await browser.newContext({ viewport: { width: variant.width, height: variant.height }, deviceScaleFactor: variant.ratio, hasTouch: mobile, isMobile: mobile })
    try {
      const page = await context.newPage()
      const errors = []
      page.on('pageerror', (error) => errors.push(error.message))
      await page.addInitScript((dark) => localStorage.setItem('elysia-webui.theme', dark ? 'dark' : 'light'), variant.dark)
      await page.route('**/api/admin/**', (route) => {
        const path = new URL(route.request().url()).pathname
        const data = path.endsWith('/health') ? { status: 'ok', database: true, memory: { alloc: 0, sys: 0, numGC: 0 } }
          : /\/usage\/(trend|by-model|by-model-daily)$/.test(path) ? [] : { items: [], total: 0 }
        return route.fulfill({ json: { ok: true, data } })
      })
      await page.goto(`${base}/#/login`)
      await page.waitForSelector('.garden[data-trace-ready="true"]')
      await page.waitForFunction(() => document.querySelector('video')?.currentTime > 2)
      if (!measureOnly) await page.screenshot({ path: `${output}/${variant.name}-login.png` })
      await page.locator('video').evaluate((video, time) => new Promise((resolve) => {
        video.addEventListener('seeked', resolve, { once: true })
        video.currentTime = time
      }), variant.mediaTime)
      await page.evaluate(() => {
        window.loginReviewFrames = []
        const observer = new MutationObserver((changes) => {
          for (const change of changes) {
            const canvas = change.target
            window.loginReviewFrames.push({ elapsed: Number(canvas.dataset.elapsed), mediaTime: Number(canvas.dataset.mediaTime), particles: Number(canvas.dataset.activeParticles), width: canvas.width, height: canvas.height })
          }
        })
        observer.observe(document.querySelector('.garden'), { subtree: true, attributes: true, attributeFilter: ['data-elapsed'] })
        window.loginReviewObserver = observer
      })
      await page.getByLabel(/Panel Access Token/).fill('visual-review-test-token')
      await page.getByRole('button', { name: '立即登录' }).click()
      if (!measureOnly) {
        for (const elapsed of [0.6, 1.6, 2.9, 3.7]) {
          await page.waitForFunction((seconds) => Number(document.querySelector('canvas')?.dataset.elapsed) >= seconds, elapsed)
          await page.screenshot({ path: `${output}/${variant.name}-${elapsed}.png` })
        }
      }
      await page.waitForSelector('.garden', { state: 'detached' })
      if (!measureOnly) await page.screenshot({ path: `${output}/${variant.name}-handoff.png` })
      const frames = await page.evaluate(() => { window.loginReviewObserver.disconnect(); return window.loginReviewFrames })
      const gaps = frames.slice(1).map((frame, index) => (frame.elapsed - frames[index].elapsed) * 1000).sort((first, second) => first - second)
      const item = { ...variant, frames: frames.length, averageHz: (frames.length - 1) / (frames.at(-1).elapsed - frames[0].elapsed), p95IntervalMs: gaps[Math.floor(gaps.length * 0.95)], maxIntervalMs: gaps.at(-1), lastFrame: frames.at(-1), errors }
      if (item.lastFrame.elapsed !== 4 || item.lastFrame.particles !== 0 || errors.length) throw new Error(`Invalid handoff: ${JSON.stringify(item)}`)
      report.variants.push(item)
      console.log(JSON.stringify(item))
    } finally { await context.close() }
  }
  await writeFile(`${output}/${measureOnly ? 'performance' : 'screenshots'}-report.json`, JSON.stringify(report, null, 2))
} finally { await browser.close() }
