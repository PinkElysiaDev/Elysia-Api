import { expect, test, type Page } from '@playwright/test'
import type { ApiToken } from '../src/lib/types'

const remoteKey: ApiToken = {
  name: 'remote-test', token: 'masked', enabled: true,
  scopes: ['agent'], allowedGroups: ['agent'],
}

async function mockTokenAPI(page: Page, initial: ApiToken[]) {
  const tokens = initial.map((token) => ({ ...token }))
  const created: ApiToken[] = []
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'test-only-panel-token')
  })
  await page.route('**/api/admin/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    let data: unknown = { items: [], total: 0 }
    if (path.endsWith('/api-tokens')) {
      if (request.method() === 'POST') {
        const token = request.postDataJSON() as ApiToken
        created.push(token)
        tokens.push({ ...token, token: 'masked' })
        data = tokens[tokens.length - 1]
      } else {
        data = { items: tokens }
      }
    } else if (path.endsWith('/runtime-config')) {
      data = {
        host: '127.0.0.1', port: 8765, panelAccessToken: 'masked',
        databasePath: '', defaultDatabasePath: '', logLevel: 'info',
        httpTimeout: 120, enablePprof: false,
        agentRemote: { enabled: true, publicUrl: '' },
        usageLog: { persistEnabled: true, retentionDays: 7, maxContentMB: 0, maxRecords: 0,
          bodyMaxKB: 0, bodyOnErrorOnly: true, externalizeMedia: false, cleanupIntervalMinutes: 5 },
        systemLog: { retentionDays: 0, maxRecords: 0, maxContentMB: 0 },
      }
    } else if (path.endsWith('/model-groups')) {
      data = { items: [{ id: 'agent', name: 'agent', enabled: true, models: [] }] }
    } else if (path.endsWith('/usage/storage')) {
      data = null
    } else if (path.endsWith('/usage/maintenance')) {
      data = { state: 'idle', phase: 'idle' }
    } else if (path.endsWith('/seq')) {
      data = { seq: 1 }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  return { tokens, created }
}

test('a remote key stays in runtime settings after creation and does not appear among inference tokens', async ({ page }) => {
  const { tokens, created } = await mockTokenAPI(page, [
    { name: 'legacy-relay', token: 'masked', enabled: true, allowedGroups: [] },
    { name: 'group-agent-relay', token: 'masked', enabled: true, scopes: [], allowedGroups: ['agent'] },
  ])
  await page.goto('/#/runtime')
  await page.getByRole('tab', { name: '远程访问', exact: true }).click()
  await page.getByPlaceholder('新 Key 名称（如 cursor、ops-agent）').fill('remote-test')
  await page.getByRole('tab', { name: '基础设置', exact: true }).click()
  await page.getByRole('tab', { name: '远程访问', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '新 Key 名称', exact: true })).toHaveValue('remote-test')
  await page.getByRole('button', { name: '新建', exact: true }).click()
  await expect(page.getByRole('button', { name: '复制 remote-test 的 MCP JSON' })).toBeVisible()
  await expect(page.getByText('有未保存的修改', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '重命名远程 Key remote-test', exact: true }).click()
  await page.getByRole('textbox', { name: '远程 Key 新名称', exact: true }).fill('rename-draft')
  await page.getByRole('tab', { name: '基础设置', exact: true }).click()
  await page.getByRole('tab', { name: '远程访问', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '远程 Key 新名称', exact: true })).toHaveValue('rename-draft')
  await page.getByRole('button', { name: '取消', exact: true }).click()
  expect(created).toHaveLength(1)
  expect(created[0].scopes).toEqual(['agent'])
  expect(tokens.filter((token) => token.name === 'remote-test')).toHaveLength(1)

  await page.getByRole('link', { name: '访问令牌', exact: true }).click()
  await expect(page.getByRole('cell', { name: 'legacy-relay', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: 'group-agent-relay', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: 'remote-test', exact: true })).toHaveCount(0)
  await page.reload()
  await expect(page.locator('tbody tr')).toHaveCount(2)

  await page.getByRole('link', { name: '运行配置', exact: true }).click()
  await page.getByRole('tab', { name: '远程访问', exact: true }).click()
  await expect(page.getByRole('button', { name: '复制 remote-test 的 MCP JSON' })).toBeVisible()
})

test('only remote keys leave the inference-token page in its empty state', async ({ page }) => {
  await mockTokenAPI(page, [remoteKey, { ...remoteKey, name: 'disabled-remote', enabled: false }])
  await page.goto('/#/tokens')
  await expect(page.getByText('暂无任何访问令牌', { exact: true })).toBeVisible()
  await expect(page.locator('tbody tr')).toHaveCount(0)
  await page.getByRole('link', { name: '运行配置', exact: true }).click()
  await page.getByRole('tab', { name: '远程访问', exact: true }).click()
  await expect(page.getByRole('button', { name: '复制 remote-test 的 MCP JSON' })).toBeEnabled()
  await expect(page.getByRole('button', { name: '复制 disabled-remote 的 MCP JSON' })).toBeDisabled()
})
