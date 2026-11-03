import { expect, test, type Page } from '@playwright/test'
import type { ModelSource } from '../src/lib/types'

async function mockDiscovery(page: Page, apiKeys: ModelSource['apiKeys'], manual = false, models: string[] = []) {
  let source: ModelSource = {
    id: 'discovery', name: 'Discovery source', baseUrl: 'https://upstream.example/v1',
    platform: 'custom:openai-chat-completions', enabled: true, autoFetchModels: !manual,
    manualModels: manual ? [{ id: 'alpha', name: 'alpha' }] : [],
    apiKeys, keyStrategy: 'round-robin',
  }
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-token'))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], total: 0 }
    if (path.endsWith('/protocols/enabled')) {
      data = { items: [{ id: 'openai-chat-completions', name: 'OpenAI', canGenerate: true, hasModelDiscovery: true }] }
    } else if (path.endsWith('/model-sources/discovery') && route.request().method() === 'PUT') {
      source = route.request().postDataJSON()
      data = source
    } else if (path.endsWith('/model-sources/discovery/fetch')) {
      source.refreshState = {
        refreshing: false, lastError: '1/2 keys failed to fetch models', lastFinishedAt: '2026-10-08T08:00:00.123Z',
        lastKeys: [{ index: 0, count: 2 }, { index: 1, note: 'backup', count: 0, error: 'HTTP 503' }],
      }
      data = { started: true }
    } else if (path.endsWith('/model-sources')) {
      data = { items: [source] }
    } else if (path.endsWith('/models')) {
      data = { items: models.map((id) => ({ id, name: id, sourceId: source.id, platform: source.platform, type: 'llm', enabled: true, available: true, maxTokens: 0 })) }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/sources')
  await expect(page.getByText('Discovery source', { exact: true })).toBeVisible()
  return { saved: () => source }
}

for (const count of [1, 2]) {
  test(`automatic source with ${count} keys saves discovery metadata`, async ({ page }) => {
    const keys = Array.from({ length: count }, (_, index) => ({
      value: `key-${index}`, fetchedModels: ['alpha', 'beta'], allowedModels: ['alpha'],
    }))
    const state = await mockDiscovery(page, keys)
    await page.getByTitle('编辑', { exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '编辑模型源' })
    await expect(dialog.getByText('API Keys', { exact: true })).toBeVisible()
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    expect(state.saved().autoFetchModels).toBe(true)
    expect(state.saved().apiKeys).toEqual(keys.map((key) => ({ ...key, allowedModels: null })))
  })
}

test('manual source retains model key assignment', async ({ page }) => {
  const state = await mockDiscovery(page, [
    { value: 'a', allowedModels: ['alpha'] },
    { value: 'b', allowedModels: [] },
  ], true)
  await page.getByTitle('编辑', { exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑模型源' })
  await expect(dialog.getByText('可用 Key', { exact: true })).toBeVisible()
  const first = dialog.getByRole('button', { name: 'Key 1', exact: true })
  const second = dialog.getByRole('button', { name: 'Key 2', exact: true })
  await expect(first).toHaveAttribute('aria-pressed', 'true')
  await expect(second).toHaveAttribute('aria-pressed', 'false')
  await second.click()
  await first.click()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(state.saved().apiKeys?.map((key) => key.allowedModels)).toEqual([[], ['alpha']])
})

for (const count of [1, 2]) {
  test(`manual source preserves explicit empty permissions with ${count} keys`, async ({ page }) => {
    const keys = Array.from({ length: count }, (_, index) => ({ value: `key-${index}`, allowedModels: [] }))
    const state = await mockDiscovery(page, keys, true)
    await page.getByTitle('编辑', { exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '编辑模型源' })
    for (let index = 0; index < count; index++) {
      await expect(dialog.getByRole('button', { name: `Key ${index + 1}`, exact: true })).toHaveAttribute('aria-pressed', 'false')
    }
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    expect(state.saved().apiKeys).toEqual(keys)
    await page.getByTitle('编辑', { exact: true }).click()
    await expect(dialog.getByRole('button', { name: 'Key 1', exact: true })).toHaveAttribute('aria-pressed', 'false')
  })
}

test('unrestricted and explicitly restricted keys initialize independently', async ({ page }) => {
  await mockDiscovery(page, [
    { value: 'unrestricted', allowedModels: null },
    { value: 'denied', allowedModels: [] },
    { value: 'allowed', allowedModels: ['alpha'] },
  ], true)
  await page.getByTitle('编辑', { exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑模型源' })
  for (const [index, selected] of [true, false, true].entries()) {
    await expect(dialog.getByRole('button', { name: `Key ${index + 1}`, exact: true })).toHaveAttribute('aria-pressed', String(selected))
  }
})

test('key reordering and blank row removal keep permissions with their credentials', async ({ page }) => {
  const state = await mockDiscovery(page, [
    { value: 'selected', allowedModels: ['alpha'] },
    { value: '', allowedModels: [] },
    { value: 'denied', allowedModels: [] },
  ], true)
  await page.getByTitle('编辑', { exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑模型源' })
  await dialog.getByTitle('下移', { exact: true }).first().click()
  await expect(dialog.getByRole('button', { name: 'Key 2', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await dialog.getByTitle('删除', { exact: true }).first().click()
  await dialog.getByTitle('下移', { exact: true }).first().click()
  await expect(dialog.getByRole('button', { name: 'Key 1', exact: true })).toHaveAttribute('aria-pressed', 'false')
  await expect(dialog.getByRole('button', { name: 'Key 2', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(state.saved().apiKeys).toEqual([
    { value: 'denied', allowedModels: [] },
    { value: 'selected', allowedModels: ['alpha'] },
  ])
})

test('removing the selected key does not authorize the remaining key', async ({ page }) => {
  const state = await mockDiscovery(page, [
    { value: 'selected', allowedModels: ['alpha'] },
    { value: 'denied', allowedModels: [] },
  ], true)
  await page.getByTitle('编辑', { exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑模型源' })
  await dialog.getByTitle('删除', { exact: true }).first().click()
  await expect(dialog.getByRole('button', { name: 'Key 1', exact: true })).toHaveAttribute('aria-pressed', 'false')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(state.saved().apiKeys).toEqual([{ value: 'denied', allowedModels: [] }])
})

test('failed refresh reports the failing key and retained data', async ({ page }) => {
  await mockDiscovery(page, [{ value: 'a' }, { value: 'b', note: 'backup' }])
  await page.getByTitle('拉取模型', { exact: true }).click()
  await expect(page.getByText('拉取失败，保留原数据', { exact: true })).toBeVisible()
  await expect(page.getByText("Discovery source：1/2 keys failed to fetch models；Key 2（backup）：HTTP 503", { exact: true })).toBeVisible()
  await expect(page.getByText('拉取完成', { exact: true })).not.toBeVisible()
})

test('source selection uses the same visible models as both key-group filters', async ({ page }) => {
  await mockDiscovery(page, [
    { value: 'one', fetchedModels: ['alpha', 'shared'] },
    { value: 'two', fetchedModels: ['beta', 'shared'] },
  ], false, ['alpha', 'beta', 'shared'])
  await page.getByRole('button', { name: '展开', exact: true }).click()
  const groupSearch = page.getByPlaceholder('组内搜索…')
  await groupSearch.first().fill('alpha')
  await page.getByPlaceholder('跨组搜索全部模型…').fill('shared')
  await expect(page.getByRole('button', { name: 'shared', exact: true })).toHaveCount(1)
  await expect(page.getByText('没有匹配的模型', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '全选', exact: true }).click()
  await expect(page.getByText('已选 1', { exact: true })).toBeVisible()
  await page.getByPlaceholder('跨组搜索全部模型…').fill('')
  await groupSearch.first().fill('')
  await expect(page.getByRole('button', { name: 'shared', exact: true })).toHaveCount(2)
  await page.getByRole('button', { name: '全选', exact: true }).click()
  await expect(page.getByText('已选 3', { exact: true })).toBeVisible()
})
