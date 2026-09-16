import { expect, test, type Page } from '@playwright/test'

async function mockAdmin(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'test-only-token')
    localStorage.setItem('elysia-webui.theme', 'light')
  })
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/seq') ? { seq: 1 }
      : path.endsWith('/model-groups') ? { items: [{ id: 'one', name: 'Alpha', models: [] }, { id: 'two', name: 'Beta', models: [] }] }
        : { items: [], total: 0 }
    await route.fulfill({ json: { ok: true, data } })
  })
}

test.beforeEach(async ({ page }) => { await mockAdmin(page) })

test('system log pagination survives delayed responses and clamps only after real totals arrive', async ({ page }) => {
  let total = 120
  await page.route('**/api/admin/logs?**', async (route) => {
    const offset = Number(new URL(route.request().url()).searchParams.get('offset'))
    // Realistic latency exposes the undefined-data reset on an uncached page.
    await new Promise((resolve) => setTimeout(resolve, 350))
    await route.fulfill({ json: { ok: true, data: {
      total,
      items: offset >= total ? [] : [{ id: offset + 1, createdAt: '2026-09-09T08:00:00Z', level: 'info', message: `Log ${offset + 1}` }],
    } } })
  })
  await page.goto('/#/logs')
  await expect(page.getByText('Log 1', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '下一页' }).click()
  await expect(page.getByText('Log 51', { exact: true })).toBeVisible()
  await expect(page.getByText(/第 2\/3 页/)).toBeVisible()
  await page.getByRole('button', { name: '下一页' }).click()
  await expect(page.getByText('Log 101', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '下一页' })).toBeDisabled()
  total = 60
  await page.getByRole('button', { name: '刷新日志' }).click()
  await expect(page.getByText('Log 51', { exact: true })).toBeVisible()
  await expect(page.getByText(/第 2\/2 页/)).toBeVisible()
})

test('mobile navigation traps focus, restores it, and closes on navigation or desktop resize', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 })
  await page.goto('/#/logs')
  const trigger = page.getByRole('button', { name: '打开导航菜单' })
  await trigger.click()
  const drawer = page.getByRole('dialog', { name: '导航菜单', exact: true })
  await expect(drawer).toBeVisible()
  await expect(page.locator('body')).toHaveCSS('overflow', 'hidden')
  for (let i = 0; i < 16; i++) {
    await page.keyboard.press('Tab')
    await expect.poll(() => drawer.evaluate((element) => element.contains(document.activeElement))).toBe(true)
  }
  await page.keyboard.press('Escape')
  await expect(drawer).not.toBeVisible()
  await expect(trigger).toBeFocused()
  await trigger.click()
  await drawer.getByRole('link', { name: '调用日志' }).click()
  await expect(page.getByRole('heading', { name: '调用日志', exact: true })).toBeVisible()
  await expect(drawer).not.toBeVisible()
  await trigger.click()
  await page.setViewportSize({ width: 1280, height: 800 })
  await expect(drawer).not.toBeVisible()
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeVisible()
  await expect(page.locator('body')).not.toHaveCSS('overflow', 'hidden')
})

test('model filters support keyboard multi-selection, search, Escape and clear', async ({ page }) => {
  await page.goto('/#/usage-logs')
  const trigger = page.getByRole('button', { name: '模型组筛选', exact: true })
  await trigger.focus()
  await page.keyboard.press('Enter')
  const search = page.getByRole('combobox', { name: '搜索模型组' })
  await expect(search).toBeFocused()
  await search.press('ArrowDown')
  await search.press('Enter')
  await expect(page.getByRole('option', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'true')
  await search.press('ArrowDown')
  await search.press('Enter')
  await expect(page.getByRole('option', { name: 'Beta' })).toHaveAttribute('aria-selected', 'true')
  await search.fill('no-matches')
  await expect(page.getByText('没有匹配的选项，请尝试其他关键词')).toBeVisible()
  await search.fill('beta')
  await expect(page.getByRole('option')).toHaveCount(1)
  await search.press('ArrowDown')
  await search.press('Enter')
  await expect(page.getByRole('option', { name: 'Beta' })).toHaveAttribute('aria-selected', 'false')
  await search.press('Escape')
  const selectedTrigger = page.getByRole('button', { name: '模型组筛选（已选 1 项）' })
  await expect(selectedTrigger).toBeFocused()
  await page.getByRole('button', { name: '清空模型组选择' }).click()
  await expect(trigger).toBeFocused()
  await trigger.click()
  await search.press('Tab')
  await expect(page.getByRole('dialog', { name: '模型组筛选选项' })).not.toBeVisible()
})

test('mobile filters fit both themes and landscape with reduced motion', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/#/usage-logs')
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => document.documentElement.classList.toggle('dark', value === 'dark'), theme)
    for (const viewport of [{ width: 375, height: 812 }, { width: 740, height: 375 }]) {
      await page.setViewportSize(viewport)
      await page.getByRole('button', { name: '调用方筛选', exact: true }).click()
      const panel = page.getByRole('dialog', { name: '调用方筛选选项' })
      await expect(panel).toBeVisible()
      const box = await panel.boundingBox()
      expect(box!.x).toBeGreaterThanOrEqual(15)
      expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width - 15)
      expect(box!.y).toBeGreaterThanOrEqual(0)
      expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height)
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.keyboard.press('Escape')
    }
  }
})

test('failed page loads can be retried without losing the requested page', async ({ page }) => {
  let fail = true
  await page.route('**/api/admin/logs?**', async (route) => {
    const offset = Number(new URL(route.request().url()).searchParams.get('offset'))
    if (offset > 0 && fail) {
      await route.fulfill({ status: 503, json: { ok: false, error: { code: 'unavailable', message: '暂时无法读取日志' } } })
      return
    }
    await route.fulfill({ json: { ok: true, data: {
      total: 100,
      items: [{ id: offset + 1, createdAt: '2026-09-09T08:00:00Z', level: 'info', message: `Log ${offset + 1}` }],
    } } })
  })
  await page.goto('/#/logs')
  await page.getByRole('button', { name: '下一页' }).click()
  await expect(page.getByText('暂时无法读取日志')).toBeVisible()
  fail = false
  await page.getByRole('button', { name: '重试' }).click()
  await expect(page.getByText('Log 51', { exact: true })).toBeVisible()
  await expect(page.getByText(/第 2\/2 页/)).toBeVisible()
})

test('login supports keyboard secret visibility and announces validation errors', async ({ page }) => {
  await page.goto('/#/logs')
  await page.route('**/api/admin/health', (route) => route.fulfill({ status: 401, json: { error: 'unauthorized' } }))
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Elysia API' })).toBeVisible()
  const token = page.getByLabel(/Panel Access Token/)
  await token.fill('test-invalid-token')
  await token.press('Tab')
  await expect(page.getByRole('button', { name: '显示', exact: true })).toBeFocused()
  await page.keyboard.press('Space')
  await expect(token).toHaveAttribute('type', 'text')
  await page.getByRole('button', { name: '立即登录' }).click()
  await expect(page.locator('li[data-variant="destructive"]')).toContainText('Token 无效')
  await expect(token).toHaveAttribute('aria-invalid', 'true')
  await expect(token).toHaveValue('test-invalid-token')
})

test('login reports connection failures and can retry the same token', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/#/logs')
  let offline = true
  await page.route('**/api/admin/health', (route) => offline
    ? route.abort('connectionrefused')
    : route.fulfill({ json: { ok: true, data: {} } }))
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '退出', exact: true }).click()
  const token = page.getByLabel(/Panel Access Token/)
  await token.fill('test-valid-token')
  const submit = page.getByRole('button', { name: '立即登录', exact: true })
  await submit.click()
  await expect(page.locator('li[data-variant="destructive"]')).toContainText('无法连接到后端，请检查网络与服务状态')
  await expect(page.locator('li[data-variant="destructive"]')).not.toContainText('Token 无效')
  await expect(token).toHaveValue('test-valid-token')
  await expect(submit).toBeEnabled()
  await expect.poll(() => page.evaluate(() => localStorage.getItem('elysia-webui.panel-token'))).toBeNull()
  offline = false
  await submit.click()
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  await expect.poll(() => page.evaluate(() => localStorage.getItem('elysia-webui.panel-token'))).toBe('test-valid-token')
})

test('login distinguishes backend failures from invalid tokens', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/#/logs')
  // 后端 5xx（如反代 502 / SQLite 故障）不应误导用户去改 panelAccessToken。
  await page.route('**/api/admin/health', (route) => route.fulfill({ status: 503, body: 'service unavailable' }))
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '退出', exact: true }).click()
  const token = page.getByLabel(/Panel Access Token/)
  await token.fill('test-valid-token')
  await page.getByRole('button', { name: '立即登录', exact: true }).click()
  await expect(page.locator('li[data-variant="destructive"]')).toContainText('后端服务异常（HTTP 503）')
  await expect(page.locator('li[data-variant="destructive"]')).not.toContainText('Token 无效')
})

test('logout clears cached list data before next session', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.route('**/api/admin/health', (route) => route.fulfill({ json: { ok: true, data: {} } }))
  let session = 0
  await page.route('**/api/admin/logs**', (route) => {
    session += 1
    route.fulfill({ json: { ok: true, data: {
      total: 1,
      items: [{ id: session, createdAt: '2026-09-09T08:00:00Z', level: 'info', message: `Session ${session} log` }],
    } } })
  })
  // beforeEach 注入的令牌即第一个会话:先确认其数据已渲染,再走登出与二次登录。
  await page.goto('/#/logs')
  await expect(page.getByText('Session 1 log')).toBeVisible()
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Elysia API' })).toBeVisible()
  await page.getByLabel(/Panel Access Token/).fill('test-valid-token')
  await page.getByRole('button', { name: '立即登录', exact: true }).click()
  await expect(page.getByRole('button', { name: '退出登录', exact: true })).toBeVisible()
  // 登出时 hash 已被改写为 /login，重新登录后经通配路由落到 /overview（既有行为）。
  // 显式回到日志页再断言数据来自新请求而非上一会话缓存。
  await page.goto('/#/logs')
  await expect(page.getByText('Session 2 log')).toBeVisible()
  await expect.poll(() => session).toBeGreaterThanOrEqual(2)
})
