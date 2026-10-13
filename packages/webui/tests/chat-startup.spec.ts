import { expect, test } from '@playwright/test'

test('source frame preview uses startup policy and exposes strict rejection', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const headers = { Authorization: `Bearer ${token}` }
  const first = { id: 'a', model: 'm', created: 1, object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: '' } }] }
  const second = { ...first, id: 'b', created: 81, choices: [{ index: 0, delta: { role: 'assistant' } }] }
  const context = { source: { definitionId: 'openai-chat-completions' }, target: { definitionId: 'openai-chat-completions' }, transport: 'sse' }
  await page.addInitScript(value => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await page.getByLabel('预览阶段').selectOption('source_event')
  await page.getByLabel('行为预览上下文').fill(JSON.stringify(context))
  await page.getByLabel('行为预览输入').fill(JSON.stringify([first, second]))
  await page.getByRole('button', { name: '预览行为', exact: true }).click()
  const preview = page.getByLabel('行为预览结果')
  await expect(preview).toContainText('chat-stream-identity')
  const result = JSON.parse((await preview.textContent())!)
  expect(result.output.map((f: { id: string; created: number }) => [f.id, f.created])).toEqual([['b', 81], ['b', 81]])
  expect(result.issues).toHaveLength(2)
  expect(result.effective.origins['chat-stream-identity']).toBe('engine-default')
  const strict = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: { phase: 'source_event', sequence: true, context, input: [first, second], policy: { schemaVersion: 1, id: 'strict-startup', mode: 'strict', rules: [] } } })
  expect(strict.status()).toBe(400)
  expect((await strict.json()).error.issues).toEqual(expect.arrayContaining([expect.objectContaining({ path: '/id', ruleId: 'chat-stream-identity', fidelity: 'rejected', direction: 'decode_event' })]))
})
