import { expect, test, type Page } from '@playwright/test'
import type { RuntimeConfig, RuntimeConfigUpdate, UsageLogRuntimeConfig } from '../src/lib/types'

const logConfig: UsageLogRuntimeConfig = {
  persistEnabled: true, retentionDays: 0, maxContentMB: 0, maxRecords: 0,
  bodyMaxKB: 0, bodyOnErrorOnly: true, externalizeMedia: false, cleanupIntervalMinutes: 60,
}

async function mockSettings(page: Page, usageLog: UsageLogRuntimeConfig = logConfig) {
  const current: RuntimeConfig = {
    host: '127.0.0.1', port: 8765, panelAccessToken: '', databasePath: '', defaultDatabasePath: '',
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
        if (patch.usageLog) current.usageLog = { ...current.usageLog, ...patch.usageLog }
        if (patch.systemLog) current.systemLog = { ...current.systemLog, ...patch.systemLog }
        data = { restartRequired: false }
      } else {
        data = current
      }
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
  await expect(capture).toBeChecked()
  await expect(limit).toHaveValue('256')
  await capture.click()
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect.poll(() => updates.length).toBe(2)
  expect(updates[1].usageLog).toMatchObject({ bodyMaxKB: 0, persistEnabled: true })
  await page.reload()
  await expect(capture).not.toBeChecked()
  await capture.click()
  await expect(limit).toHaveValue('1024')
})

test('existing capture settings survive toggling and unrelated saves leave log policy untouched', async ({ page }) => {
  const updates = await mockSettings(page, { ...logConfig, bodyMaxKB: 512 })
  await page.goto('/#/runtime')
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
