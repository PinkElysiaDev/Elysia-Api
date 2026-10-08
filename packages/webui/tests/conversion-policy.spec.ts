import { expect, test } from '@playwright/test'

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
