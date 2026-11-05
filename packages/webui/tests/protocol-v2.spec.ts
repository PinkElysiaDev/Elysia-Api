import { expect, test } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { ProtocolDocument } from '../src/lib/protocol-document'
import { protocolIdentityPrefix } from '../src/lib/protocol-v2'

function readProtocolPackage(directory: string) {
  const readPart = (part: string) => JSON.parse(readFileSync(resolve(`../../backend/protocol/${directory}/${part}.json`), 'utf8'))
  return { schemaVersion: 4, spec: readPart('spec'), mapping: readPart('mapping') }
}

function readTextPackage(id: string) {
  const fixture = readProtocolPackage('testdata/packages/text-alpha')
  fixture.spec.id = id
  fixture.spec.family = id
  return fixture
}

test('designer renders the published listing contract without migration metadata', async ({ page }) => {
  const errors: string[] = []
  const activeHash = 'a'.repeat(64)
  page.on('pageerror', (error) => errors.push(error.message))
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-only'))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], seq: 1 }
    if (path.endsWith('/schema')) data = {}
    if (path.endsWith('/protocols')) data = {
      drafts: [{ protocolId: 'my-protocol', hash: 'd'.repeat(64), definition: { id: 'my-protocol' }, updatedAt: '2026-10-08T00:00:00Z' }],
      active: [
        { protocolId: 'openai-chat-completions', revisionHash: activeHash },
        { protocolId: 'google-generate-content', revisionHash: 'pending' },
        { protocolId: 'my-protocol', revisionHash: 'pending' },
      ],
      loaded: { 'openai-chat-completions': activeHash },
      presets: ['openai-chat-completions', 'google-generate-content'],
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols')
  await expect(page.getByRole('row').filter({ hasText: 'openai-chat-completions' })).toContainText('已加载')
  await expect(page.getByRole('row').filter({ hasText: 'google-generate-content' })).toContainText('已创建，等待验证或加载')
  await expect(page.getByRole('row').filter({ hasText: 'my-protocol' })).toContainText('需重新验证或修复')
  for (const width of [375, 820, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.evaluate(() => document.fonts.ready)
    const overlapping = await page.locator('tbody td').evaluateAll((cells) => cells.filter((cell) => {
      const bounds = cell.getBoundingClientRect()
      const range = document.createRange()
      range.selectNodeContents(cell)
      const content = range.getBoundingClientRect()
      return content.left < bounds.left - 1 || content.right > bounds.right + 1
    }).map((cell) => cell.textContent))
    expect(overlapping, `table content must stay within its column at ${width}px`).toEqual([])
    await page.getByRole('button', { name: '复制为新协议' }).first().click({ trial: true })
    await page.getByRole('button', { name: '编辑 my-protocol', exact: true }).click({ trial: true })
    await page.getByRole('button', { name: '删除 my-protocol', exact: true }).click({ trial: true })
  }
  expect(errors).toEqual([])
})

test('Agent edit session opens exact package draft in the editor', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'requires an isolated real backend')
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-protocol-e2e'
  const fixture = readTextPackage(`agent-browser-${Date.now()}`)
  const source = new ProtocolDocument(JSON.stringify(fixture)).set('/mapping/samples/0/context', '{"long":900719925474099312345}')
  const saved = await request.put(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/${fixture.spec.id}/draft`, { headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, data: source })
  expect(saved.ok()).toBeTruthy()
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto(`/#/agent?mode=edit&protocol=${fixture.spec.id}`)
  await page.getByRole('button', { name: '协议草稿', exact: true }).click()
  await page.getByRole('button', { name: '在协议设计器中打开' }).click()
  await page.getByRole('tab', { name: '完整 JSON', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '完整协议 JSON', exact: true })).toHaveValue(source)
})

test('raw protocol edits preserve presence, numeric spelling and ordered extensions', () => {
  const source = '{"name":"first","long":900719925474099312345,"negative":-0,"exp":1e99,"array":[null,false,0,{},[]],"nested":{"a/b":{"~x":null}}}'
  const document = new ProtocolDocument(source)
  const updated = document.set('/name', '"second"')
  expect(updated).toBe(source.replace('first', 'second'))
  expect(document.read('/nested/a~1b/~0x')).toBe('null')
  expect(new ProtocolDocument(document.set('/nested/new', 'false')).read('/nested/new')).toBe('false')
  for (const invalid of ['{"x":1,"x":2}', '{"x":}', '[1,]', '{"x":01}', '{}{}', '['.repeat(66) + ']'.repeat(66)]) expect(() => new ProtocolDocument(invalid)).toThrow()
  for (const version of [3, 4]) {
    const packageSource = `{"schemaVersion":${version},"spec":{"id":"historical"},"mapping":{}}`
    expect(protocolIdentityPrefix(new ProtocolDocument(packageSource))).toBe('/spec')
  }
})

test('package sources share one save and evidence, preserve exact JSON, and locate diagnostics', async ({ page }) => {
  const id = 'package-browser'
  const draftHash = 'd'.repeat(64)
  const revisionHash = 'a'.repeat(64)
  const compilerVersion = 'package-browser-compiler'
  const spec = '{"id":"package-browser","name":"Package browser","version":"1","family":"synthetic","wireVersion":"1","documents":{},"operations":{}}'
  const mapping = '{"coreVersion":2,"dslVersion":2,"native":{"preserve":true},"capabilities":{},"directions":{},"operations":{},"samples":[],"expressions":{"exact":{"op":"literal","value":900719925474099312345}}}'
  let saved = ''
  let saves = 0
  let activeHash = ''
  const draftJSON = () => `{"protocolId":"${id}","hash":"${draftHash}","definition":${saved},"updatedAt":"2026-10-31T00:00:00Z"}`
  await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-only'))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = { items: [], seq: 1 }
    if (path.endsWith('/protocols/schema')) data = {
      compilerVersion, packageVersion: 4, directions: [], capabilities: [], mappingOperations: [],
      modules: [], events: [], transports: [], engineFeatures: [],
    }
    if (path.endsWith('/protocols')) {
      await route.fulfill({ contentType: 'application/json', body: `{"ok":true,"data":{"drafts":[${saved ? draftJSON() : ''}],"active":${JSON.stringify(activeHash ? [{ protocolId: id, revisionHash: activeHash }] : [])},"loaded":{},"presets":[]}}` })
      return
    }
    if (path.endsWith(`/${id}/draft`) && route.request().method() === 'PUT') {
      saves++
      saved = route.request().postData()!
      await route.fulfill({ contentType: 'application/json', body: `{"ok":true,"data":{"draft":${draftJSON()},"issues":[]}}` })
      return
    }
    if (path.endsWith(`/${id}/verify`)) data = { report: { passed: true, definitionHash: revisionHash, compilerVersion, issues: [], checks: [], covered: [] } }
    if (path.endsWith(`/${id}/activate`)) {
      expect(route.request().postDataJSON()).toEqual({ revisionHash, expectedActive: '' })
      activeHash = revisionHash
      data = { protocolId: id, revisionHash }
    }
    if (path.endsWith('/protocols/validate')) data = {
      valid: false, issues: [{ code: 'invalid_definition', severity: 'error', path: '/mapping/expressions/exact/value', reason: 'Synthetic editor diagnostic' }],
    }
    if (path.endsWith('/protocols/preview')) {
      if (route.request().postDataJSON().mode === 'count_tokens') {
        expect(route.request().postDataJSON()).toMatchObject({ mode: 'count_tokens', operation: 'count', conversionContext: { model: 'count-model' } })
        expect(route.request().postData()).toContain('9007199254740993')
        await route.fulfill({ contentType: 'application/json', body: '{"ok":true,"data":{"output":{"inputTokens":9007199254740993},"issues":[]}}' })
        return
      }
      expect(route.request().postDataJSON()).toMatchObject({ mode: 'models', direction: 'encode_response', operation: 'models', input: { models: [{ id: 'local' }] } })
      data = { output: { available: ['local'] }, issues: [] }
    }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/protocols')
  await page.locator('section').getByRole('button', { name: '新建协议', exact: true }).click()
  await page.getByRole('tab', { name: '外部协议 spec.json', exact: true }).click()
  await page.getByLabel('导入 spec.json', { exact: true }).setInputFiles({ name: 'spec.json', mimeType: 'application/json', buffer: Buffer.from(spec) })
  await expect(page.getByRole('textbox', { name: '外部协议 spec.json JSON', exact: true })).toHaveValue(spec)
  await page.getByRole('tab', { name: '转换说明 mapping.json', exact: true }).click()
  await page.getByLabel('导入 mapping.json', { exact: true }).setInputFiles({ name: 'mapping.json', mimeType: 'application/json', buffer: Buffer.from(mapping) })
  const mappingEditor = page.getByRole('textbox', { name: '转换说明 mapping.json JSON', exact: true })
  await expect(mappingEditor).toHaveValue(mapping)
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 mapping.json', exact: true }).click()
  const download = await downloadPromise
  expect(readFileSync((await download.path())!, 'utf8')).toBe(mapping)
  await mappingEditor.fill('{"unfinished":')
  await expect(page.getByRole('button', { name: '保存草稿', exact: true })).toBeDisabled()
  await page.getByRole('tab', { name: '外部协议 spec.json', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '外部协议 spec.json JSON', exact: true })).toHaveValue(spec)
  await page.getByRole('tab', { name: '转换说明 mapping.json', exact: true }).click()
  await expect(mappingEditor).toHaveValue('{"unfinished":')
  await mappingEditor.fill(mapping)
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect(page.getByRole('button', { name: '离线验证', exact: true })).toBeEnabled()
  expect(saves).toBe(1)
  const document = new ProtocolDocument(saved)
  expect(document.read('/schemaVersion')).toBe('4')
  expect(document.read('/spec')).toBe(spec)
  expect(document.read('/mapping')).toBe(mapping)
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '启用版本', exact: true }).click()
  await expect(page.getByText(`启用版本 ${revisionHash.slice(0, 12)}`, { exact: true })).toBeVisible()
  await mappingEditor.fill(mapping.replace('900719925474099312345', '900719925474099312346'))
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '检查定义', exact: true }).click()
  await page.getByLabel('协议诊断').getByRole('button').click()
  await expect(page.getByRole('tab', { name: '转换说明 mapping.json', exact: true })).toHaveAttribute('aria-selected', 'true')
  expect(await mappingEditor.evaluate((element: HTMLTextAreaElement) => element.value.slice(element.selectionStart, element.selectionEnd))).toBe('900719925474099312346')
  await page.getByRole('tab', { name: '转换预览', exact: true }).click()
  await page.getByLabel('预览类型', { exact: true }).selectOption('models')
  await expect(page.getByLabel('转换方向', { exact: true })).toHaveValue('decode_response')
  await page.getByLabel('转换方向', { exact: true }).selectOption('encode_response')
  await page.getByRole('textbox', { name: '目录操作名（decode_response 发现／encode_response 客户端输出）', exact: true }).fill('models')
  await page.getByRole('textbox', { name: '预览输入 JSON', exact: true }).fill('{"models":[{"id":"local"}]}')
  await page.getByRole('button', { name: '运行转换预览', exact: true }).click()
  await expect(page.getByLabel('转换链路结果')).toContainText('available')
  await page.getByLabel('预览类型', { exact: true }).selectOption('count_tokens')
  await page.getByRole('textbox', { name: '计数操作名（独立请求与结果映射）', exact: true }).fill('count')
  await page.getByRole('textbox', { name: '端点模型名（协议路径含模型时填写）', exact: true }).fill('count-model')
  const countInput = '{"inputTokens":9007199254740993}'
  await page.getByRole('textbox', { name: '预览输入 JSON', exact: true }).fill(countInput)
  for (const direction of ['decode_request', 'encode_request', 'decode_response', 'encode_response']) {
    await page.getByLabel('转换方向', { exact: true }).selectOption(direction)
    const request = page.waitForRequest((request) => request.url().endsWith('/protocols/preview'))
    await page.getByRole('button', { name: '运行转换预览', exact: true }).click()
    expect((await request).postDataJSON().direction).toBe(direction)
    await expect(page.getByLabel('转换链路结果')).toContainText('"inputTokens":9007199254740993')
  }
  await page.getByRole('tab', { name: '版本与回滚', exact: true }).click()
  await page.getByRole('tab', { name: '转换预览', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '预览输入 JSON', exact: true })).toHaveValue(countInput)
  await expect(page.getByLabel('转换方向', { exact: true })).toHaveValue('encode_response')
  await expect(page.getByLabel('转换链路结果')).toContainText('"inputTokens":9007199254740993')
})

test('browser creates, verifies, previews, activates and rolls back through the real protocol service', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL, 'Requires an isolated local backend and ELYSIA_DEV_PROXY')
  const token = process.env.PROTOCOL_E2E_TOKEN ?? 'local-protocol-e2e'
  const id = `browser-${Date.now()}`
  const fixture = readTextPackage(id)
  fixture.spec.name = 'Browser draft'
  let source = JSON.stringify(fixture, null, 2)
  source = new ProtocolDocument(source).set('/mapping/samples/0/context', '{"long":900719925474099312345,"values":[null,false,0,{},[]],"custom":{"nested":"preserve"}}')
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto('/#/protocols')
  await page.getByRole('button', { name: '新建协议', exact: true }).click()
  await page.getByRole('tab', { name: '完整 JSON', exact: true }).click()
  await page.getByRole('textbox', { name: '完整协议 JSON', exact: true }).fill(source)
  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await page.getByRole('textbox', { name: '名称', exact: true }).fill('Edited by form')
  await page.getByRole('tab', { name: '转换说明 mapping.json', exact: true }).click()
  const mapping = page.getByRole('textbox', { name: '转换说明 mapping.json JSON', exact: true })
  const original = await mapping.inputValue()
  await mapping.fill('{"invalid":')
  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await expect(page.getByRole('button', { name: '保存草稿', exact: true })).toBeDisabled()
  await page.getByRole('tab', { name: '转换说明 mapping.json', exact: true }).click()
  await expect(mapping).toHaveValue('{"invalid":')
  await mapping.fill(original)
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect(page.getByRole('button', { name: '离线验证', exact: true })).toBeEnabled()
  const saved = await request.get(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/${id}/draft`, { headers: { Authorization: `Bearer ${token}` } })
  expect(await saved.text()).toContain('900719925474099312345')
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '启用版本', exact: true }).click()
  await expect(page.getByText(/启用版本 [a-f0-9]{12}/)).toBeVisible()

  await page.getByRole('tab', { name: '转换预览', exact: true }).click()
  await page.getByRole('textbox', { name: '预览输入 JSON', exact: true }).fill(JSON.stringify(fixture.mapping.samples[0].input))
  await page.getByRole('button', { name: '运行转换预览', exact: true }).click()
  await expect(page.getByLabel('转换链路结果')).toContainText('hello')
  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await page.getByRole('checkbox', { name: 'tools.function', exact: true }).check()
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByLabel('协议诊断')).toContainText('incomplete_coverage')
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeDisabled()
  await page.getByLabel('协议诊断').getByRole('button').first().click()
  await expect(page.getByRole('tab', { name: '转换说明 mapping.json', exact: true })).toHaveAttribute('aria-selected', 'true')

  await page.getByRole('tab', { name: '基本信息', exact: true }).click()
  await page.getByRole('checkbox', { name: 'tools.function', exact: true }).uncheck()
  await page.getByRole('textbox', { name: '定义版本', exact: true }).fill('2')
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await page.getByRole('button', { name: '离线验证', exact: true }).click()
  await expect(page.getByRole('button', { name: '启用版本', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '启用版本', exact: true }).click()
  await page.getByRole('tab', { name: '版本与回滚', exact: true }).click()
  const choices = await page.getByRole('combobox', { name: '选择修订', exact: true }).locator('option').allTextContents()
  expect(choices.length).toBe(4)
  const revisionResponse = await request.get(`${process.env.PROTOCOL_E2E_URL}/api/admin/protocols/${id}/revisions`, { headers: { Authorization: `Bearer ${token}` } })
  const revisions = (await revisionResponse.json()).data.items
  const first = revisions.find((revision: { definition: { spec: { version: string }; mapping: { capabilities: Record<string, boolean> } } }) => revision.definition.spec.version === '1' && !revision.definition.mapping.capabilities['tools.function'])
  await page.getByRole('combobox', { name: '选择修订', exact: true }).selectOption(first.hash)
  await page.getByRole('button', { name: '比较启用版本', exact: true }).click()
  await expect(page.getByText('"/spec/version"', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '验证并回滚至所选修订', exact: true }).click()
  await expect(page.getByText(new RegExp(`启用版本 ${first.hash.slice(0, 12)}`))).toBeVisible()
})

for (const provider of [
  { name: 'anthropic', request: { model: 'count-model', messages: [{ role: 'user', content: 'hello' }] }, countField: 'input_tokens' },
  { name: 'google-generate-content', request: { contents: [{ role: 'user', parts: [{ text: 'hello' }] }] }, countField: 'totalTokens' },
]) {
  test(`real ${provider.name} package previews all token-count directions without generation`, async ({ page }) => {
    test.skip(!process.env.PROTOCOL_E2E_URL, 'requires an isolated real backend')
    await page.addInitScript((token) => localStorage.setItem('elysia-webui.panel-token', token), process.env.PROTOCOL_E2E_TOKEN!)
    const fixture = readProtocolPackage(`builtin/packages/${provider.name}`)
    fixture.spec.id = `count-browser-${provider.name}`
    await page.goto('/#/protocols')
    await page.getByRole('button', { name: '新建协议', exact: true }).click()
    await page.getByRole('tab', { name: '完整 JSON', exact: true }).click()
    await page.getByRole('textbox', { name: '完整协议 JSON', exact: true }).fill(JSON.stringify(fixture))
    await page.getByRole('tab', { name: '转换预览', exact: true }).click()
    await page.getByLabel('预览类型', { exact: true }).selectOption('count_tokens')
    await page.getByRole('textbox', { name: '计数操作名（独立请求与结果映射）', exact: true }).fill('count_tokens')
    await page.getByRole('textbox', { name: '端点模型名（协议路径含模型时填写）', exact: true }).fill('count-model')
    const preview = async (direction: string, input: string) => {
      await page.getByLabel('转换方向', { exact: true }).selectOption(direction)
      await page.getByRole('textbox', { name: '预览输入 JSON', exact: true }).fill(input)
      const response = page.waitForResponse((response) => response.url().endsWith('/api/admin/protocols/preview'))
      await page.getByRole('button', { name: '运行转换预览', exact: true }).click()
      const result = await (await response).json()
      expect(result.ok).toBe(true)
      expect(result.data.issues).toEqual([])
      return result.data.output
    }
    const request = await preview('decode_request', JSON.stringify(provider.request))
    expect(request.model).toBe('count-model')
    const wire = await preview('encode_request', JSON.stringify(request))
    expect(JSON.stringify(wire)).toContain('hello')
    expect(JSON.stringify(wire)).not.toMatch(/max_tokens|maxOutputTokens/)
    await preview('decode_response', `{"${provider.countField}":9007199254740993}`)
    await expect(page.getByLabel('转换链路结果')).toContainText('"inputTokens":9007199254740993')
    await preview('encode_response', '{"inputTokens":9007199254740993}')
    await expect(page.getByLabel('转换链路结果')).toContainText(`"${provider.countField}":9007199254740993`)
  })
}
