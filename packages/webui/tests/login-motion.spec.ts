import { expect, test, type Page } from '@playwright/test'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { gunzipSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { CharacterTrace, frameAtTime, interludeAtTime, type CharacterTraceAsset } from '../src/lib/character-trace'
import { clipTraceSegment, coverPlacement, particlePoint } from '../src/lib/login-renderer'

const assetPath = (name: string) => fileURLToPath(new URL(`../public/assets/${name}`, import.meta.url))

test('trace covers every presentation frame, stays in bounds, and is tied to source assets', () => {
  const manifest = JSON.parse(readFileSync(assetPath('elysia-character-trace.json'), 'utf8')) as CharacterTraceAsset
  const compressed = readFileSync(assetPath(manifest.payload))
  expect(createHash('sha256').update(compressed).digest('hex')).toBe(manifest.payloadSha256)
  expect(createHash('sha256').update(readFileSync(assetPath(manifest.source.file))).digest('hex')).toBe(manifest.source.sha256)
  expect(createHash('sha256').update(readFileSync(assetPath('../role-mask.png'))).digest('hex')).toBe(manifest.target.sha256)
  const decoded = gunzipSync(compressed)
  const trace = new CharacterTrace(manifest, decoded.buffer.slice(decoded.byteOffset, decoded.byteOffset + decoded.byteLength) as ArrayBuffer, {} as HTMLImageElement)
  expect(manifest.frameTimes).toHaveLength(960)
  for (const time of manifest.frameTimes) {
    const paths = trace.sampleCharacterPaths(time)
    expect(new Set(paths.map((path) => path.group)).size).toBe(4)
    for (const path of paths) {
      if (path.points.length < 4) throw new Error(`Empty trace path at ${time}`)
      for (const coordinate of path.points) {
        if (!Number.isFinite(coordinate) || coordinate < 0 || coordinate > 1) throw new Error(`Out-of-bounds trace at ${time}`)
      }
    }
  }
  expect(trace.sampleCharacterPaths(16)).toEqual(trace.sampleCharacterPaths(0))
  expect(trace.targetPaths.every((path) => path.points.every((coordinate) => coordinate >= 0 && coordinate <= 1))).toBe(true)
})

test('media sampling, cover crop, flight endpoints and text use separate deterministic timelines', () => {
  expect(frameAtTime([0, 0.02, 0.08], 0.1, 0.06)).toBe(1)
  expect(frameAtTime([0, 0.02, 0.08], 0.1, 0.1)).toBe(0)
  expect(frameAtTime([0, 0.02, 0.08], 0.1, -0.01)).toBe(2)
  expect(coverPlacement(1920, 1080, 1000, 900, 0.5, 0.43)).toEqual({ fitWidth: 1600, fitHeight: 900, offsetHorizontal: -300, offsetVertical: 0 })
  const source = { horizontal: 0.2, vertical: 0.6 }
  const target = { horizontal: 0.8, vertical: 0.3 }
  expect(particlePoint(source, target, 0, 0.05)).toEqual(source)
  expect(particlePoint(source, target, 1, 0.05)).toEqual(target)
  expect(clipTraceSegment({ horizontal: -10, vertical: 10 }, { horizontal: 50, vertical: 10 }, 0, 0, 40, 30)).toEqual({ from: { horizontal: 0, vertical: 10 }, to: { horizontal: 40, vertical: 10 } })
  expect(clipTraceSegment({ horizontal: -10, vertical: 10 }, { horizontal: -10, vertical: 20 }, 0, 0, 40, 30)).toBeNull()
  expect(interludeAtTime(0).opacity).toBe(0)
  expect(interludeAtTime(2).opacity).toBe(1)
  expect(interludeAtTime(3.6).opacity).toBeGreaterThan(0)
  expect(interludeAtTime(4).opacity).toBe(1)
  expect(interludeAtTime(5)).toEqual({ opacity: 0, blur: 12 })
})

async function mockLogin(page: Page, valid = true) {
  await page.addInitScript(() => {
    localStorage.removeItem('elysia-webui.panel-token')
    localStorage.setItem('elysia-webui.theme', 'light')
  })
  await page.route('**/api/admin/**', (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/health') ? { status: 'ok', database: true, memory: { alloc: 0, sys: 0, numGC: 0 } }
      : /\/usage\/(trend|by-model|by-model-daily)$/.test(path) ? [] : { items: [], total: 0 }
    return route.fulfill({ status: valid ? 200 : 401, json: { ok: valid, data } })
  })
}

async function expectLoginReady(page: Page) {
  await page.goto('/#/login')
  await expect(page.locator('.garden')).toHaveAttribute('data-trace-ready', 'true', { timeout: 25000 })
  await expect.poll(() => page.locator('video').evaluate((video) => !video.paused && video.currentTime > 0)).toBe(true)
}

test('pause freezes video, camera, aurora and form; the choice survives reload', async ({ page }) => {
  await mockLogin(page)
  await expectLoginReady(page)
  await page.mouse.move(130, 180)
  await expect.poll(() => page.locator('.garden').evaluate((root) => Number(root.style.getPropertyValue('--par-x')))).toBeLessThan(-0.05)
  await page.getByRole('button', { name: '暂停动态效果' }).click()
  await expect.poll(() => page.locator('video').evaluate((video) => video.paused)).toBe(true)
  const snapshot = () => page.locator('.garden').evaluate((root) => ({
    horizontal: root.style.getPropertyValue('--par-x'), vertical: root.style.getPropertyValue('--par-y'),
    time: root.querySelector('video')!.currentTime,
    camera: getComputedStyle(root.querySelector('.garden-camera')!).scale,
    scene: getComputedStyle(root.querySelector('.garden-scene')!).transform,
    aurora: getComputedStyle(root.querySelector('.garden-aurora')!, '::before').transform,
    form: getComputedStyle(root.querySelector('.garden-login-motion')!).translate,
  }))
  const before = await snapshot()
  await page.mouse.move(1150, 620)
  await page.waitForTimeout(350)
  expect(await snapshot()).toEqual(before)
  await page.reload()
  await expect(page.locator('.garden')).toHaveAttribute('data-motion', 'paused')
  await expect(page.locator('video')).not.toHaveAttribute('src')
  await page.getByRole('button', { name: '播放动态效果' }).click()
  await expect.poll(() => page.locator('video').evaluate((video) => !video.paused)).toBe(true)
})

test('labels remain accessible, tools are equal-sized, and errors surface in a bottom toast without moving the form', async ({ page }) => {
  await mockLogin(page, false)
  await page.addInitScript(() => localStorage.setItem('elysia-webui.login-motion', 'paused'))
  await page.goto('/#/login')
  const token = page.getByLabel('访问令牌 Panel Access Token')
  await expect(token).toHaveAttribute('placeholder', 'Panel Access Token')
  await expect(page.locator('label[for="token"]')).toHaveClass('sr-only')
  const playback = await page.getByRole('button', { name: '播放动态效果' }).boundingBox()
  const theme = await page.getByRole('button', { name: '切换到深色模式' }).boundingBox()
  expect([playback?.width, playback?.height, theme?.width, theme?.height]).toEqual([44, 44, 44, 44])
  await token.fill('invalid-test-token')
  const before = await token.boundingBox()
  await page.getByRole('button', { name: '立即登录' }).click()
  // 底部 toast 报错：不夺焦点、不移动表单，输入值原样保留。
  await expect(page.locator('li[data-variant="destructive"]')).toContainText('Token 无效，请确认与后端 config.json 中的 panelAccessToken 一致')
  await expect(token).toHaveValue('invalid-test-token')
  expect(await token.boundingBox()).toEqual(before)
  await expect(page.locator('.garden')).toHaveAttribute('data-phase', 'login')
  expect(await page.evaluate(() => localStorage.getItem('elysia-webui.panel-token'))).toBeNull()
})

for (const mode of ['paused', 'reduced', 'unavailable'] as const) {
  test(`successful ${mode} login skips both cinematic and arrival effects`, async ({ page }) => {
    await mockLogin(page)
    if (mode === 'paused') await page.addInitScript(() => localStorage.setItem('elysia-webui.login-motion', 'paused'))
    if (mode === 'reduced') await page.emulateMedia({ reducedMotion: 'reduce' })
    if (mode === 'unavailable') await page.route('**/elysia-character-trace.json*', (route) => route.fulfill({ status: 404 }))
    await page.goto('/#/login')
    await page.getByLabel(/Panel Access Token/).fill('valid-test-token')
    await page.getByRole('button', { name: '立即登录' }).click()
    await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
    await expect(page.locator('.garden-cinematic, .arrival-echo, .elysia-arrive, .app-fade')).toHaveCount(0)
    expect(await page.evaluate(() => sessionStorage.getItem('elysia-webui.arrived-from-login'))).toBeNull()
  })
}

test('cinematic follows a looping video, keeps Chinese text centered and hands off once', async ({ page }) => {
  test.setTimeout(45000)
  await mockLogin(page)
  await expectLoginReady(page)
  await page.locator('video').evaluate((video) => new Promise<void>((resolve) => {
    video.addEventListener('seeked', () => resolve(), { once: true })
    video.currentTime = 15
  }))
  await page.getByLabel(/Panel Access Token/).fill('animated-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  const canvas = page.locator('.garden-cinematic')
  await expect(page.locator('.garden')).toHaveAttribute('data-cinematic', 'ready')
  await expect.poll(() => canvas.getAttribute('data-elapsed')).not.toBeNull()
  await expect(page.locator('.garden-interlude')).toContainText('因你而在的故事')
  await expect(page.getByText('TruE', { exact: true })).toHaveCount(0)
  await expect.poll(() => canvas.evaluate((element) => Number(element.dataset.elapsed))).toBeGreaterThan(1)
  expect(await page.locator('video').evaluate((video) => video.paused)).toBe(false)
  expect(await canvas.evaluate((element) => Number(element.dataset.paths))).toBeGreaterThan(100)
  expect(await canvas.evaluate((element) => Number(element.dataset.mediaTime))).toBeLessThan(5)
  const text = page.locator('.garden-interlude-cn')
  const bounds = await text.boundingBox()
  expect(Math.abs(bounds!.x + bounds!.width / 2 - page.viewportSize()!.width / 2)).toBeLessThan(2)
  // 5s 时间线:文字淡出窗口 4.0-5.0,采样 4.5s 处应处于半隐状态。
  await page.waitForFunction(() => Number(document.querySelector<HTMLCanvasElement>('.garden-cinematic')?.dataset.elapsed) > 4.5)
  expect(await text.evaluate((element) => Number(getComputedStyle(element).opacity))).toBeLessThan(1)
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('elysia-webui.panel-token'))).toBe('animated-test-token')
  await expect(canvas).toHaveCount(0)
})

test('pausing during the cinematic immediately finishes authentication without arrival effects', async ({ page }) => {
  await mockLogin(page)
  await expectLoginReady(page)
  await page.getByLabel(/Panel Access Token/).fill('paused-during-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.locator('.garden')).toHaveAttribute('data-cinematic', 'ready')
  await page.getByRole('button', { name: '暂停动态效果' }).click()
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect(page.locator('.arrival-echo, .elysia-arrive, .app-fade')).toHaveCount(0)
})

test('renderer context loss cannot strand a verified login', async ({ page }) => {
  await mockLogin(page)
  await expectLoginReady(page)
  await page.getByLabel(/Panel Access Token/).fill('context-loss-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.locator('.garden')).toHaveAttribute('data-cinematic', 'ready')
  await page.locator('canvas').evaluate((canvas) => canvas.getContext('webgl2')?.getExtension('WEBGL_lose_context')?.loseContext())
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect(page.locator('.arrival-echo, .elysia-arrive')).toHaveCount(0)
})

test('raw gzip delivery loads the same checked trace as HTTP-decoded gzip', async ({ page }) => {
  await mockLogin(page)
  await page.route('**/elysia-character-trace.bin.gz*', (route) => route.fulfill({
    contentType: 'application/octet-stream', body: readFileSync(assetPath('elysia-character-trace.bin.gz')),
  }))
  await expectLoginReady(page)
})

test('a stale trace manifest cannot delay verified authentication', async ({ page }) => {
  await mockLogin(page)
  const manifest = JSON.parse(readFileSync(assetPath('elysia-character-trace.json'), 'utf8'))
  manifest.source.sha256 = 'stale'
  await page.route('**/elysia-character-trace.json*', (route) => route.fulfill({ json: manifest }))
  await page.goto('/#/login')
  await page.getByLabel(/Panel Access Token/).fill('stale-asset-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect(page.locator('.garden-cinematic, .arrival-echo, .elysia-arrive')).toHaveCount(0)
})

test('autoplay refusal preserves the preference and the play button retries', async ({ page }) => {
  await mockLogin(page)
  await page.addInitScript(() => {
    const play = HTMLMediaElement.prototype.play
    let refused = false
    HTMLMediaElement.prototype.play = function () {
      if (!refused) { refused = true; return Promise.reject(new DOMException('Test autoplay refusal', 'NotAllowedError')) }
      return play.call(this)
    }
  })
  await page.goto('/#/login')
  await expect(page.getByRole('button', { name: '播放动态效果' })).toBeVisible()
  await expect(page.locator('.garden-form')).toHaveCSS('opacity', '1')
  expect(await page.evaluate(() => localStorage.getItem('elysia-webui.login-motion'))).toBeNull()
  await page.getByRole('button', { name: '播放动态效果' }).click()
  await expect(page.getByRole('button', { name: '暂停动态效果' })).toBeVisible()
  await expect.poll(() => page.locator('video').evaluate((video) => !video.paused && video.currentTime > 0)).toBe(true)
})

test('save-data and visibility restrictions do not overwrite the saved preference', async ({ page }) => {
  await mockLogin(page)
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.login-motion', 'playing')
    const connection = Object.assign(new EventTarget(), { saveData: true })
    Object.defineProperty(navigator, 'connection', { configurable: true, value: connection })
  })
  await page.goto('/#/login')
  await expect(page.getByRole('button', { name: '省流模式下已暂停动态效果' })).toBeDisabled()
  await expect(page.locator('video')).not.toHaveAttribute('src')
  await expect(page.locator('.garden')).toHaveAttribute('data-intro', 'false')
  await page.evaluate(() => {
    const connection = (navigator as Navigator & { connection: EventTarget & { saveData: boolean } }).connection
    connection.saveData = false
    connection.dispatchEvent(new Event('change'))
  })
  await expect.poll(() => page.locator('video').evaluate((video) => video.currentTime > 0)).toBe(true)
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect.poll(() => page.locator('video').evaluate((video) => video.paused)).toBe(true)
  expect(await page.evaluate(() => localStorage.getItem('elysia-webui.login-motion'))).toBe('playing')
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect.poll(() => page.locator('video').evaluate((video) => !video.paused)).toBe(true)
  await expect(page.locator('.garden-header')).toHaveCSS('animation-name', 'none')
})

test('backgrounding a cinematic completes login without an arrival marker', async ({ page }) => {
  await mockLogin(page)
  await expectLoginReady(page)
  await page.getByLabel(/Panel Access Token/).fill('background-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.locator('.garden')).toHaveAttribute('data-cinematic', 'ready')
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect(page.locator('.arrival-echo, .elysia-arrive')).toHaveCount(0)
})

test('failed GPU allocation releases partially initialized resources and skips the cinematic', async ({ page }) => {
  await mockLogin(page)
  await page.addInitScript(() => {
    const original = WebGL2RenderingContext.prototype.deleteProgram
    WebGL2RenderingContext.prototype.deleteProgram = function (program) {
      document.documentElement.dataset.releasedPrograms = String(Number(document.documentElement.dataset.releasedPrograms || 0) + 1)
      original.call(this, program)
    }
    WebGL2RenderingContext.prototype.createTexture = () => null
  })
  await expectLoginReady(page)
  await page.getByLabel(/Panel Access Token/).fill('allocation-failure-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('data-released-programs', '2')
  await expect(page.locator('.arrival-echo, .elysia-arrive')).toHaveCount(0)
})

test('empty submissions use the toast and repeated pending submissions verify only once', async ({ page }) => {
  await mockLogin(page)
  await page.addInitScript(() => localStorage.setItem('elysia-webui.login-motion', 'paused'))
  let verifications = 0
  let release: () => void = () => undefined
  const pending = new Promise<void>((resolve) => { release = resolve })
  await page.route('**/api/admin/health', async (route) => {
    verifications++
    await pending
    await route.fulfill({ status: 401, json: { ok: false } })
  })
  await page.goto('/#/login')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.locator('li[data-variant="destructive"]')).toContainText('请输入访问令牌')
  expect(verifications).toBe(0)
  await page.getByLabel(/Panel Access Token/).fill('duplicate-test-token')
  await page.locator('form').evaluate((form) => { form.requestSubmit(); form.requestSubmit(); form.requestSubmit() })
  await expect.poll(() => verifications).toBe(1)
  await expect(page.getByRole('button', { name: '正在验证' })).toBeDisabled()
  release()
  // 空提交的 toast(4.2s 自动关闭)可能仍在,断言最新弹出的一条。
  await expect(page.locator('li[data-variant="destructive"]').last()).toContainText('Token 无效')
  expect(verifications).toBe(1)
})

test('a touch viewport disables parallax and keeps target placement through a cinematic resize', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true })
  try {
    const page = await context.newPage()
    await mockLogin(page)
    await expectLoginReady(page)
    await page.mouse.move(20, 20)
    expect(await page.locator('.garden').evaluate((root) => getComputedStyle(root).getPropertyValue('--par-x').trim())).toBe('0')
    await page.getByLabel(/Panel Access Token/).fill('mobile-resize-test-token')
    await page.getByRole('button', { name: '立即登录' }).click()
    await expect(page.locator('.garden')).toHaveAttribute('data-cinematic', 'ready')
    await page.setViewportSize({ width: 844, height: 390 })
    const targetBounds = await page.locator('.garden-echo').boundingBox()
    await expect.poll(() => page.locator('canvas').evaluate((canvas) => canvas.width)).toBe(1688)
    await page.waitForSelector('.garden', { state: 'detached' })
    await expect(page.locator('.arrival-echo')).toBeVisible()
    expect(await page.locator('.arrival-echo').boundingBox()).toEqual(targetBounds)
    await expect(page.locator('.elysia-arrive')).toBeAttached()
    expect(await page.locator('.elysia-arrive').boundingBox()).toEqual(targetBounds)
    await expect(page.getByText('页面渲染失败')).toHaveCount(0)
  } finally { await context.close() }
})

test('video projection agrees with native CSS geometry through perspective and responsive crops', async ({ page }) => {
  await mockLogin(page)
  await expectLoginReady(page)
  await page.getByRole('button', { name: '暂停动态效果' }).click()
  for (const viewport of [{ width: 1440, height: 900 }, { width: 900, height: 700 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    const errors = await page.evaluate(async () => {
      const modulePath = '/src/lib/login-renderer.ts'
      const { readSceneProjection, projectVideoPoint } = await import(modulePath)
      const root = document.querySelector<HTMLElement>('.garden')!
      const scene = document.querySelector<HTMLElement>('.garden-scene')!
      const camera = document.querySelector<HTMLElement>('.garden-camera')!
      const video = document.querySelector<HTMLVideoElement>('video')!
      root.style.setProperty('--par-x', '0.75')
      root.style.setProperty('--par-y', '-0.55')
      const projection = readSceneProjection(root, scene, camera, video)
      const scale = Math.max(camera.clientWidth / video.videoWidth, camera.clientHeight / video.videoHeight)
      const position = getComputedStyle(video).objectPosition.split(' ').map(Number.parseFloat)
      return [[0.2, 0.3], [0.5, 0.5], [0.8, 0.7]].map(([horizontal, vertical]) => {
        const marker = document.createElement('span')
        marker.style.cssText = `position:absolute;width:0;height:0;left:${video.videoWidth * scale * horizontal + (camera.clientWidth - video.videoWidth * scale) * position[0] / 100}px;top:${video.videoHeight * scale * vertical + (camera.clientHeight - video.videoHeight * scale) * position[1] / 100}px`
        camera.appendChild(marker)
        const actual = marker.getBoundingClientRect()
        const predicted = projectVideoPoint(horizontal, vertical, projection)
        marker.remove()
        return Math.hypot(actual.left - predicted.horizontal, actual.top - predicted.vertical)
      })
    })
    expect(Math.max(...errors)).toBeLessThan(0.05)
  }
})

test('late video callbacks cannot pair stale timestamps with a newer decoded frame', async ({ page }) => {
  await mockLogin(page)
  await expectLoginReady(page)
  await page.evaluate(() => {
    const request = HTMLVideoElement.prototype.requestVideoFrameCallback
    let blocked = false
    HTMLVideoElement.prototype.requestVideoFrameCallback = function (callback) {
      return request.call(this, (time, metadata) => {
        if (!blocked) {
          blocked = true
          const until = performance.now() + 180
          while (performance.now() < until) { Math.sqrt(performance.now()) }
        }
        callback(time, metadata)
      })
    }
  })
  await page.getByLabel(/Panel Access Token/).fill('late-frame-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.locator('.garden')).toHaveAttribute('data-cinematic', 'ready')
  await expect.poll(() => page.locator('canvas').evaluate((canvas) => Number(canvas.dataset.maxFrameDrift))).toBeGreaterThan(100)
  await page.waitForSelector('.garden', { state: 'detached' })
  await expect(page.locator('.arrival-echo')).toBeVisible()
})

test('video failure cannot delay verified authentication or replay arrival effects', async ({ page }) => {
  await mockLogin(page)
  await page.route('**/elysia-login.mp4*', (route) => route.abort('failed'))
  await page.goto('/#/login')
  await expect(page.getByRole('button', { name: '动态壁纸加载失败' })).toBeDisabled()
  await page.getByLabel(/Panel Access Token/).fill('video-failure-test-token')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect(page.locator('.arrival-echo, .elysia-arrive')).toHaveCount(0)
})
