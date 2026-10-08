import { expect, test } from '@playwright/test'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'

test('upgraded backend exposes recovery progress and discovers models with all four presets', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires a real upgraded backend')
  test.setTimeout(90_000)
  const base = process.env.PROTOCOL_E2E_URL!
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-protocol-e2e'
  const headers = { Authorization: `Bearer ${token}` }
  const ready = await request.get(`${base}/ready`)
  expect(ready.status()).toBe(200)
  expect((await ready.json()).ready).toBe(true)
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto('/#/protocols')
  await expect(page.getByRole('heading', { name: '预置协议（只读）' })).toBeVisible()
  const paths: string[] = []
  const upstream = createServer((incoming, response) => {
    paths.push(new URL(incoming.url!, 'http://localhost').pathname)
    response.writeHead(200, { 'Content-Type': 'application/json' })
    response.end(JSON.stringify({ has_more: false, data: [{ id: 'browser-model', display_name: 'browser-model', type: 'model' }], models: [{ name: 'models/browser-model', displayName: 'browser-model', supportedGenerationMethods: ['generateContent'] }] }))
  })
  await new Promise<void>((resolve) => upstream.listen(0, '127.0.0.1', resolve))
  try {
    const origin = `http://127.0.0.1:${(upstream.address() as AddressInfo).port}`
    for (const protocol of ['openai-chat-completions', 'openai-responses', 'anthropic-messages', 'google-generate-content']) {
      const id = `upgrade-browser-${protocol}-${Date.now()}`
      const source = { id, name: id, platform: `custom:${protocol}`, baseUrl: origin + (protocol.startsWith('openai') ? '/v1' : ''), apiKey: 'mock-only', enabled: true, autoFetchModels: true }
      const saved = await request.put(`${base}/api/admin/model-sources/${id}`, { headers, data: source })
      expect(saved.status(), await saved.text()).toBe(200)
      await expect.poll(async () => {
        const listed = await request.get(`${base}/api/admin/model-sources`, { headers })
        const item = (await listed.json()).data.items.find((entry: { id: string }) => entry.id === id)
        if (item.refreshState.lastError) throw new Error(item.refreshState.lastError)
        return item.refreshState.lastCount
      }).toBe(1)
    }
    expect(paths.filter((path) => path === '/v1/models')).toHaveLength(3)
    expect(paths.filter((path) => path === '/v1beta/models')).toHaveLength(1)
    await page.goto('/#/sources')
    await expect(page.getByText(/upgrade-browser-openai-chat-completions-/).first()).toBeVisible()
  } finally {
    await new Promise<void>((resolve, reject) => upstream.close((error) => error ? reject(error) : resolve()))
  }
})
