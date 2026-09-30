import { expect, test, type Page } from '@playwright/test'
import type { RuntimeConfig, RuntimeConfigUpdate, UsageLogRuntimeConfig } from '../src/lib/types'

const logConfig: UsageLogRuntimeConfig = {
  persistEnabled: true, retentionDays: 0, maxContentMB: 0, maxRecords: 0,
  bodyMaxKB: 0, bodyOnErrorOnly: true, externalizeMedia: false, cleanupIntervalMinutes: 60,
}

async function mockSettings(page: Page, usageLog: UsageLogRuntimeConfig = logConfig) {
  const current: RuntimeConfig = {
    host: '127.0.0.1', port: 8765, panelAccessToken: '', databasePath: '/tmp/elysia.db', defaultDatabasePath: '/tmp/elysia.db',
    logLevel: 'info', httpTimeout: 120, enablePprof: false, usageLog, systemLog: { retentionDays: 0, maxRecords: 0, maxContentMB: 0 },
  }
  const updates: RuntimeConfigUpdate[] = []
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-only'))
  await page.route('**/api/admin/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    let data: unknown = { items: [], total: 0 }
    if (path.endsWith('/runtime-config')) {
      if (request.method() === 'PUT') {
        const patch = request.postDataJSON() as RuntimeConfigUpdate
        updates.push(patch)
        Object.assign(current, Object.fromEntries(Object.entries(patch).filter(([key]) => !['usageLog', 'systemLog'].includes(key))))
        if (patch.usageLog) current.usageLog = { ...current.usageLog, ...patch.usageLog }
        if (patch.systemLog) current.systemLog = { ...current.systemLog, ...patch.systemLog }
        data = { restartRequired: false }
      } else {
        data = current
      }
    } else if (/\/usage\/(trend|by-model|by-model-daily)$/.test(path)) {
      data = []
    } else if (path.endsWith('/health')) {
      data = { status: 'ok', database: true, memory: { alloc: 0, sys: 0, numGC: 0 } }
    } else if (path.endsWith('/usage/storage')) {
      data = null
    } else if (path.endsWith('/usage/maintenance')) {
      data = { state: 'idle', phase: 'idle' }
    } else if (path.endsWith('/seq')) {
      data = { seq: 1 }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  return updates
}

test('body logging is opt-in while basic log persistence stays enabled', async ({ page }) => {
  const updates = await mockSettings(page)
  await page.goto('/#/runtime')
  await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
  const capture = page.getByRole('switch', { name: '保存请求与响应正文', exact: true })
  await expect(capture).not.toBeChecked()
  await expect(page.getByRole('switch', { name: '启用日志持久化', exact: true })).toBeChecked()
  await expect(page.getByRole('spinbutton', { name: '正文保存上限', exact: true })).toHaveCount(0)
  await capture.click()
  const limit = page.getByRole('spinbutton', { name: '正文保存上限', exact: true })
  await expect(limit).toHaveValue('1024')
  await limit.fill('256')
  await capture.click()
  await expect(limit).toHaveCount(0)
  await capture.click()
  await expect(limit).toHaveValue('256')
  expect(updates).toHaveLength(0)
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(1)
  expect(updates[0].usageLog).toMatchObject({ bodyMaxKB: 256, persistEnabled: true })
  await page.reload()
  await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
  await expect(capture).toBeChecked()
  await expect(limit).toHaveValue('256')
  await capture.click()
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(2)
  expect(updates[1].usageLog).toMatchObject({ bodyMaxKB: 0, persistEnabled: true })
  await page.reload()
  await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
  await expect(capture).not.toBeChecked()
  await capture.click()
  await expect(limit).toHaveValue('1024')
})

test('existing capture settings survive toggling and unrelated saves leave log policy untouched', async ({ page }) => {
  const updates = await mockSettings(page, { ...logConfig, bodyMaxKB: 512 })
  await page.goto('/#/runtime')
  await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
  const capture = page.getByRole('switch', { name: '保存请求与响应正文', exact: true })
  await expect(capture).toBeChecked()
  await expect(page.getByRole('spinbutton', { name: '正文保存上限', exact: true })).toHaveValue('512')
  await expect(page.getByRole('switch', { name: '仅保存失败请求正文', exact: true })).toBeChecked()
  await expect(page.getByRole('switch', { name: '媒体外置保存', exact: true })).not.toBeChecked()
  await capture.click()
  await expect(page.getByRole('switch', { name: '仅保存失败请求正文', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(1)
  expect(updates[0].usageLog).toMatchObject({ bodyMaxKB: 0, bodyOnErrorOnly: true, externalizeMedia: false })
  await page.reload()
  await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
  await expect(capture).not.toBeChecked()
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(2)
  expect(updates[1].usageLog).toBeUndefined()
  await capture.click()
  await expect(page.getByRole('switch', { name: '仅保存失败请求正文', exact: true })).toBeChecked()
  await expect(page.getByRole('switch', { name: '媒体外置保存', exact: true })).not.toBeChecked()
})


test('system log budgets save independently and maintenance reports blocked and failed states', async ({ page }) => {
  const updates = await mockSettings(page)
  const zero = { byTTL: 0, byRecords: 0, byContent: 0 }
  let maintenance = { state: 'waiting', phase: 'checkpoint', pending: false, lastRunAt: new Date().toISOString(),
    usageDeleted: zero, systemDeleted: zero, assetsRemoved: 0, remainingFreePages: 12, checkpointBlocked: true, lastError: '' }
  await page.route('**/api/admin/usage/maintenance', (route) => route.fulfill({ json: { ok: true, data: maintenance } }))
  await page.goto('/#/runtime')
  await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
  await expect(page.getByText('空间回收等待重试', { exact: true })).toBeVisible()
  await expect(page.getByText('读取事务暂时阻止 WAL 回收，结束后会自动重试。')).toBeVisible()
  await page.getByRole('spinbutton', { name: '系统日志内容预算', exact: true }).fill('64')
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(1)
  expect(updates[0].systemLog).toMatchObject({ maxContentMB: 64 })
  expect(updates[0].usageLog).toBeUndefined()
  maintenance = { ...maintenance, state: 'failed', checkpointBlocked: false, lastError: 'disk full' }
  await expect(page.getByText('日志维护或空间回收失败，将自动重试')).toBeVisible({ timeout: 10000 })
  await expect(page.getByText('disk full', { exact: true })).toBeVisible()
  await expect(page.getByText('空间回收完成', { exact: true })).toHaveCount(0)
  maintenance = { ...maintenance, state: 'completed', phase: 'idle', remainingFreePages: 0, lastError: '' }
  await expect(page.getByText('空间回收完成', { exact: true })).toBeVisible({ timeout: 10000 })
})

test('category drafts survive switching, reverting and saving together', async ({ page }) => {
  const updates = await mockSettings(page)
  await page.goto('/#/runtime')
  await expect(page.getByRole('tab', { name: '基础设置' })).toHaveAttribute('aria-selected', 'true')
  const host = page.getByRole('textbox', { name: '监听 Host', exact: true })
  await host.fill('0.0.0.0')
  await host.fill('127.0.0.1')
  await page.getByRole('button', { name: '重载配置', exact: true }).click()
  await expect(page.getByRole('button', { name: '重载配置', exact: true })).toBeEnabled()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await host.fill('0.0.0.0')
  await page.getByRole('tab', { name: '日志与存储' }).click()
  await page.getByRole('spinbutton', { name: '系统日志内容预算', exact: true }).fill('64')
  await page.getByRole('tab', { name: '基础设置' }).click()
  await expect(host).toHaveValue('0.0.0.0')
  expect(updates).toHaveLength(0)
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(1)
  expect(updates[0]).toMatchObject({ host: '0.0.0.0', systemLog: { maxContentMB: 64 } })
  expect(updates[0].usageLog).toBeUndefined()
  await page.getByRole('tab', { name: '日志与存储' }).click()
  await expect(page.getByRole('spinbutton', { name: '系统日志内容预算', exact: true })).toHaveValue('64')
})

test('save locks inputs, preserves failed drafts and focuses invalid hidden fields', async ({ page }) => {
  await mockSettings(page)
  let releaseSave!: () => void
  const pending = new Promise<void>((resolve) => { releaseSave = resolve })
  await page.route('**/api/admin/runtime-config', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    await pending
    await route.fulfill({ status: 500, json: { ok: false, error: { code: 'save_failed', message: 'test disk failure' } } })
  })
  await page.goto('/#/runtime')
  const port = page.getByRole('spinbutton', { name: '监听 Port', exact: true })
  await port.fill('9000')
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect(port).toBeDisabled()
  await expect(page.getByRole('button', { name: '保存中…', exact: true })).toBeDisabled()
  await page.getByRole('tab', { name: '日志与存储' }).click()
  await expect(page.getByRole('switch', { name: '启用日志持久化', exact: true })).toBeDisabled()
  releaseSave()
  await expect(page.getByText('test disk failure', { exact: true })).toBeVisible()
  await page.getByRole('tab', { name: '基础设置' }).click()
  await expect(port).toBeEnabled()
  await expect(port).toHaveValue('9000')
  await port.fill('65536')
  await page.getByRole('tab', { name: '模型目录' }).click()
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect(port).toBeFocused()
  await port.fill('9000')
  await page.getByRole('tab', { name: '安全设置' }).click()
  const cidr = page.getByRole('textbox', { name: '禁止出站 IP 段（CIDR）', exact: true })
  await cidr.fill('invalid cidr')
  await page.getByRole('tab', { name: '远程访问' }).click()
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect(cidr).toBeFocused()
  await cidr.fill('999.0.0.0/8')
  await page.route('**/api/admin/runtime-config', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    await route.fulfill({ status: 400, json: { ok: false, error: { code: 'invalid_outbound', message: 'invalid CIDR' } } })
  })
  await page.getByRole('tab', { name: '基础设置' }).click()
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect(cidr).toBeFocused()
  await expect(cidr).toHaveValue('999.0.0.0/8')
})

test('reload cancellation keeps drafts; confirmed reload resets even an unchanged server snapshot', async ({ page }) => {
  await mockSettings(page)
  let reloads = 0
  await page.route('**/api/admin/reload', async (route) => {
    reloads++
    await route.fulfill({ json: { ok: true, data: {} } })
  })
  await page.goto('/#/runtime')
  const host = page.getByRole('textbox', { name: '监听 Host', exact: true })
  await host.fill('0.0.0.0')
  await page.getByRole('button', { name: '重载配置', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.getByRole('button', { name: '取消', exact: true }).click()
  expect(reloads).toBe(0)
  await expect(host).toHaveValue('0.0.0.0')
  await page.getByRole('button', { name: '重载配置', exact: true }).click()
  await page.getByRole('button', { name: '丢弃并重载', exact: true }).click()
  await expect.poll(() => reloads).toBe(1)
  await expect(host).toHaveValue('127.0.0.1')
})

test('details collapse independently; immediate actions do not submit drafts', async ({ page }) => {
  const updates = await mockSettings(page)
  let cleanups = 0
  let catalogRefreshes = 0
  await page.route('**/api/admin/usage/cleanup', async (route) => {
    cleanups++
    await route.fulfill({ json: { ok: true, data: { accepted: true } } })
  })
  await page.route('**/api/admin/model-catalog/refresh', async (route) => {
    catalogRefreshes++
    await route.fulfill({ json: { ok: true, data: { status: { entries: 42 } } } })
  })
  await page.goto('/#/runtime')
  await page.getByRole('tab', { name: '日志与存储' }).click()
  await expect(page.getByText('数据库文件', { exact: true })).toBeVisible()
  await expect(page.getByText('WAL 文件', { exact: true })).not.toBeVisible()
  await page.getByText('查看占用明细', { exact: true }).click()
  await expect(page.getByText('WAL 文件', { exact: true })).toBeVisible()
  await page.getByText('查看占用明细', { exact: true }).click()
  await page.getByRole('spinbutton', { name: '过期清理天数', exact: true }).fill('14')
  await page.getByRole('button', { name: '立即清理', exact: true }).click()
  await expect.poll(() => cleanups).toBe(1)
  await page.getByRole('tab', { name: '模型目录' }).click()
  await page.getByRole('spinbutton', { name: '后台自动同步周期', exact: true }).fill('60')
  await page.getByRole('button', { name: '立即更新', exact: true }).click()
  await expect.poll(() => catalogRefreshes).toBe(1)
  expect(updates).toHaveLength(0)
})

for (const viewport of [{ width: 1440, height: 1000 }, { width: 820, height: 900 }, { width: 375, height: 812 }, { width: 812, height: 375 }]) {
  test(`settings remain usable at ${viewport.width}x${viewport.height} in both themes`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockSettings(page)
    await page.goto('/#/logs')
    await expect(page.getByRole('heading', { name: '系统日志', exact: true })).toBeVisible()
    const logTitle = await page.getByRole('heading', { name: '系统日志', exact: true }).boundingBox()
    await page.goto('/#/runtime')
    await expect(page.getByRole('tablist', { name: '配置分类' })).toBeVisible()
    const runtimeTitle = await page.getByRole('heading', { name: '运行配置', exact: true }).boundingBox()
    expect(runtimeTitle!.x).toBe(logTitle!.x)
    expect(runtimeTitle!.y).toBe(logTitle!.y)
    await expect(page.getByText('修改后保存，应用于所有分类', { exact: true })).toHaveCount(0)
    await expect(page.getByText('有未保存的修改', { exact: true })).toHaveCount(0)
    await page.screenshot({ path: testInfo.outputPath('initial.png'), fullPage: true })
    await page.getByRole('tab', { name: '基础设置', exact: true }).focus()
    await page.keyboard.press('ArrowRight')
    await expect(page.getByRole('tab', { name: '安全设置', exact: true })).toBeFocused()
    await page.keyboard.press('End')
    await expect(page.getByRole('tab', { name: '模型目录', exact: true })).toBeFocused()
    for (const dark of [false, true]) {
      await page.evaluate((enabled) => document.documentElement.classList.toggle('dark', enabled), dark)
      for (const name of ['基础设置', '安全设置', '远程访问', '日志与存储', '模型目录']) {
        await page.getByRole('tab', { name, exact: true }).click()
        if (name === '基础设置') await page.getByRole('textbox', { name: '数据库存储路径', exact: true }).fill('/very-long-directory/'.repeat(15) + 'elysia.db')
        if (name === '远程访问') await page.getByRole('textbox', { name: '对外基础地址', exact: true }).fill('https://example.com/' + 'long-path/'.repeat(12))
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
        await page.screenshot({ path: testInfo.outputPath(`${name}-${dark ? 'dark' : 'light'}.png`), fullPage: true })
      }
    }
    await page.getByRole('tab', { name: '日志与存储', exact: true }).click()
    await page.getByText('查看占用明细', { exact: true }).scrollIntoViewIfNeeded()
    const save = page.getByRole('button', { name: '保存配置', exact: true })
    const box = await save.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.y).toBeGreaterThanOrEqual(0)
    expect(box!.y + box!.height).toBeLessThan(viewport.height)
    const categories = await page.getByRole('tablist', { name: '配置分类' }).boundingBox()
    expect(Math.abs(categories!.y + categories!.height / 2 - (box!.y + box!.height / 2))).toBeLessThan(1)
    expect(categories!.y).toBeLessThan(20)
    const toolbar = page.getByRole('region', { name: '运行配置工具栏' })
    await expect(toolbar).toHaveCSS('backdrop-filter', 'blur(24px) saturate(1.5)')
    expect((await toolbar.boundingBox())!.y).toBe(0)
    for (const dark of [false, true]) {
      await page.evaluate((enabled) => document.documentElement.classList.toggle('dark', enabled), dark)
      await page.screenshot({ path: testInfo.outputPath(`pinned-${dark ? 'dark' : 'light'}.png`) })
    }
    expect(await save.evaluate((button) => {
      const rect = button.getBoundingClientRect()
      return button.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2))
    })).toBe(true)
    await page.getByRole('tab', { name: '基础设置', exact: true }).click()
    await expect(page.getByRole('textbox', { name: '监听 Host', exact: true })).toBeInViewport()
  })
}

test('runtime and overview tabs share the sliding underline and respect reduced motion', async ({ page }) => {
  await mockSettings(page)
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  for (const [path, name, target] of [
    ['/#/runtime', '配置分类', '日志与存储'],
    ['/#/overview', '透视维度', '模型调用日分布'],
  ]) {
    await page.goto(path)
    const list = page.getByRole('tablist', { name, exact: true })
    const bar = list.locator('span[aria-hidden="true"]')
    await expect(bar).toHaveCSS('transition-property', 'left, width')
    await expect(bar).toHaveCSS('transition-duration', '0.3s')
    await list.getByRole('tab', { name: target, exact: true }).click()
    await expect.poll(async () => {
      const line = await bar.boundingBox()
      const selected = await list.getByRole('tab', { selected: true }).boundingBox()
      return Math.abs(line!.x - selected!.x) + Math.abs(line!.width - selected!.width)
    }).toBeLessThan(1)
  }
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await expect(page.getByRole('tablist', { name: '透视维度' }).locator('span[aria-hidden="true"]')).toHaveCSS('transition-property', 'none')
})
