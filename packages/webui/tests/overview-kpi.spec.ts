import { expect, test, type Page } from '@playwright/test'

async function mockAdmin(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'test-only-token')
    localStorage.setItem('elysia-webui.theme', 'light')
  })
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/seq') ? { seq: 1 }
      : /\/usage\/(trend|by-model|by-model-daily)$/.test(path) ? []
        : path.endsWith('/health') ? { status: 'ok', database: true, memory: { alloc: 0, sys: 0, numGC: 0 } }
          : { items: [], total: 0 }
    await route.fulfill({ json: { ok: true, data } })
  })
}

/** /usage/stats 按「无 from = 累计全量 / 有 from = 今日/昨日/上一分钟窗口」应答两套口径。 */
function statsFixture(requests: number, firstUsedAt: string) {
  return {
    requests,
    success: requests,
    failed: 0,
    inputTokens: 1000,
    outputTokens: 2000,
    totalTokens: 3000,
    cacheHitTokens: 500,
    cacheHitRate: 0.5,
    cacheHitRateReliable: true,
    avgDurationMs: 120,
    avgFirstByteMs: 30,
    firstUsedAt,
    lastUsedAt: '2026-10-18T08:00:00Z',
  }
}

test.beforeEach(async ({ page }) => {
  await mockAdmin(page)
  await page.route('**/api/admin/usage/stats*', async (route) => {
    const from = new URL(route.request().url()).searchParams.get('from')
    await route.fulfill({
      json: { ok: true, data: statsFixture(from ? 42 : 12345, from ? '' : '2026-09-01T12:00:00Z') },
    })
  })
})

test('overview KPI defaults to cumulative, toggles to today, and remembers the choice across reloads', async ({ page }) => {
  await page.goto('/#/overview')
  const seg = page.getByRole('group', { name: '核心用量时间窗' })

  // 默认累计档：hero 展示累计请求数与首次使用日期，隔天进入不再归零。
  await expect(page.getByText('累计请求总数', { exact: true })).toBeVisible()
  await expect(page.getByText('12,345', { exact: true })).toBeVisible()
  await expect(page.getByText('自 2026/09/01 起', { exact: true })).toBeVisible()
  await expect(seg.getByRole('button', { name: '累计', exact: true })).toHaveAttribute('aria-pressed', 'true')

  // 切到今日档：数值换为窗口口径，标签同步。
  await seg.getByRole('button', { name: '今日', exact: true }).click()
  await expect(page.getByText('今日请求总数', { exact: true })).toBeVisible()
  await expect(page.getByText('12,345', { exact: true })).not.toBeVisible()
  await expect(seg.getByRole('button', { name: '今日', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('elysia-webui.overview-kpi-range')))
    .toBe('today')

  // 刷新后保持今日档（localStorage 记忆）。
  await page.reload()
  await expect(page.getByText('今日请求总数', { exact: true })).toBeVisible()
  await expect(page.getByText('12,345', { exact: true })).not.toBeVisible()
})
