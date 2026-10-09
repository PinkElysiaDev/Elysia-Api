import { expect, test } from '@playwright/test'

test('real backend include and usage projections share preview and strict boundaries', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-conversion-e2e'
  const headers = { Authorization: `Bearer ${token}` }
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await expect(page.getByRole('heading', { name: '转换行为', exact: true })).toBeVisible()
  await page.getByLabel('行为预览上下文').fill(JSON.stringify({ source: { definitionId: 'openai-responses' }, target: { definitionId: 'google-generate-content' } }))
  await page.getByLabel('行为预览输入').fill(JSON.stringify({ schemaVersion: 1, source: {}, content: [], parameters: { responses_include: ['reasoning.encrypted_content'] } }))
  await page.getByRole('button', { name: '预览行为', exact: true }).click()
  const preview = page.getByLabel('行为预览结果')
  await expect(preview).toContainText('rawResponsesInclude')
  const result = JSON.parse((await preview.textContent())!)
  expect(result.output.parameters?.responses_include).toBeUndefined()
  expect(result.effective.origins['responses-include']).toBe('engine-default')
  expect(result.persistentWrites).toBe(false)
  const policy = { schemaVersion: 1, id: 'strict-preview', mode: 'strict', rules: [] }
  const context = { source: { definitionId: 'google-generate-content' }, target: { definitionId: 'anthropic-messages' } }
  const input = { schemaVersion: 1, source: {}, content: [], usage: { output: { count: 5, origin: 'observed' }, details: { 'output.reasoning_tokens': { count: 3, origin: 'observed' } } } }
  const strict = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: { policy, context, input, phase: 'response' } })
  expect(strict.status()).toBe(400)
  expect(await strict.text()).toContain('output.reasoning_tokens')
  const compatible = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: { policy: { ...policy, mode: 'compatible' }, context, input, phase: 'response' } })
  expect(compatible.ok()).toBeTruthy()
  const converted = (await compatible.json()).data
  expect(converted.output.usage.output.count).toBe(5)
  expect(converted.output.usage.details?.['output.reasoning_tokens']).toBeUndefined()
  expect(converted.issues[0].ruleId).toBe('response-usage-projection')
  await page.getByRole('button', { name: '添加规则' }).click()
  await page.getByRole('combobox', { name: /^动作/ }).selectOption('usage_projection')
  await expect(page.getByLabel('规则 1 目标编码器')).toBeVisible()
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
