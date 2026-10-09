import { expect, test } from '@playwright/test'
import type { ProtocolBindingEntry } from '../src/lib/protocol-v2'

const date = '2026-10-06T12:00:00Z'
const old = { id: 'anthropic-api~abc123456789', protocolId: 'anthropic-api', name: 'Anthropic 旧版本', hash: 'abc123456789', version: '2.0', reason: 'preset_replaced', archivedAt: date, createdAt: date, isDraft: false }
const custom = { ...old, id: 'my-protocol~def123456789', protocolId: 'my-protocol', name: '已删除协议', hash: 'def123456789', reason: 'custom_deleted' }

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'test-only')
    localStorage.setItem('elysia-webui.theme', 'light')
  })
})

test('damaged preset archives display the original text and parse diagnosis', async ({ page }) => {
  const damaged = { ...old, id: 'anthropic-messages~damaged', protocolId: 'anthropic-messages', reason: 'preset_repaired', readError: 'invalid JSON value' }
  const rawDefinition = '{broken raw definition'
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/history') ? { items: [damaged] }
      : path.includes('/history/') ? { item: { ...damaged, rawDefinition }, references: [] }
      : { items: [], seq: 1 }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols/history')
  await expect(page.getByText('预置损坏原文')).toBeVisible()
  await page.getByRole('button', { name: '查看版本' }).click()
  await expect(page.getByRole('alert')).toContainText('invalid JSON value')
  await expect(page.getByRole('region', { name: '历史版本详情' }).locator('pre')).toHaveText(rawDefinition)
})

test('history filters, restores a new ID and requires irreversible-delete confirmation', async ({ page }) => {
  let items = [old, custom]
  let restores = 0
  let deletes = 0
  await page.route('**/api/admin/**', async (route) => {
    const path = decodeURIComponent(new URL(route.request().url()).pathname)
    let data: unknown = { items: [], total: 0 }
    if (path.endsWith('/seq')) data = { seq: 1 }
    else if (path.endsWith('/history')) data = { items }
    else if (path.endsWith('/restore')) {
      restores++
      expect(route.request().postDataJSON()).toMatchObject({ id: 'anthropic-api-restored-abc12345' })
      data = { protocolId: 'anthropic-api-restored-abc12345', activated: true }
    } else if (path.includes('/history/')) {
      const item = items.find((item) => path.endsWith(item.id))!
      if (route.request().method() === 'DELETE') {
        deletes++
        items = items.filter((entry) => entry.id !== item.id)
        data = { deleted: true }
      } else data = { item: { ...item, definition: { id: item.protocolId, version: item.version } }, references: [], changes: [{ path: '/version', before: '2.0', after: '2.1' }], report: { definitionHash: item.hash, passed: true, compilerVersion: 'test', issues: [] } }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols/history')
  await expect(page.getByRole('button', { name: '查看版本' })).toHaveCount(2)
  await page.getByRole('group', { name: '历史类型' }).getByRole('button', { name: '预置旧版本', exact: true }).click()
  await expect(page.getByRole('button', { name: '查看版本' })).toHaveCount(1)
  await page.getByRole('button', { name: '查看版本' }).click()
  await expect(page.getByRole('region', { name: '历史版本详情' })).toContainText('恢复时会重新验证')
  await page.getByRole('button', { name: '恢复为新协议' }).click()
  await expect(page.getByLabel('新协议 ID')).toHaveValue('anthropic-api-restored-abc12345')
  await page.getByRole('button', { name: '创建并验证启用' }).click()
  await expect(page.getByRole('status')).toContainText('已创建并启用')
  expect(restores).toBe(1)
  await expect(page.getByRole('button', { name: '查看版本' })).toHaveCount(1)
  await page.getByRole('button', { name: '彻底删除', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('此操作不可恢复')
  await expect(page.getByRole('button', { name: '确认彻底删除' })).toBeDisabled()
  await page.getByLabel('确认删除协议 ID').fill('anthropic-api')
  await page.getByRole('button', { name: '确认彻底删除' }).click()
  await expect(page.getByRole('status')).toContainText('已彻底删除')
  expect(deletes).toBe(1)
})

test('history shows dependency blockers and keeps failed restoration editable', async ({ page }) => {
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/history') ? { items: [custom] }
      : path.endsWith('/restore') ? { protocolId: 'repair-copy', activated: false, issues: [{ path: '/directions', reason: '缺少响应映射' }] }
      : path.includes('/history/') ? { item: { ...custom, definition: { id: custom.protocolId } }, references: [{ kind: 'job', id: 'pending-job' }] }
      : { items: [], seq: 1 }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols/legacy')
  await expect(page).toHaveURL(/protocols\/history/)
  await page.getByRole('button', { name: '查看版本' }).click()
  await expect(page.getByText('以下引用阻止彻底删除')).toBeVisible()
  await expect(page.getByRole('button', { name: '彻底删除', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '恢复为新协议' }).click()
  await page.getByLabel('新协议 ID').fill('repair-copy')
  await page.getByRole('button', { name: '创建并验证启用' }).click()
  await expect(page.getByRole('status')).toContainText('已保存新草稿 repair-copy')
  await expect(page.getByRole('status')).toContainText('/directions 缺少响应映射')
  await expect(page.getByRole('button', { name: '查看新协议' })).toBeVisible()
})

test('archive previews references and refreshes the state digest after a conflict', async ({ page }) => {
  let baseline = 'initial'
  let attempts = 0
  let archived = false
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], seq: 1 }
    if (path.endsWith('/protocols')) data = { drafts: archived ? [] : [{ protocolId: 'my-protocol', hash: 'hash', definition: { id: 'my-protocol' }, updatedAt: date }], active: [], loaded: {}, presets: [] }
    else if (path.endsWith('/schema')) data = {}
    else if (path.endsWith('/enabled')) data = { items: [{ id: 'replacement', name: '替换协议' }] }
    else if (path.endsWith('/references')) data = { baseline, references: [{ kind: 'source', sourceId: 's', id: '', name: '我的模型源' }], affectedModels: [{ kind: 'model', sourceId: 's', id: 'm' }] }
    else if (path.endsWith('/archive')) {
      attempts++
      expect(route.request().postDataJSON()).toMatchObject({ baseline, mode: 'unbind' })
      if (attempts === 1) {
        baseline = 'refreshed'
        await route.fulfill({ status: 409, json: { ok: false, error: { code: 'revision_conflict', message: '配置已变化，请重试' } } })
        return
      }
      archived = true
      data = { archived: true }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols')
  await page.getByRole('button', { name: '删除 my-protocol' }).click()
  await expect(page.getByRole('dialog')).toContainText('我的模型源')
  await expect(page.getByRole('button', { name: '删除并归档' })).toBeDisabled()
  await page.getByLabel('引用处理').selectOption('replace')
  await expect(page.getByRole('button', { name: '替换绑定并归档' })).toBeDisabled()
  await page.getByLabel('替换协议').selectOption('replacement')
  await expect(page.getByRole('button', { name: '替换绑定并归档' })).toBeEnabled()
  await page.getByLabel('引用处理').selectOption('unbind')
  await expect(page.getByRole('dialog')).toContainText('模型源与模型记录保留')
  await page.getByRole('button', { name: '取消绑定并归档' }).click()
  await expect(page.getByRole('alert')).toContainText('配置已变化')
  await page.getByRole('button', { name: '取消绑定并归档' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '删除 my-protocol' })).toHaveCount(0)
})

test('saved Agent session explains separate capabilities and only repairs after a successful probe', async ({ page }) => {
  const session = { id: 'saved', title: '已有会话', status: 'idle', mode: 'create', createdAt: date, updatedAt: date, settings: { modelSourceId: 's', modelName: 'Grok 4.7' } }
  let available = false
  let probes = 0
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], seq: 1 }
    if (path.endsWith('/agent/models/verify-tools')) {
      probes++
      expect(route.request().postDataJSON()).toEqual({ sourceId: 's', modelId: 'grok' })
      if (probes === 1) {
        await route.fulfill({ status: 400, json: { ok: false, error: { code: 'tool_probe_failed', message: '未返回有效工具调用，未修改配置' } } })
        return
      }
      available = true
    } else if (path.endsWith('/agent/models')) data = { items: [{ sourceId: 's', modelId: 'grok', modelName: 'Grok 4.7', available, modelTools: available, bindingTools: true, bindingKind: 'source', capabilitySource: 'manual', protocolId: 'anthropic-api-copy', revision: 'abc123456789', canRepair: !available, reason: available ? '' : '模型未声明工具调用能力' }] }
    else if (path.endsWith('/models')) data = { items: [{ id: 'grok', sourceId: 's', name: 'Grok 4.7', enabled: true, type: 'llm' }] }
    else if (path.endsWith('/model-sources')) data = { items: [{ id: 's', name: '上游', enabled: true }] }
    else if (path.endsWith('/agent/sessions')) data = { items: [session] }
    else if (path.endsWith('/sessions/saved')) data = { session, messages: [] }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/agent')
  await page.getByRole('button', { name: /^已有会话 空对话/ }).click({ position: { x: 15, y: 15 } })
  await expect(page.getByText('模型未声明工具调用能力', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '验证并启用工具调用' }).click()
  const dialog = page.getByRole('dialog', { name: '验证并启用工具调用' })
  await expect(dialog).toContainText('模型工具能力：未开启')
  await expect(dialog).toContainText('绑定工具能力：已开启')
  expect(probes).toBe(0)
  await dialog.getByRole('button', { name: '开始验证并修复' }).click()
  await expect(dialog.getByRole('alert')).toContainText('未修改配置')
  await dialog.getByRole('button', { name: '开始验证并修复' }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText('模型未声明工具调用能力', { exact: true })).toHaveCount(0)
  expect(probes).toBe(2)
})

test('an explicitly unbound model can be rebound without changing its source', async ({ page }) => {
  let binding: ProtocolBindingEntry = { kind: 'model', sourceId: 's', modelId: 'm', unbound: true, binding: {} }
  let saved = 0
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], seq: 1 }
    if (path.endsWith('/model-sources')) data = { items: [{ id: 's', name: '我的模型源', enabled: true, platform: '', apiKeys: [], manualModels: [] }] }
    else if (path.endsWith('/models')) data = { items: [{ id: 'm', sourceId: 's', sourceName: '我的模型源', name: '测试模型', enabled: true, available: true, type: 'llm', toolsCapable: false, visionCapable: false }] }
    else if (path.endsWith('/enabled')) data = { items: [{ id: 'restored', name: '恢复的协议', revision: 'rev', capabilities: { text: true, 'tools.function': true, images: true }, canGenerate: true }] }
    else if (path.endsWith('/bindings')) {
      if (route.request().method() === 'PUT') {
        saved++
        binding = route.request().postDataJSON()
        expect(binding).toMatchObject({ unbound: false, kind: 'model', sourceId: 's', modelId: 'm', binding: { protocolId: 'restored', revisionHash: 'rev', capabilities: { text: true }, transports: ['http_json'] } })
        expect(binding.binding.capabilities).toEqual({ text: true })
        data = binding
      } else data = [binding, { kind: 'source', sourceId: 's', unbound: true, binding: {} }]
    } else if (path.endsWith('/revisions')) data = { items: [{ hash: 'rev', protocolId: 'restored', definition: { operations: { generate: { kind: 'generate', transport: 'http_json' } } } }] }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/sources')
  await page.getByRole('button', { name: '展开', exact: true }).click()
  await page.getByRole('button', { name: '编辑模型', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑模型' })
  await expect(dialog.getByLabel('模型协议')).toHaveValue('')
  await dialog.getByLabel('模型协议').selectOption('restored')
  await dialog.getByRole('button', { name: '验证并保存模型绑定' }).click()
  await expect(dialog.getByRole('status')).toContainText('模型级绑定已验证并保存')
  expect(saved).toBe(1)
})
