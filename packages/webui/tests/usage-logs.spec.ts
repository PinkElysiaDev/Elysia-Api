import { expect, test } from '@playwright/test'
import type { UsageLogDetail } from '../src/lib/types'

const emptyBody = { content: '', truncated: false }
const details: UsageLogDetail[] = [
  {
    requestId: 'missing', startedAt: '2026-09-28T12:10:00Z', endedAt: '2026-09-28T12:10:01Z',
    keyName: 'test-key', keyHash: '', requestedModelGroup: 'gpt-6-luna', groupName: '', modelName: '',
    platform: '', inputFormat: 'openai_responses', stream: true, statusCode: 404,
    error: 'Model not found', errorKind: 'model_not_found', firstByteMs: 0, durationMs: 5, usage: {}, retryCount: 0,
    incomingBody: emptyBody, outgoingBody: emptyBody, providerResponse: emptyBody, downstreamResponse: emptyBody,
  },
]
const base = { ...details[0], sourceId: 'deepseek', statusCode: 200, error: '', errorKind: '', firstByteMs: 250, durationMs: 1600 }
details.push(
  { ...base, requestId: 'same', requestedModelGroup: 'deepseek-flash', modelName: 'deepseek-flash', groupName: 'deepseek-flash', platform: 'deepseek', sourceFormat: 'openai', targetFormat: 'chat_completions' },
  { ...base, requestId: 'mapped', requestedModelGroup: 'long-routing-group-name-that-needs-truncation-'.repeat(3), modelName: 'actual-model', platform: 'openai', sourceFormat: 'openai_responses', targetFormat: 'openai' },
  { ...base, requestId: 'agent', keyName: 'AI 助手', requestedModelGroup: 'deepseek-flash', modelName: 'deepseek-flash', inputFormat: '', platform: 'deepseek', targetFormat: 'chat_completions', relayMode: 'agent-assist',
    incomingBody: { ...emptyBody, content: '{"Messages":[{"role":"user","content":"hello"}]}' },
    outgoingBody: { ...emptyBody, content: '{"model":"deepseek-flash","stream":true}' },
    providerResponse: { ...emptyBody, content: '[{"choices":[{"delta":{"content":"hello"}}]}]' },
    downstreamResponse: { ...emptyBody, content: '{"result":{"Text":"hello"}}' },
  },
  { ...base, requestId: 'cancelled', requestedModelGroup: 'chat', modelName: 'deepseek-flash', statusCode: 499, errorKind: 'client_canceled', error: 'context canceled', sourceFormat: 'openai', targetFormat: 'openai' },
  { ...base, requestId: 'gemini', requestedModelGroup: 'chat', modelName: 'gemini-flash', platform: 'gemini', sourceFormat: 'openai', relayMode: 'transform' },
)

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'test-only')
    localStorage.setItem('elysia-webui.theme', 'light')
  })
  await page.route('**/api/admin/**', async (route) => {
    const url = new URL(route.request().url())
    let data: unknown = { items: [], total: 0 }
    if (url.pathname.endsWith('/seq')) data = { seq: 1 }
    else if (url.pathname.endsWith('/model-sources')) data = { items: [{ id: 'deepseek', name: 'DeepSeek', enabled: true }] }
    else if (url.pathname.endsWith('/usage/logs')) {
      const keys = url.searchParams.getAll('keyName')
      const items = details.filter((d) => !keys.length || keys.includes(d.keyName)).map((d) => ({ ...d, inputTokens: 100, outputTokens: 20 }))
      data = { total: items.length, items }
    } else if (url.pathname.includes('/usage/logs/')) data = details.find((d) => url.pathname.endsWith(`/${d.requestId}`))
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/usage-logs')
})

test('request model and route stay compact and missing models remain visible', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expect(page.getByRole('columnheader', { name: '请求模型 / 路由' })).toBeVisible()
  const missing = page.getByRole('button', { name: '查看请求 missing 详情', exact: true })
  await expect(missing).toContainText('gpt-6-luna')
  await expect(missing).toContainText('未路由')
  const same = page.getByRole('button', { name: '查看请求 same 详情', exact: true })
  await expect(same.getByText('deepseek-flash', { exact: true })).toHaveCount(1)
  await expect(same).toContainText('DeepSeek')
  const mapped = page.getByRole('button', { name: '查看请求 mapped 详情', exact: true })
  await expect(mapped).toContainText('→ actual-model · DeepSeek')
  const model = mapped.getByTitle(details[2].requestedModelGroup, { exact: true })
  await expect(model).toBeVisible()
  expect(await model.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('usage-logs.png'), fullPage: true })
  await missing.click()
  const sheet = page.getByRole('dialog', { name: '调用详情' })
  await expect(sheet.getByText('调用失败（404）', { exact: false })).toBeVisible()
  await expect(sheet.getByText('gpt-6-luna', { exact: true })).toBeVisible()
  await expect(sheet.getByText('Responses API', { exact: true })).toHaveCount(1)
  await expect(sheet.getByText('未转发', { exact: true })).toBeVisible()
  await expect(sheet.getByText('未记录正文', { exact: true })).toHaveCount(4)
  await page.setViewportSize({ width: 375, height: 812 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('protocol routes stay consistent and status codes stay compact', async ({ page }) => {
  for (const id of ['same', 'mapped', 'cancelled', 'gemini']) {
    await page.getByRole('button', { name: `查看请求 ${id} 详情`, exact: true }).click()
    const sheet = page.getByRole('dialog', { name: '调用详情' })
    await expect(sheet.getByText('Chat Completions API', { exact: true })).toHaveCount(1)
    await expect(sheet.getByText('Responses API', { exact: true })).toHaveCount(id === 'mapped' ? 1 : 0)
    await expect(sheet.getByText('Gemini API', { exact: true })).toHaveCount(id === 'gemini' ? 1 : 0)
    if (id === 'gemini') {
      const colors = await sheet.locator('section').filter({ hasText: '协议链路' }).locator('span.rounded-full').evaluateAll((pills) => pills.map((pill) => getComputedStyle(pill).borderColor))
      expect(new Set(colors).size).toBe(1)
    }
    await expect(sheet.getByText('未转发', { exact: true })).toHaveCount(0)
    await expect(sheet.locator('section').filter({ hasText: '协议链路' }).getByRole('button')).toHaveCount(0)
    if (id === 'cancelled') await expect(sheet.getByText('客户端取消（499）', { exact: false })).toBeVisible()
    await sheet.getByRole('button', { name: '关闭', exact: true }).last().click()
  }
  await expect(page.getByText('499', { exact: true })).toBeVisible()
})

test('assistant has four labelled bodies, exports metadata, and uses the new filter name', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.getByRole('button', { name: '查看请求 agent 详情', exact: true }).click()
  const sheet = page.getByRole('dialog', { name: '调用详情' })
  await expect(sheet.getByText('内部调用', { exact: true })).toBeVisible()
  await expect(sheet.getByText('Chat Completions API', { exact: true })).toHaveCount(1)
  for (const title of ['① 助手内部请求', '② 后端转发', '③ 上游回传', '④ 返回助手引擎']) {
    await expect(sheet.getByRole('button', { name: new RegExp(title) })).toBeEnabled()
    await expect(sheet.getByRole('button', { name: new RegExp(title) })).toHaveAttribute('aria-expanded', 'false')
  }
  await sheet.getByRole('button', { name: /④ 返回助手引擎/ }).click()
  await expect(sheet.locator('#chain-body-downstream')).toHaveAttribute('aria-hidden', 'false')
  await sheet.getByText('内部调用', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('assistant-detail.png'), fullPage: true })
  const downloaded = page.waitForEvent('download')
  await sheet.getByRole('button', { name: '导出完整日志' }).click()
  const stream = await (await downloaded).createReadStream()
  let body = ''
  for await (const chunk of stream!) body += chunk.toString()
  expect(JSON.parse(body).overview).toMatchObject({ requestedModelGroup: 'deepseek-flash', sourceId: 'deepseek' })
  await sheet.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.getByRole('button', { name: '调用方筛选', exact: true }).click()
  await expect(page.getByRole('option', { name: 'AI 协议助手', exact: true })).toHaveCount(0)
  await page.getByRole('option', { name: 'AI 助手', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: /^查看请求 .* 详情$/ })).toHaveCount(1)
})

for (const viewport of [{ width: 1440, height: 800 }, { width: 1440, height: 500 }, { width: 375, height: 812 }]) {
  test(`opening log details preserves page and sidebar position at ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    const rows = Array.from({ length: 20 }, (_, index) => ({ ...details[3], requestId: `row-${index}`, keyName: 'test-key', relayMode: 'transform', sourceFormat: 'openai', targetFormat: 'gemini', platform: 'gemini' }))
    await page.route('**/api/admin/usage/logs?**', (route) => route.fulfill({ json: { ok: true, data: { items: rows, total: rows.length } } }))
    await page.route('**/api/admin/usage/logs/row-*', (route) => route.fulfill({ json: { ok: true, data: rows.find((row) => route.request().url().endsWith(`/${row.requestId}`)) } }))
    await page.reload()
    const row = page.getByRole('button', { name: '查看请求 row-18 详情', exact: true })
    await row.scrollIntoViewIfNeeded()
    const scrollY = await page.evaluate(() => window.scrollY)
    expect(scrollY).toBeGreaterThan(500)
    const sidebar = page.locator('aside').first()
    const sidebarBox = await sidebar.boundingBox()
    const nav = sidebar.locator('nav')
    const navScroll = await nav.evaluate((el) => { el.scrollTop = el.scrollHeight; return el.scrollTop })
    await row.click()
    const sheet = page.getByRole('dialog', { name: '调用详情' })
    await expect(sheet.getByText('Gemini API', { exact: true })).toBeVisible()
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(scrollY)
    await expect.poll(() => sidebar.boundingBox()).toEqual(sidebarBox)
    await expect.poll(() => nav.evaluate((el) => el.scrollTop)).toBe(navScroll)
    await page.mouse.move(10, viewport.height / 2)
    await page.mouse.wheel(0, -400)
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    expect(await page.evaluate(() => window.scrollY)).toBe(scrollY)
    expect(await sidebar.boundingBox()).toEqual(sidebarBox)
    for (const segment of ['incoming', 'outgoing', 'provider', 'downstream']) {
      await expect(sheet.locator(`#chain-trigger-${segment}`)).toHaveAttribute('aria-expanded', 'false')
    }
    await sheet.locator('#chain-trigger-incoming').click()
    await expect(sheet.locator('#chain-trigger-incoming')).toHaveAttribute('aria-expanded', 'true')
    await page.keyboard.press('Escape')
    await expect(sheet).not.toBeVisible()
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(scrollY)
    await expect.poll(() => sidebar.boundingBox()).toEqual(sidebarBox)
    await expect.poll(() => nav.evaluate((el) => el.scrollTop)).toBe(navScroll)
    await row.press('Enter')
    await expect(sheet.locator('#chain-trigger-incoming')).toHaveAttribute('aria-expanded', 'false')
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(scrollY)
    await expect.poll(() => sidebar.boundingBox()).toEqual(sidebarBox)
    await expect.poll(() => nav.evaluate((el) => el.scrollTop)).toBe(navScroll)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test('large bodies render only when expanded and preserve JSON and SSE contents', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const messages = Array.from({ length: 6000 }, (_, index) => ({ role: 'user', content: `message ${index} <script>sample</script>` }))
  const request = { model: 'test-model', messages }
  const jsonBody = { ...emptyBody, content: JSON.stringify(request) }
  const sseBody = { ...emptyBody, content: messages.map((message) => `data: ${JSON.stringify({ choices: [{ delta: message }] })}\n\n`).join('') + 'data: [DONE]\n\n' }
  await page.route('**/api/admin/usage/logs/agent', (route) => route.fulfill({ json: { ok: true, data: {
    ...details[3], incomingBody: jsonBody, outgoingBody: jsonBody, providerResponse: sseBody, downstreamResponse: sseBody,
  } } }))
  await page.getByRole('button', { name: '查看请求 agent 详情', exact: true }).click()
  const sheet = page.getByRole('dialog', { name: '调用详情' })
  await expect(sheet.locator('#chain-trigger-downstream')).toBeVisible()
  await expect(sheet.locator('pre')).toHaveCount(0)
  await sheet.locator('#chain-trigger-incoming').click()
  await expect(sheet.locator('pre')).toHaveCount(1)
  expect(JSON.parse((await sheet.locator('#chain-body-incoming pre').textContent())!)).toEqual(request)
  await expect(sheet.locator('#chain-body-incoming script')).toHaveCount(0)
  await sheet.locator('#chain-trigger-provider').click()
  await expect(sheet.locator('pre')).toHaveCount(2)
  await expect(sheet.locator('#chain-body-provider pre')).toContainText('message 5999 <script>sample</script>')
  await expect(sheet.locator('#chain-body-provider pre')).toContainText('[DONE]')
  await sheet.locator('#chain-trigger-incoming').click()
  await expect(sheet.locator('#chain-body-incoming pre')).toHaveCount(0)
  await expect(sheet.locator('#chain-body-provider pre')).toBeVisible()
})
