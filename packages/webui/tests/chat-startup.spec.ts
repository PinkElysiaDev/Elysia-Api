import { expect, test } from '@playwright/test'
import { readActiveProtocolPackage } from './protocol-fixtures'

test('source frames use the active Spec lifecycle without a legacy startup rewrite', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const headers = { Authorization: `Bearer ${token}` }
  const definition = await readActiveProtocolPackage(request, base, headers, 'openai-chat-completions')
  const first = { id: 'a', model: 'm', created: 1, object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: '' } }] }
  const second = { ...first, choices: [{ index: 0, delta: { content: 'hello' } }] }
  const finished = { ...first, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] }
  const preview = (frames: unknown[]) => request.post(`${base}/api/admin/protocols/preview`, {
    headers: { ...headers, 'Content-Type': 'application/json' },
    data: `{"definition":${definition},"direction":"decode_event","sequence":true,"input":${JSON.stringify(frames)}}`,
  })
  const valid = await preview([first, second, finished])
  expect(valid.ok(), await valid.text()).toBeTruthy()
  const events = (await valid.json()).data.output as { type: string; responseId: string; delta?: string }[]
  expect(events.every((event) => event.responseId === 'a')).toBeTruthy()
  expect(events.filter((event) => event.type === 'item.delta').map((event) => event.delta).join('')).toBe('hello')
  expect(events.some((event) => event.type === 'response.finished')).toBeTruthy()
  for (const frames of [[first, { ...second, id: 'b', created: 81 }, finished], [first, second]]) {
    const rejected = await preview(frames)
    expect(rejected.ok()).toBeTruthy()
    const result = (await rejected.json()).data
    expect(result.output).toBeUndefined()
    expect(result.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ severity: 'error', direction: 'decode_event' }),
    ]))
  }
  await page.addInitScript(value => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await expect(page.getByLabel('预览阶段')).not.toContainText('上游流帧（解码前）')
  const retired = await request.post(`${base}/api/admin/protocols/conversion-policies/preview`, { headers, data: {
    phase: 'source_event', input: [first, second],
    context: { source: { definitionId: 'openai-chat-completions' }, target: { definitionId: 'openai-chat-completions' } },
    policy: { schemaVersion: 2, id: 'retired-startup', mode: 'compatible', rules: [] },
  } })
  expect(retired.status()).toBe(400)
})
