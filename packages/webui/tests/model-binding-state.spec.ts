import { expect, test, type Page } from '@playwright/test'
import type { ProtocolBindingEntry, ReasoningCapabilities } from '../src/lib/protocol-v2'

const original: ReasoningCapabilities = { kind: 'level', disable: true, levels: ['low', 'high'] }
const updated: ReasoningCapabilities = { kind: 'budget', disable: true, minBudget: 1024, maxBudget: 8192 }

async function mockBindings(page: Page, initial = true, hasPolicies = false) {
  let binding: ProtocolBindingEntry | undefined = initial ? {
    kind: 'model', sourceId: 'binding-source', modelId: 'private-model',
    binding: { protocolId: 'chat', revisionHash: 'chat-rev', reasoning: original },
  } : undefined
  let bindingJSON = binding ? JSON.stringify(binding) : undefined
  let failNext = false
  let pending: Promise<void> | undefined
  let attempts = 0
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-token'))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], total: 0 }
    if (path.endsWith('/protocols/bindings')) {
      if (route.request().method() === 'PUT') {
        attempts++
        const wait = pending
        pending = undefined
        await wait
        if (failNext) {
          failNext = false
          await route.fulfill({ status: 400, json: { ok: false, error: { message: 'binding validation failed' } } })
          return
        }
        binding = route.request().postDataJSON()
        bindingJSON = route.request().postData()!
        await route.fulfill({ contentType: 'application/json', body: `{"ok":true,"data":${bindingJSON}}` })
      } else await route.fulfill({ contentType: 'application/json', body: `{"ok":true,"data":${bindingJSON ? `[${bindingJSON}]` : '[]'}}` })
      return
    } else if (path.endsWith('/conversion-policies') && hasPolicies) {
      data = { items: ['policy-a', 'policy-b'].map((id) => ({ id, policy: { id, name: id, rules: [] } })) }
    } else if (path.endsWith('/protocols/enabled')) {
      data = { items: ['chat', 'responses'].map((id) => ({ id, name: id, revision: `${id}-rev`, canGenerate: true, capabilities: { 'tools.function': true } })) }
    } else if (path.endsWith('/revisions')) {
      const id = path.split('/').at(-2)
      data = { items: [{ hash: `${id}-rev`, definition: { operations: { generate: { kind: 'generate', transport: 'http' } } } }] }
    } else if (path.endsWith('/model-sources')) {
      data = { items: [{ id: 'binding-source', name: 'Binding source', baseUrl: 'https://upstream.example', platform: 'custom:chat', enabled: true }] }
    } else if (path.endsWith('/models')) {
      data = { items: [{ id: 'private-model', name: 'Private model', sourceId: 'binding-source', platform: 'custom:chat', type: 'llm', enabled: true, available: true, toolsCapable: true }] }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/sources')
  await page.getByRole('button', { name: '展开', exact: true }).click()
  await page.getByRole('button', { name: '编辑模型', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '编辑模型', exact: true })
  await expect(dialog.getByLabel('模型协议', { exact: true })).toBeEnabled()
  await dialog.getByText('转换行为覆盖', { exact: true }).click()
  return {
    dialog,
    saved: () => binding,
    savedJSON: () => bindingJSON,
    attempts: () => attempts,
    failNext: () => { failNext = true },
    holdNext: () => {
      let release!: () => void
      pending = new Promise<void>((resolve) => { release = resolve })
      return release
    },
    reopen: async () => {
      await page.keyboard.press('Escape')
      await expect(dialog).not.toBeVisible()
      await page.getByRole('button', { name: '编辑模型', exact: true }).click()
      await dialog.getByText('转换行为覆盖', { exact: true }).click()
    },
  }
}

test('late policy revisions do not replace a newer binding selection', async ({ page }) => {
  const state = await mockBindings(page, true, true)
  let release!: () => void
  const pending = new Promise<void>((resolve) => { release = resolve })
  await page.route('**/conversion-policies/*/revisions', async (route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-2)!
    if (id === 'policy-a') await pending
    await route.fulfill({ json: { ok: true, data: { items: [{ id, hash: `${id}-revision`, policy: { id, rules: [] } }] } } })
  })
  const policies = state.dialog.getByLabel('绑定转换策略', { exact: true })
  await policies.selectOption('policy-a')
  await policies.selectOption('policy-b')
  const revisions = state.dialog.getByLabel('绑定策略修订', { exact: true })
  await expect(revisions.locator('option')).toContainText(['选择不可变修订', 'policy-b-rev'])
  release()
  await expect(revisions.locator('option')).toHaveCount(2)
  await revisions.selectOption('policy-b-revision')
  await state.dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
  await expect(state.dialog.getByText('转换覆盖已验证并保存', { exact: true })).toBeVisible()
  expect(state.saved()?.conversion).toMatchObject({ policyId: 'policy-b', revisionHash: 'policy-b-revision' })
})

test('binding overrides keep exact values through child save, reopen and parent save', async ({ page }) => {
  const state = await mockBindings(page)
  const field = state.dialog.getByLabel('绑定转换覆盖 JSON')
  const overrides = '{"schemaVersion":1,"id":"precise","rules":[{"id":"large","value":9007199254740993}]}'
  await field.fill(overrides)
  await state.dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
  await expect(state.dialog.getByText('转换覆盖已验证并保存', { exact: true })).toBeVisible()
  expect(state.savedJSON()).toContain('9007199254740993')
  expect(state.savedJSON()).not.toContain('overridesJSON')
  await state.reopen()
  await expect(field).toHaveValue(overrides)
  await state.dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
  await expect(state.dialog.getByText('模型级绑定已验证并保存', { exact: true })).toBeVisible()
  expect(state.savedJSON()).toContain('9007199254740993')
})

for (const reasoning of [updated, undefined]) {
  test(`model binding retains ${reasoning ? 'saved' : 'cleared'} reasoning after parent save and reopen`, async ({ page }) => {
    const state = await mockBindings(page)
    const { dialog } = state
    const field = dialog.getByLabel('模型思考能力 JSON')
    await expect(field).toHaveValue(JSON.stringify(original, null, 2))
    await field.fill(reasoning ? JSON.stringify(reasoning) : '')
    const overrides = { schemaVersion: 1, id: 'local', name: 'Local', mode: 'strict', rules: [] }
    await dialog.getByLabel('绑定转换覆盖 JSON').fill(JSON.stringify(overrides))
    const releaseChild = state.holdNext()
    await dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
    await expect.poll(state.attempts).toBe(1)
    await expect(dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true })).toBeDisabled()
    releaseChild()
    await expect(dialog.getByText('转换覆盖已验证并保存', { exact: true })).toBeVisible()
    const releaseParent = state.holdNext()
    await dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
    await expect.poll(state.attempts).toBe(2)
    await expect(dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true })).toBeDisabled()
    releaseParent()
    await expect(dialog.getByText('模型级绑定已验证并保存', { exact: true })).toBeVisible()
    expect(state.saved()?.binding.reasoning).toEqual(reasoning)
    expect(state.saved()?.conversion?.overrides).toEqual(overrides)
    await state.reopen()
    await expect(field).toHaveValue(reasoning ? JSON.stringify(reasoning, null, 2) : '')
  })
}

test('creating a model binding and switching protocol reloads the child editor', async ({ page }) => {
  const state = await mockBindings(page, false)
  const { dialog } = state
  const field = dialog.getByLabel('模型思考能力 JSON')
  await expect(field).toBeDisabled()
  await dialog.getByLabel('模型协议', { exact: true }).selectOption('chat')
  await dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
  await expect(dialog.getByText('模型级绑定已验证并保存', { exact: true })).toBeVisible()
  await dialog.getByText('转换行为覆盖', { exact: true }).click()
  await expect(field).toBeEnabled()
  await field.fill(JSON.stringify(updated))
  await dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
  await expect(dialog.getByText('转换覆盖已验证并保存', { exact: true })).toBeVisible()
  await dialog.getByLabel('模型协议', { exact: true }).selectOption('responses')
  await dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
  await expect.poll(() => state.saved()?.binding.protocolId).toBe('responses')
  await dialog.getByText('转换行为覆盖', { exact: true }).click()
  await expect(field).toHaveValue('')
  await field.fill(JSON.stringify(original))
  await dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
  await expect(dialog.getByText('转换覆盖已验证并保存', { exact: true })).toBeVisible()
  expect(state.saved()?.binding.protocolId).toBe('responses')
  await state.reopen()
  await expect(field).toHaveValue(JSON.stringify(original, null, 2))
})

test('failed child save releases the lock and does not update the parent snapshot', async ({ page }) => {
  const state = await mockBindings(page)
  const { dialog } = state
  await expect(dialog.getByLabel('模型思考能力 JSON')).toHaveValue(JSON.stringify(original, null, 2))
  await dialog.getByLabel('模型思考能力 JSON').fill(JSON.stringify(updated))
  state.failNext()
  await dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
  await expect(dialog.getByRole('alert')).toContainText('binding validation failed')
  expect(state.saved()?.binding.reasoning).toEqual(original)
  await dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
  await expect(dialog.getByText('模型级绑定已验证并保存', { exact: true })).toBeVisible()
  await state.reopen()
  await expect(dialog.getByLabel('模型思考能力 JSON')).toHaveValue(JSON.stringify(original, null, 2))
})

test('failed parent save keeps the child draft and allows retry', async ({ page }) => {
  const state = await mockBindings(page)
  const { dialog } = state
  const field = dialog.getByLabel('模型思考能力 JSON')
  await expect(field).toHaveValue(JSON.stringify(original, null, 2))
  await field.fill(JSON.stringify(updated))
  state.failNext()
  await dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
  await expect(dialog.getByRole('alert')).toContainText('binding validation failed')
  await expect(field).toHaveValue(JSON.stringify(updated))
  await expect(field).toBeEnabled()
  await dialog.getByRole('button', { name: '验证并保存转换覆盖', exact: true }).click()
  await expect(dialog.getByText('转换覆盖已验证并保存', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: '验证并保存模型绑定', exact: true }).click()
  await expect(dialog.getByText('模型级绑定已验证并保存', { exact: true })).toBeVisible()
  await state.reopen()
  await expect(field).toHaveValue(JSON.stringify(updated, null, 2))
})
