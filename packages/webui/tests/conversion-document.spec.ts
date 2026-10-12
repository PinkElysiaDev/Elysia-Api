import { expect, test } from '@playwright/test'
import { appendConversionRule, moveConversionRule, parseConversionDocument, patchConversionFields } from '../src/lib/conversion-document'
import { ProtocolDocument } from '../src/lib/protocol-document'

const largeInteger = '9007199254740993'
const policyJSON = `{"schemaVersion":1,"id":"precision-policy","name":"Precision","rules":[{"id":"exact","order":1000,"enabled":true,"phase":"request","match":{},"action":"set","path":"/parameters/audit_integer","value":${largeInteger},"expression":{"op":"literal","value":${largeInteger}}},{"id":"other","order":2000,"enabled":true,"phase":"request","match":{},"action":"warn","reason":"audit"}]}`

test('field editing, rule ordering and deletion preserve exact arbitrary JSON', () => {
  expect(parseConversionDocument(policyJSON).ok).toBe(true)
  let source = patchConversionFields(policyJSON, '', { name: 'Changed' })
  source = patchConversionFields(source, '/rules/0', { enabled: false })
  source = patchConversionFields(source, '/rules/0/match', { model: 'example' })
  source = moveConversionRule(source, 0, 1)
  source = appendConversionRule(source, { id: 'new', order: 3000, enabled: true, phase: 'request', match: {}, action: 'warn' })
  source = new ProtocolDocument(source).remove('/rules/0')
  const document = new ProtocolDocument(source)
  expect(document.read('/rules/0/value')).toBe(largeInteger)
  expect(document.read('/rules/0/expression/value')).toBe(largeInteger)
  expect(document.read('/rules/0/order')).toBe('2000')
  expect(document.read('/rules/0/enabled')).toBe('false')
  expect(document.read('/rules/0/match/model')).toBe('"example"')
})

test('invalid structures produce a parse failure with no renderable policy', () => {
  for (const rules of ['null', '{}', '[null]', '[42]', '[{"match":42}]', '[{"id":{}}]']) {
    const parsed = parseConversionDocument(`{"id":"invalid","rules":${rules}}`)
    expect(parsed.ok).toBe(false)
    expect('policy' in parsed).toBe(false)
    if (!parsed.ok) expect(parsed.error).toContain('/rules')
  }
  for (const raw of ['null', '[]', '{', '{"id":"x","id":"y","rules":[]}']) {
    expect(parseConversionDocument(raw).ok).toBe(false)
  }
})

test('real backend policy editing and preview preserve large integers and recover from invalid JSON', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires isolated real backend')
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`${process.env.PROTOCOL_UI_PATH ?? '/'}#/protocols/conversions`)
  await expect(page.getByRole('button', { name: '新建策略' })).toBeEnabled()
  await page.getByText('完整策略 JSON', { exact: true }).click()
  const editor = page.getByLabel('完整策略 JSON', { exact: true })
  for (const rules of ['null', '{}', '[null]']) {
    const invalid = `{"id":"invalid","rules":${rules}}`
    await editor.fill(invalid)
    await expect(page.getByRole('alert')).toContainText('/rules')
    await expect(page.getByRole('button', { name: '保存策略草稿' })).toBeDisabled()
    await expect(editor).toHaveValue(invalid)
  }
  const policyId = `precision-${Date.now()}`
  await editor.fill(policyJSON.replace('precision-policy', policyId))
  await page.getByLabel('策略名称', { exact: true }).fill('Renamed without rounding')
  await page.getByRole('button', { name: '下移', exact: true }).first().click()
  await page.getByRole('button', { name: '保存策略草稿' }).click()
  await expect(page.getByRole('status')).toContainText('草稿已保存')
  const stored = await request.get(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/conversion-policies`, { headers: { Authorization: `Bearer ${token}` } })
  expect(await stored.text()).toContain(`"value":${largeInteger}`)
  await page.getByRole('button', { name: '新建策略' }).click()
  await page.getByLabel('转换策略', { exact: true }).selectOption(policyId)
  await expect(editor).toHaveValue(new RegExp(largeInteger))
  await page.getByRole('button', { name: '预览行为', exact: true }).click()
  await expect(page.getByLabel('行为预览结果')).toContainText(`"audit_integer":${largeInteger}`)
  expect(errors).toEqual([])
})
