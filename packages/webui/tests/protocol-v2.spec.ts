import { expect, test } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { ProtocolDocument } from '../src/lib/protocol-document'

test('designer renders the published listing contract without migration metadata', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-only'))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], seq: 1 }
    if (path.endsWith('/schema')) data = {}
    if (path.endsWith('/protocols')) data = {
      drafts: [{ protocolId: 'my-protocol', hash: 'draft', definition: { id: 'my-protocol' }, updatedAt: '2026-10-08T00:00:00Z' }],
      active: [
        { protocolId: 'openai-chat-completions', revisionHash: 'active' },
        { protocolId: 'google-generate-content', revisionHash: 'pending' },
        { protocolId: 'my-protocol', revisionHash: 'pending' },
      ],
      loaded: { 'openai-chat-completions': 'active' },
      presets: ['openai-chat-completions', 'google-generate-content'],
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols')
  await expect(page.getByRole('row').filter({ hasText: 'openai-chat-completions' })).toContainText('已启用')
  await expect(page.getByRole('row').filter({ hasText: 'google-generate-content' })).toContainText('待恢复')
  await expect(page.getByRole('row').filter({ hasText: 'my-protocol' })).toContainText('需重新验证或修复')
  expect(errors).toEqual([])
})

test('Agent edit session opens exact v2 draft in the editor', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires an isolated real backend')
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-protocol-e2e'
  const fixture = JSON.parse(readFileSync(resolve('../../backend/protocol/testdata/text-alpha.json'), 'utf8'))
  fixture.id = `agent-browser-${Date.now()}`
  fixture.family = fixture.id
  const source = new ProtocolDocument(JSON.stringify(fixture)).set('/extensions', '{"long":900719925474099312345}')
  const saved = await request.put(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/${fixture.id}/draft`, { headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, data: source })
  expect(saved.ok()).toBeTruthy()
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`/#/agent?mode=edit&protocol=${fixture.id}`)
  await page.getByRole('button', { name: '协议草稿', exact: true }).click()
  await page.getByRole('button', { name: '在协议设计器中打开' }).click()
  await page.getByRole('tab', { name: '完整 JSON', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '完整协议 JSON', exact: true })).toHaveValue(source)
})

test('raw protocol edits preserve presence, numeric spelling and ordered extensions', () => {
  const source = '{"name":"first","long":900719925474099312345,"negative":-0,"exp":1e99,"array":[null,false,0,{},[]],"nested":{"a/b":{"~x":null}}}'
  const document = new ProtocolDocument(source)
  const updated = document.set('/name', '"second"')
  expect(updated).toBe(source.replace('first', 'second'))
  expect(document.read('/nested/a~1b/~0x')).toBe('null')
  expect(new ProtocolDocument(document.set('/nested/new', 'false')).read('/nested/new')).toBe('false')
  for (const invalid of ['{"x":1,"x":2}', '{"x":}', '[1,]', '{"x":01}', '{}{}', '['.repeat(66) + ']'.repeat(66)]) expect(() => new ProtocolDocument(invalid)).toThrow()
})

test('browser creates, verifies, previews, activates and rolls back through the real protocol service', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'Requires an isolated local backend and ELYSIA_DEV_PROXY')
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-protocol-e2e'
  const id = `browser-${Date.now()}`
  const fixture = JSON.parse(readFileSync(resolve('../../backend/protocol/testdata/text-alpha.json'), 'utf8'))
  fixture.id = id
  fixture.name = 'Browser draft'
  fixture.family = id
  let source = JSON.stringify(fixture, null, 2)
  source = new ProtocolDocument(source).set('/extensions', '{"long":900719925474099312345,"values":[null,false,0,{},[]],"custom":{"nested":"preserve"}}')
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto('/#/protocols')
  await page.getByRole('button', { name: '新建协议', exact: true }).click()
  await page.getByRole('tab', { name: '完整 JSON', exact: true }).click()
  await page.getByRole('textbox', { name: '完整协议 JSON', exact: true }).fill(source)
  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await page.getByRole('textbox', { name: '名称', exact: true }).fill('Edited by form')
  await page.getByRole('tab', { name: '扩展元数据', exact: true }).click()
  const extension = page.getByRole('textbox', { name: '扩展元数据 JSON', exact: true })
  const original = await extension.inputValue()
  await extension.fill('{"invalid":')
  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await expect(page.getByRole('button', { name: '保存草稿', exact: true })).toBeDisabled()
  await page.getByRole('tab', { name: '扩展元数据', exact: true }).click()
  await expect(extension).toHaveValue('{"invalid":')
  await extension.fill(original)
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect(page.getByRole('button', { name: '离线验证', exact: true })).toBeEnabled()
  const saved = await request.get(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/${id}/draft`, { headers: { Authorization: `Bearer ${token}` } })
  expect(await saved.text()).toContain('900719925474099312345')
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '启用版本', exact: true }).click()
  await expect(page.getByText(/启用版本 [a-f0-9]{12}/)).toBeVisible()

  await page.getByRole('tab', { name: '转换预览', exact: true }).click()
  await page.getByRole('textbox', { name: '预览输入 JSON', exact: true }).fill(JSON.stringify(fixture.samples[0].input))
  await page.getByRole('button', { name: '运行转换预览', exact: true }).click()
  await expect(page.getByLabel('转换链路结果')).toContainText('hello')
  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await page.getByRole('checkbox', { name: 'tools.function', exact: true }).check()
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByLabel('协议诊断')).toContainText('incomplete_coverage')
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeDisabled()
  await page.getByLabel('协议诊断').getByRole('button').first().click()
  await expect(page.getByRole('tab', { name: '完整 JSON', exact: true })).toHaveAttribute('aria-selected', 'true')

  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await page.getByRole('checkbox', { name: 'tools.function', exact: true }).uncheck()
  await page.getByRole('textbox', { name: '定义版本', exact: true }).fill('2')
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '启用版本', exact: true }).click()
  await page.getByRole('tab', { name: '版本与回滚', exact: true }).click()
  const choices = await page.getByRole('combobox', { name: '选择修订', exact: true }).locator('option').allTextContents()
  expect(choices.length).toBe(4)
  const revisionResponse = await request.get(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/${id}/revisions`, { headers: { Authorization: `Bearer ${token}` } })
  const revisions = (await revisionResponse.json()).data.items
  const first = revisions.find((revision: { definition: { version: string; capabilities: Record<string, boolean> } }) => revision.definition.version === '1' && !revision.definition.capabilities['tools.function'])
  await page.getByRole('combobox', { name: '选择修订', exact: true }).selectOption(first.hash)
  await page.getByRole('button', { name: '比较启用版本', exact: true }).click()
  await expect(page.getByText('"/version"', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '验证并回滚至所选修订', exact: true }).click()
  await expect(page.getByText(new RegExp(`启用版本 ${first.hash.slice(0, 12)}`))).toBeVisible()
})
