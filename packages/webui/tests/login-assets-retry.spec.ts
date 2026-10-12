import { expect, test } from '@playwright/test'

test('a failed login trace chunk can be loaded on the next demand', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-only'))
  await page.route('**/api/admin/**', (route) => route.fulfill({ json: { ok: true, data: { items: [], total: 0 } } }))
  let attempts = 0
  await page.route('**/assets/login-trace/001.json?*', async (route) => {
    attempts++
    if (attempts === 1) await route.fulfill({ status: 503, body: 'temporary failure' })
    else await route.continue()
  })
  await page.goto('/#/usage-logs')
  const load = () => page.evaluate(async () => {
    const modulePath = '/src/lib/login-assets.ts'
    const { loadCharacterTrace } = await import(modulePath)
    const controller = new AbortController()
    await loadCharacterTrace(controller.signal)
    controller.abort()
  })
  const failed = page.waitForResponse((response) => response.url().includes('/001.json') && response.status() === 503)
  await load()
  await failed
  const recovered = page.waitForResponse((response) => response.url().includes('/001.json') && response.ok())
  await load()
  await recovered
  expect(attempts).toBe(2)
})
