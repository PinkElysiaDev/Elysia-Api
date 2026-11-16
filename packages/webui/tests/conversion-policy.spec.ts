import { expect, test } from '@playwright/test'
import { decodeProtocolRequest, decodeProtocolResponse, readActiveProtocolPackage } from './protocol-fixtures'

test('real backend include and usage projections share preview and strict boundaries', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-conversion-e2e'
  const headers = { Authorization: `Bearer ${token}` }
  const sourcePackage = await readActiveProtocolPackage(request, base, headers, 'openai-responses')
  const requestCore = await decodeProtocolRequest(request, base, headers, sourcePackage, {
    model: 'm', input: 'hello', store: false, include: ['reasoning.encrypted_content'],
  })
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await expect(page.getByRole('heading', { name: '转换行为', exact: true })).toBeVisible()
  await page.getByLabel('行为预览上下文').fill(JSON.stringify({ source: { definitionId: 'openai-responses' }, target: { definitionId: 'google-generate-content' } }))
  await page.getByLabel('行为预览输入').fill(JSON.stringify(requestCore))
  await page.getByRole('button', { name: '预览行为', exact: true }).click()
  const preview = page.getByLabel('行为预览结果')
  await expect(preview).toContainText('requireContinuationState')
  const result = JSON.parse((await preview.textContent())!)
  expect(requestCore.clientOutput.requireContinuationState).toBe(true)
  expect(result.output.clientOutput.requireContinuationState).toBe(true)
  expect(result.persistentWrites).toBe(false)
  expect(result.issues ?? []).toEqual([])
  const policy = { schemaVersion: 2, id: 'strict-preview', mode: 'strict', rules: [] }
  const context = { source: { definitionId: 'google-generate-content' }, target: { definitionId: 'anthropic-messages' } }
  const input = { schemaVersion: 2, source: {}, content: [], usage: { input: { count: 4, origin: 'observed' }, output: { count: 5, origin: 'observed' }, details: { 'output.reasoning_tokens': { count: 3, origin: 'observed' } } } }
  const preserved = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: { policy, context, input, phase: 'response' } })
  expect(preserved.ok()).toBeTruthy()
  expect((await preserved.json()).data.output.usage.details['output.reasoning_tokens']).toEqual(input.usage.details['output.reasoning_tokens'])
  const lossyInput = { ...input, usage: { ...input.usage, details: { ...input.usage.details, toolUsePromptTokenCount: { count: 1, origin: 'observed' } } } }
  const strict = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: { policy, context, input: lossyInput, phase: 'response' } })
  expect(strict.status()).toBe(400)
  expect((await strict.json()).error.issues).toEqual(expect.arrayContaining([expect.objectContaining({ ruleId: 'response-usage-projection', path: '/usage/details/toolUsePromptTokenCount', fidelity: 'rejected' })]))
  const compatible = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: { policy: { ...policy, mode: 'compatible' }, context, input: lossyInput, phase: 'response' } })
  expect(compatible.ok()).toBeTruthy()
  const converted = (await compatible.json()).data
  expect(converted.output.usage.output.count).toBe(5)
  expect(converted.output.usage.details['output.reasoning_tokens']).toEqual(input.usage.details['output.reasoning_tokens'])
  expect(converted.output.usage.details.toolUsePromptTokenCount).toBeUndefined()
  expect(converted.issues).toEqual(expect.arrayContaining([expect.objectContaining({ ruleId: 'response-usage-projection', path: '/usage/details/toolUsePromptTokenCount', fidelity: 'lossy_compatible' })]))
  await page.getByRole('button', { name: '添加规则' }).click()
  await page.getByRole('combobox', { name: /^动作/ }).selectOption('core_controls')
  await expect(page.getByText('Apply the selected mapping', { exact: false })).toBeVisible()
  await expect(page.getByLabel('规则 1 目标编码器')).toHaveCount(0)
  const actions = await page.getByRole('combobox', { name: /^动作/ }).locator('option').allTextContents()
  for (const action of ['usage_projection', 'responses_storage', 'responses_context', 'anthropic_usage_envelope']) {
    expect(actions).not.toContain(action)
  }
})

test('real backend context preview preserves null intent, rejects unsupported history and honors disabling', async ({ request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const headers = { Authorization: `Bearer ${process.env.PROTOCOL_E2E_TOKEN}` }
  const sourcePackage = await readActiveProtocolPackage(request, base, headers, 'openai-responses')
  const sourceWire = { model: 'm', input: 'hello', store: false, include: ['reasoning.encrypted_content'] }
  const decode = (fields: object) => decodeProtocolRequest(request, base, headers, sourcePackage, { ...sourceWire, ...fields })
  const list = async () => (await (await request.get(`${base}/api/admin/protocols/conversion-policies`, { headers })).json()).data
  const before = await list()
  const preview = (input: unknown, target = 'google-generate-content', disabled = false) => request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: {
    policy: { schemaVersion: 2, id: 'context-preview', mode: 'strict', rules: disabled ? [{ id: 'reasoning-controls', order: 185, enabled: false, phase: 'request', match: {}, action: 'warn' }] : [] },
    phase: 'request', context: { source: { definitionId: 'openai-responses' }, target: { definitionId: target } },
    input,
  } })
  const nullContext = await decode({ previous_response_id: null })
  expect(nullContext.parameters['wire:openai_responses'].previous_response_id).toBeNull()
  const result = await preview(nullContext)
  expect(result.ok()).toBeTruthy()
  const normalized = (await result.json()).data
  expect(normalized.output.parameters?.['wire:openai_responses']?.previous_response_id).toBeUndefined()
  expect(normalized.output.clientOutput.requireContinuationState).toBe(true)
  expect(normalized.issues).toEqual([expect.objectContaining({ code: 'conversion_normalized', severity: 'info', fidelity: 'preserved', ruleId: 'reasoning-controls', path: '/parameters/wire:openai_responses/previous_response_id' })])
  expect(normalized.persistentWrites).toBe(false)
  for (const [field, values] of Object.entries({ previous_response_id: ['', 42, 'prior-response'], truncation: [42, 'unknown'] })) {
    for (const value of values) {
      const rejected = await request.post(`${base}/api/admin/protocols/preview`, {
        headers: { ...headers, 'Content-Type': 'application/json' },
        data: `{"definition":${sourcePackage},"direction":"decode_request","input":${JSON.stringify({ ...sourceWire, [field]: value })}}`,
      })
      expect(rejected.ok()).toBeTruthy()
      const body = (await rejected.json()).data
      expect(body.output).toBeUndefined()
      expect(body.issues).toEqual(expect.arrayContaining([expect.objectContaining({ severity: 'error', path: `/${field}` })]))
    }
  }
  for (const truncation of ['auto', 'disabled', null]) {
    const input = await decode({ truncation })
    expect(input.parameters.contextTruncation).toBe(truncation)
    const rejected = await preview(input)
    expect(rejected.status()).toBe(400)
    expect((await rejected.json()).error.issues).toEqual(expect.arrayContaining([expect.objectContaining({ path: '/parameters/contextTruncation', fidelity: 'rejected' })]))
  }
  for (const [target, disabled] of [['openai-responses', false], ['google-generate-content', true]] as const) {
    const unchanged = await preview(nullContext, target, disabled)
    expect(unchanged.ok()).toBeTruthy()
    const body = (await unchanged.json()).data
    expect(body.output.parameters['wire:openai_responses'].previous_response_id).toBeNull()
    expect((body.issues ?? []).filter((issue: { ruleId: string }) => issue.ruleId === 'reasoning-controls')).toEqual([])
  }
  expect(await list()).toEqual(before)
})

test('real backend storage and required usage policies expose explicit client projections', async ({ request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const headers = { Authorization: `Bearer ${process.env.PROTOCOL_E2E_TOKEN}` }
  const sourcePackage = await readActiveProtocolPackage(request, base, headers, 'openai-responses')
  const preview = (mode: string, phase: string, input: unknown) => request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: {
    policy: { schemaVersion: 2, id: 'envelope-preview', mode, rules: [] }, phase, input,
    context: phase === 'request' ? { source: { definitionId: 'openai-responses' }, target: { definitionId: 'google-generate-content' } } : { source: { definitionId: 'google-generate-content' }, target: { definitionId: 'anthropic-messages' }, model: 'chosen-model' },
  } })
  for (const store of [undefined, null, true, false]) {
    const requestCore = await decodeProtocolRequest(request, base, headers, sourcePackage, { model: 'm', input: 'hello', store })
    for (const mode of ['compatible', 'strict']) {
      const result = await preview(mode, 'request', requestCore)
      const accepted = mode === 'compatible' || store === false
      expect(result.ok()).toBe(accepted)
      const body = await result.json()
      if (accepted) {
        expect(body.data.output.clientOutput.responsesStorage.effective).toBe(false)
        expect(body.data.output.parameters?.store).toBeUndefined()
        expect(body.data.persistentWrites).toBe(false)
      }
    }
  }
  const invalidStore = await request.post(`${base}/api/admin/protocols/preview`, {
    headers: { ...headers, 'Content-Type': 'application/json' },
    data: `{"definition":${sourcePackage},"direction":"decode_request","input":{"model":"m","input":"hello","store":42}}`,
  })
  expect(invalidStore.ok()).toBeTruthy()
  const invalidPreview = (await invalidStore.json()).data
  expect(invalidPreview.output).toBeUndefined()
  expect(invalidPreview.issues).toEqual(expect.arrayContaining([expect.objectContaining({ severity: 'error' })]))
  const missing = { schemaVersion: 2, source: {}, content: [] }
  const projected = await preview('compatible', 'response', missing)
  expect(projected.ok()).toBeTruthy()
  const body = (await projected.json()).data
  expect(body.output.usage.input).toEqual({ count: 0, origin: 'placeholder' })
  expect(body.output.usage.output).toEqual({ count: 0, origin: 'placeholder' })
  expect(body.issues.some((issue: { ruleId: string }) => issue.ruleId === 'response-anthropic-usage-envelope')).toBeTruthy()
  expect((await preview('strict', 'response', missing)).status()).toBe(400)
})

test('real backend policy draft, read-only preview, verification, activation and stale save', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  test.setTimeout(120000)
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-conversion-e2e'
  const headers = { Authorization: `Bearer ${token}` }
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await expect(page.getByRole('heading', { name: '转换行为', exact: true })).toBeVisible()
  const id = `browser-policy-${Date.now()}`
  await page.getByLabel('策略 ID', { exact: true }).fill(id)
  await page.getByLabel('策略名称', { exact: true }).fill('浏览器验证策略')
  await page.getByRole('button', { name: '添加规则' }).click()
  await page.getByRole('button', { name: '保存策略草稿' }).click()
  await expect(page.getByRole('status')).toContainText('草稿已保存')
  const original = await request.get(`${base}/api/admin/protocols/conversion-policies`, { headers })
  const before = (await original.json()).data.items.find((r: { id: string }) => r.id === id)
  const statsBefore = (await (await request.get(`${base}/api/admin/protocols/continuations`, { headers })).json()).data
  await page.getByRole('button', { name: '预览行为', exact: true }).click()
  await expect(page.getByLabel('行为预览结果')).toContainText('persistentWrites')
  await expect(page.getByLabel('行为预览结果')).toContainText('rule-1')
  const statsAfter = (await (await request.get(`${base}/api/admin/protocols/continuations`, { headers })).json()).data
  expect(statsAfter).toEqual(statsBefore)
  await page.getByRole('button', { name: '验证策略', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('验证完成', { timeout: 60000 })
  await page.getByLabel('来源协议', { exact: true }).selectOption('openai-chat-completions')
  await page.getByLabel('上游协议', { exact: true }).selectOption('google-generate-content')
  await page.getByRole('button', { name: '启用或回滚至此修订' }).click()
  await expect(page.getByRole('status')).toContainText('原子更新')
  const changed = await request.put(`${base}/api/admin/protocols/conversion-policies/${id}/draft`, { headers, data: { policy: { ...before.policy, name: 'New draft' }, expectedHash: before.hash } })
  expect(changed.ok()).toBeTruthy()
  const conflict = await request.put(`${base}/api/admin/protocols/conversion-policies/${id}/draft`, { headers, data: { policy: before.policy, expectedHash: before.hash } })
  expect(conflict.status()).toBe(409)
  expect(errors).toEqual([])
})


test('declared metadata and layout projections use the shared core action catalog', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const headers = { Authorization: `Bearer ${token}` }
  await page.addInitScript(value => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await expect(page.getByRole('heading', { name: '转换行为', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '添加规则', exact: true }).click()
  for (const action of ['core_controls', 'request_projection', 'model_controls', 'stream_disposition']) {
    await page.getByRole('combobox', { name: /^动作/ }).selectOption(action)
  }
  const definition = await readActiveProtocolPackage(request, base, headers, 'openai-chat-completions')
  const input = await decodeProtocolResponse(request, base, headers, definition, {
    id: 'r', object: 'chat.completion', created: 1, model: 'm', system_fingerprint: 'fp_observed',
    choices: [{ index: 0, message: { role: 'assistant', content: 'hello' }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
  })
  const preview = (mode: string) => request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: {
    policy: { schemaVersion: 2, id: 'audit-preview', mode, rules: [] }, phase: 'response', input,
    context: { source: { definitionId: 'openai-chat-completions' }, target: { definitionId: 'anthropic-messages' } },
  } })
  const result = await preview('compatible')
  expect(result.ok()).toBeTruthy()
  const resultData = (await result.json()).data
  expect(resultData.output.metadata ?? []).toEqual([])
  expect(resultData.issues).toEqual(expect.arrayContaining([expect.objectContaining({ ruleId: 'response-metadata', path: '/system_fingerprint', fidelity: 'lossy_compatible' })]))
  expect(resultData.persistentWrites).toBe(false)
  expect((await preview('strict')).status()).toBe(400)
})

test('request hint projection exposes info losses and strict rejection in the shared editor', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const headers = { Authorization: `Bearer ${token}` }
  await page.addInitScript(value => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await page.getByRole('button', { name: '添加规则', exact: true }).click()
  await page.getByRole('combobox', { name: /^动作/ }).selectOption('request_projection')
  await expect(page.getByLabel('规则 1 来源编码器')).toHaveCount(0)
  await expect(page.getByLabel('规则 1 目标编码器')).toHaveCount(0)
  const sourcePackage = await readActiveProtocolPackage(request, base, headers, 'openai-responses')
  const input = await decodeProtocolRequest(request, base, headers, sourcePackage, {
    model: 'm', input: 'hello', store: false, prompt_cache_key: 'anonymous-session', metadata: { trace: 'opaque' }, user: 'anonymous',
  })
  const preview = (mode: string) => request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: {
    policy: { schemaVersion: 2, id: 'request-hints', mode, rules: [] }, phase: 'request', input,
    context: { source: { definitionId: 'openai-responses' }, target: { definitionId: 'google-generate-content' } },
  } })
  const compatible = await preview('compatible')
  expect(compatible.ok()).toBeTruthy()
  const result = (await compatible.json()).data
  expect(result.persistentWrites).toBe(false)
  expect(result.output.cache).toBeUndefined()
  expect(result.output.parameters?.metadata).toBeUndefined()
  expect(result.output.parameters?.user).toBeUndefined()
  const omissions = result.issues.filter((issue: { ruleId: string }) => issue.ruleId === 'request-projection')
  expect(omissions).toHaveLength(3)
  for (const issue of omissions) expect(issue).toMatchObject({ code: 'conversion_degraded', severity: 'info', fidelity: 'lossy_compatible' })
  const strict = await preview('strict')
  expect(strict.status()).toBe(400)
  expect((await strict.json()).error.issues).toEqual(expect.arrayContaining([expect.objectContaining({ ruleId: 'request-projection', fidelity: 'rejected' })]))
})
