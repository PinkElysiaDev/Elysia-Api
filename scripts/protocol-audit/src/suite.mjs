import { writeFile, mkdir, rename } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { resolve, dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { randomUUID } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { AsyncLocalStorage } from 'node:async_hooks'
import { setTimeout as delay } from 'node:timers/promises'
import assert from 'node:assert/strict'
import { runAudit, planAudit, redactor, codeCheck, exchange, runLogger } from './audit.mjs'
import { protocols, requestBody, followupBody, endpoint, inspectReply } from './protocols.mjs'
import { localInstance } from './local.mjs'
import { mapConcurrent, serialize } from './concurrency.mjs'

export const groups = ['daily', 'protocol', 'sdk', 'errors', 'persistence', 'code']
const toolDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const blocked = message => Object.assign(new Error(message), { code: 'blocked' })

export function checkReply(result, protocol, { stream = false, expectedText, requireUsage = true } = {}) {
  if (result.error) throw result.error
  assert.equal(result.status, 200, result.raw.slice(0, 1500))
  if (stream) assert.match(result.headers['content-type'] || '', /text\/event-stream/i)
  const reply = inspectReply(protocol, result.raw, stream, requireUsage)
  assert.deepEqual(reply.issues, [], JSON.stringify(reply.issues))
  assert.ok(reply.text.trim(), 'Expected a nonempty response')
  if (expectedText !== undefined) assert.ok(reply.text.includes(expectedText), `Expected ${expectedText}, got ${reply.text}`)
  return reply
}

export function planSuite(input, group = 'all', configDir = process.cwd()) {
  assert.ok(input && typeof input === 'object' && !Array.isArray(input), 'Config must be an object')
  const allowed = ['projectRoot', 'targets', 'codeChecks', 'concurrency', 'timeoutMs', 'maxResponseBytes', 'maxRequests', 'maxOutputTokens', 'requireUsage', 'scenarios', 'streams']
  for (const key of Object.keys(input)) assert.ok(allowed.includes(key), `Unknown configuration field: ${key}`)
  assert.ok(Array.isArray(input.targets ?? []), 'targets must be an array')
  assert.ok(group === 'all' || groups.includes(group), `Unknown group: ${group}; choose ${groups.join(', ')}`)
  assert.ok(input.projectRoot === undefined || typeof input.projectRoot === 'string', 'projectRoot must be a path')
  let root = input.projectRoot ? resolve(configDir, input.projectRoot) : resolve(configDir)
  if (!input.projectRoot) {
    for (;;) { if (existsSync(join(root, 'backend', 'go.mod')) || dirname(root) === root) break; root = dirname(root) }
    if (!existsSync(join(root, 'backend', 'go.mod'))) root = resolve(toolDir, '../..')
  }
  const { projectRoot: _, ...rest } = input
  const targets = (input.targets ?? []).map((t, i) => {
    assert.ok(t && typeof t === 'object' && !Array.isArray(t), `targets[${i}] must be an object`)
    return { id: `${t.protocol}-${i + 1}`, ...t }
  })
  const api = { ...rest, scenarios: input.scenarios ?? ['models', 'text', 'multiturn', 'tools', 'system', 'chinese', 'history'], targets }
  const validated = planAudit({ ...api, targets: api.targets.length ? api.targets : [{ id: 'validation', protocol: 'chat', baseUrl: 'http://127.0.0.1', model: 'test', auth: 'none' }] })
  if (group !== 'code') {
    const missing = protocols.filter(protocol => !targets.some(t => t.protocol === protocol))
    assert.ok(!missing.length, `请填写四种协议的渠道配置；缺少：${missing.join(', ')}。编辑 config.local.json；仅检查代码可用 --group code`)
    for (const target of targets) {
      const hostname = new URL(target.baseUrl).hostname
      assert.ok(!/(^|\.)example$|(^|\.)example\.(com|net|org)$/i.test(hostname), `${target.id}.baseUrl 仍为示例地址，请填写真实渠道地址`)
      for (const field of ['model', 'apiKey']) assert.ok(!/^YOUR_/i.test(target[field] || ''), `${target.id}.${field} 仍为占位符，请填写真实值`)
    }
  }
  return { root, groups: group === 'all' ? [...groups] : [group], api: { ...validated.config, targets: api.targets }, configDir }
}

function reportMarkdown(report) {
  const cell = value => String(value ?? '').replaceAll('|', '\\|').replace(/[\r\n]+/g, ' ').replaceAll('<', '&lt;')
  const lines = ['# 自动回归测试报告', '', `- 状态：${report.status}`, `- 开始：${report.startedAt}`, `- 项目提交：${report.project.commit || 'unknown'}${report.project.dirty ? '（含本地修改）' : ''}`, `- 分组：${report.groups.join(', ')}`, `- 结果（不含汇总项）：${JSON.stringify(report.counts)}`, '- [完整执行日志](run.log)', '', '| 用例 | 证据来源 | 状态 | 预期 | 实际 / 问题 | 详细日志 |', '|---|---|---|---|---|---|']
  for (const item of report.cases) lines.push(`| ${cell(item.id)}${item.summary ? '（汇总）' : ''} | ${cell(item.source)} | ${item.status} | ${cell(item.expected)} | ${cell(item.error?.message || item.actual || '')} | ${(item.evidence || []).map((p, i) => `[${i + 1}](${p})`).join(' ')} |`)
  lines.push('', '## 失败及未完成项目', '')
  for (const item of report.cases.filter(c => !['passed', 'not_applicable'].includes(c.status))) {
    lines.push(`### ${cell(item.id)}`, '', `状态：${item.status}；${cell(item.error?.message || item.actual || '尚未完成')}`, '')
    for (const evidence of item.evidence || []) lines.push(`- [证据](${evidence})`)
    lines.push('')
  }
  lines.push('## 覆盖边界', '', '本轮使用配置的真实渠道和真实临时后端；源码检查另行标注。预期错误被正确处理时用例通过。blocked/skipped/running 不代表通过。', '', '不主动制造上游 429、503、超时或断流；实际发生时记录失败，未发生不代表这些异常已验证。取消测试只验证客户端结束请求及后续可用性，无法确认供应商停止生成或计费。日常流程通过管理 API 检查，不包含浏览器交互、长期压测和安装包。证据按已知密钥及签名字段脱敏，分享前请检查其他私有信息。', '')
  return lines.join('\n')
}

export async function runSuite(input, { group = 'all', configDir = process.cwd(), outputDir, signal, env = process.env, onProgress = () => {} } = {}) {
  const plan = planSuite(input, group, configDir)
  const replyOptions = { requireUsage: plan.api.requireUsage }
  if (group !== 'code') for (const target of plan.api.targets) {
    for (const name of [target.apiKeyEnv, ...Object.values(target.headersEnv || {})].filter(Boolean)) {
      assert.ok(typeof env[name] === 'string' && env[name].trim() && !/[\r\n]/.test(env[name]) && !/^YOUR_/i.test(env[name]), `${target.id} 缺少有效环境变量 ${name}；请设置后重跑`)
    }
  }
  outputDir = resolve(outputDir || join(toolDir, 'results', `${new Date().toISOString().replaceAll(':', '-')}-${randomUUID().slice(0, 8)}`))
  await mkdir(dirname(outputDir), { recursive: true }); await mkdir(outputDir, { mode: 0o700 }); await mkdir(join(outputDir, 'evidence'))
  const secrets = plan.api.targets.flatMap(t => [t.apiKey, ...[t.apiKeyEnv, ...Object.values(t.headersEnv || {})].filter(Boolean).map(k => env[k])]).filter(Boolean)
  let clean = redactor(secrets), instance, counter = 0
  const currentCase = new AsyncLocalStorage()
  const log = runLogger(outputDir, value => clean(value))
  log(`RUN START groups=${plan.groups.join(',')} concurrency=${plan.api.concurrency} project=${plan.root}`)
  const git = args => { const r = spawnSync('git', args, { cwd: plan.root, encoding: 'utf8' }); return r.status === 0 ? r.stdout.trim() : '' }
  const report = { startedAt: new Date().toISOString(), status: 'running', runtime: { node: process.version, platform: process.platform, arch: process.arch }, project: { root: plan.root, commit: git(['rev-parse', 'HEAD']), dirty: !!git(['status', '--porcelain']) }, groups: plan.groups, configuration: { ...input, concurrency: plan.api.concurrency, targets: plan.api.targets }, cases: [] }
  const save = serialize(async () => {
    report.counts = Object.fromEntries(['passed', 'failed', 'blocked', 'skipped', 'not_applicable', 'running'].map(status => [status, report.cases.filter(c => !c.summary && c.status === status).length]))
    const snapshot = clean(report)
    for (const [name, body] of [['report.json', JSON.stringify(snapshot, null, 2)], ['report.md', reportMarkdown(snapshot)]]) {
      await writeFile(join(outputDir, `${name}.tmp`), body + '\n', { mode: 0o600 }); await rename(join(outputDir, `${name}.tmp`), join(outputDir, name))
    }
  })
  async function evidence(name, value, owner = currentCase.getStore()) {
    const path = `evidence/${String(++counter).padStart(4, '0')}-${name}`
    await writeFile(join(outputDir, path), typeof value === 'string' ? clean(value) : JSON.stringify(clean(value), null, 2), { mode: 0o600 })
    owner?.evidence.push(path)
    return path
  }
  async function check(group, name, expected, fn, source = 'live') {
    const item = { id: `${group}/${name}`, group, source, expected, status: 'running', evidence: [], startedAt: new Date().toISOString() }
    report.cases.push(item)
    log(`START ${item.id}: ${expected}`)
    if (signal?.aborted) { item.status = 'skipped'; item.actual = 'interrupted'; log(`END ${item.id} skipped: interrupted`); await save(); return false }
    await save(); const start = performance.now()
    try { item.actual = await currentCase.run(item, () => fn(item)) || '符合预期'; item.status = 'passed' }
    catch (error) { item.status = error.code === 'blocked' ? 'blocked' : signal?.aborted ? 'skipped' : 'failed'; item.error = { code: error.code || error.name, message: error.message, stack: error.stack }; if (error.sdk) item.sdk = error.sdk; if (error.backendLog) await evidence('backend.log', error.backendLog, item) }
    item.elapsedMs = Math.round(performance.now() - start); item.finishedAt = new Date().toISOString()
    log(`END ${item.id} ${item.status} elapsedMs=${item.elapsedMs}${item.error ? ` error=${item.error.stack || item.error.message}` : ''}`)
    await save(); onProgress(clean(`${item.status}: ${item.id}${item.error ? ` — ${item.error.message}` : ''}`))
    return item.status === 'passed'
  }
  async function command(name, argv, cwd, timeoutMs = 600000) {
    const active = currentCase.getStore()
    log(`COMMAND START ${active.id} cwd=${cwd} argv=${JSON.stringify(argv)}`)
    const isolatedEnv = { ...env, ...Object.fromEntries(Object.keys({ ...process.env, ...env }).filter(k => k.startsWith('ELYSIA_') || k === 'UPDATE_AGENT_CLI_DOCS').map(k => [k, ''])) }
    const result = await codeCheck({ command: argv, cwd, timeoutMs }, plan.root, plan.api.maxResponseBytes, signal, isolatedEnv)
    const path = await evidence(`${name}.log`, result.raw)
    log(`COMMAND END ${active.id} exit=${result.exitCode} evidence=${path}`)
    active.command = argv; active.cwd = cwd; active.exitCode = result.exitCode
    if (result.error) throw result.error
  }
  async function http(path, body, { token = instance.panel, method, rawBody, expected, timeoutMs = plan.api.timeoutMs, requestSignal = signal, onChunk } = {}) {
    const active = currentCase.getStore()
    log(`HTTP START ${active.id} ${method || (body === undefined && rawBody === undefined ? 'GET' : 'POST')} ${path}`)
    const headers = { authorization: `Bearer ${token}`, 'content-type': 'application/json' }, url = `${instance.baseUrl}${path}`
    const result = await exchange(url, body, headers, { ...plan.api, timeoutMs }, requestSignal, { method, rawBody, onChunk })
    const file = await evidence('http.json', { caseId: active.id, request: { url, method: method || (body === undefined && rawBody === undefined ? 'GET' : 'POST'), headers, body, rawBody }, response: { ...result, error: result.error?.message } })
    log(`HTTP END ${active.id} status=${result.status} elapsedMs=${result.elapsedMs} evidence=${file}${result.error ? ` error=${result.error.message}` : ''}`)
    if (expected !== undefined) {
      if (result.error) throw result.error
      assert.ok((Array.isArray(expected) ? expected : [expected]).includes(result.status), `Expected HTTP ${JSON.stringify(expected)}, got ${result.status}: ${result.raw.slice(0, 1500)}`)
    }
    try { result.json = JSON.parse(result.raw) } catch { /* SSE and malformed responses remain raw evidence. */ }
    return result
  }
  async function admin(path, body, method) {
    const result = await http(`/api/admin/${path}`, body, { expected: 200, method })
    assert.equal(result.json?.ok, true, `Management API rejected ${path}: ${result.raw.slice(0, 1000)}`)
    return result.json.data
  }
  const ready = () => { if (!instance) throw blocked('临时后端准备失败；查看 setup 用例') }
  function source(target, id) {
    const key = target.apiKey ?? env[target.apiKeyEnv]
    if (target.auth !== 'none' && (!key || /[\r\n]/.test(key))) throw blocked(`渠道 ${target.id} 缺少有效密钥：填写 apiKey 或设置 ${target.apiKeyEnv}`)
    if (Object.keys(target.headersEnv || {}).length) throw blocked('暂不支持 headersEnv；请使用标准密钥认证的渠道')
    return { id, name: id, baseUrl: target.baseUrl, apiKey: key || '', platform: target.protocol === 'chat' ? 'chat_completions' : target.protocol, enabled: true, autoFetchModels: false, manualModels: [{ id: target.model, name: target.model, type: 'llm', enabled: true, available: true, toolsCapable: true, visionCapable: target.vision === true }] }
  }
  async function route(target, id) {
    ready()
    await admin('model-sources', source(target, id))
    await admin('model-groups', { id, name: id, enabled: true, models: [`${id}:${target.model}`], strategy: 'sequential', maxRetries: 0, type: 'llm', toolsCapable: true, visionCapable: target.vision === true })
    return { id, model: id, upstreamTarget: target.id }
  }
  async function call(protocol, model, stream = false, options = {}, body) {
    const url = new URL(endpoint(instance.baseUrl, protocol, model, stream))
    return http(url.pathname + url.search, body || requestBody(protocol, model, stream, 'text', '', plan.api.maxOutputTokens), { token: instance.token, ...options })
  }
  await save()
  try {
    if (plan.groups.some(g => g !== 'code')) {
      const ok = await check('setup', 'isolated-backend', '编译并启动真实临时后端，使用独立数据库', async () => {
        if (!existsSync(join(plan.root, 'backend/go.mod'))) throw blocked('找不到源码：请设置 projectRoot')
        instance = await localInstance(plan.root, command, signal, plan.api.timeoutMs, plan.api.maxResponseBytes)
        secrets.push(instance.panel, instance.token); clean = redactor(secrets)
        await admin('api-tokens', { name: 'audit-relay', token: instance.token, enabled: true, allowedGroups: [], scopes: [] })
      }, 'source-code')
      if (!ok && instance) { await instance.close(); instance = undefined }
    }
    for (const current of plan.groups) {
      log(`GROUP ${current}`); onProgress(`Running group: ${current}`)
      const routes = [], sdkTasks = []
      let consume
      if (current === 'sdk') {
        try { consume = (await import('./sdk.mjs')).consume } catch (error) {
          await check(current, 'dependencies', 'SDK 依赖可加载', async () => { throw blocked(`请先执行 npm install --prefix scripts/protocol-audit：${error.message}`) }); continue
        }
      }
      for (const [index, target] of (current === 'code' ? [] : plan.api.targets).entries()) {
        let id = `audit-${index + 1}`
        for (let suffix = 1; plan.api.targets.some(t => t.id === id); suffix++) id = `audit-${index + 1}-${suffix}`
        const protocol = target.protocol
        const run = (name, expected, fn) => check(current, `${target.id}/${name}`, expected, fn)
        const configured = await run('configure', '根据真实渠道配置创建源和模型组', () => route(target, id).then(() => undefined))
        if (configured) routes.push({ id, model: id, upstreamTarget: target.id })
        const needRoute = () => { ready(); if (!configured) throw blocked('渠道准备失败；查看本组 configure 用例') }
        const recovery = async () => checkReply(await call(protocol, id), protocol, replyOptions)
        if (current === 'daily') {
          let called = false
          await run('token-and-call', '创建独立令牌后成功对话', async () => {
            needRoute(); const token = randomUUID(); secrets.push(token); clean = redactor(secrets)
            await admin('api-tokens', { name: `daily-${id}`, token, enabled: true, allowedGroups: [id], scopes: [] })
            checkReply(await call(protocol, id, false, { token }), protocol, replyOptions); called = true
          })
          await run('model-discovery', '从真实上游拉取非空模型列表并保存', async () => {
            needRoute(); const discovery = `${id}-discovery`
            await admin('model-sources', { ...source(target, discovery), autoFetchModels: true, manualModels: [] })
            await admin(`model-sources/${discovery}/fetch`, {})
            let state; const deadline = Date.now() + plan.api.timeoutMs
            do {
              const listed = await admin('model-sources')
              state = listed.items.find(s => s.id === discovery)?.refreshState
              if (state?.lastFinishedAt && !state.refreshing) break
              await delay(250, undefined, { signal })
            } while (Date.now() < deadline)
            assert.ok(state?.lastFinishedAt && !state.refreshing, '模型拉取没有在等待时间内完成')
            assert.ok(!state.lastError, state.lastError)
            const models = await admin(`models?sourceId=${discovery}`)
            assert.ok(models.items?.length, '拉取后模型列表为空')
          })
          await run('usage-and-logs', '成功请求有对应的用量和日志', async () => {
            needRoute(); if (!called) throw blocked('依赖 token-and-call 成功')
            let rows
            for (let i = 0; i < 30; i++) { rows = await admin(`usage/logs?limit=100&groupName=${id}`); if (rows.items?.some(r => r.statusCode === 200)) break; await delay(100, undefined, { signal }) }
            assert.ok(rows.items?.some(r => r.statusCode === 200), '未找到成功请求的用量日志')
            assert.ok(await admin(`usage/stats?groupName=${id}`))
          })
          await run('session-isolation', '独立的两轮会话分别返回各自的记忆码', async () => {
            needRoute()
            await mapConcurrent([0, 1], plan.api.concurrency, async () => {
              const marker = `AUDIT_${randomUUID().replaceAll('-', '').slice(0, 12)}`
              const body = requestBody(protocol, id, false, 'multiturn', marker, plan.api.maxOutputTokens)
              const first = checkReply(await call(protocol, id, false, {}, body), protocol, replyOptions)
              checkReply(await call(protocol, id, false, {}, followupBody(protocol, body, first, 'multiturn', marker)), protocol, { ...replyOptions, expectedText: marker })
            })
          })
          await run('stream', '流式响应结构、内容和结束事件正确，并记录首字节耗时', async () => {
            needRoute(); const result = await call(protocol, id, true); checkReply(result, protocol, { ...replyOptions, stream: true })
            return `firstByteMs=${result.firstByteMs}; elapsedMs=${result.elapsedMs}；仅记录客户端观测，不推断上游是否缓冲`
          })
        }
        if (current === 'sdk') for (const ingress of protocols) for (const stream of plan.api.streams) sdkTasks.push(() => run(`${ingress}-${stream ? 'sse' : 'json'}`, '实际 SDK 发送请求并消费真实网关响应', async item => {
          needRoute()
          item.sdk = await consume(ingress, { baseUrl: instance.baseUrl, apiKey: instance.token, model: id, ...plan.api, signal, onExchange: async detail => {
            const path = await evidence('sdk-http.json', { caseId: item.id, ...detail }, item)
            log(`SDK HTTP ${item.id} status=${detail.response?.status ?? 'none'} elapsedMs=${detail.elapsedMs} evidence=${path}${detail.error ? ` error=${detail.error}` : ''}`)
          } }, stream)
          return JSON.stringify(item.sdk)
        }))
        if (current === 'errors') {
          for (const [name, expected, fn] of [
            ['wrong-key', '错误网关令牌返回 401，后续正常调用成功', () => call(protocol, id, false, { token: 'audit-invalid', expected: 401 })],
            ['unknown-model', '无效模型返回 4xx，后续正常调用成功', () => call(protocol, 'audit-model-does-not-exist', false, { expected: [400, 404] })],
            ['invalid-json', '非法 JSON 返回 400，后续正常调用成功', () => call(protocol, id, false, { rawBody: '{bad json', expected: 400 })],
            ['cancel', '客户端取消流式请求后仍可正常调用', async () => {
              const abort = new AbortController()
              const result = await call(protocol, id, true, { requestSignal: AbortSignal.any([abort.signal, ...(signal ? [signal] : [])]), onChunk: () => abort.abort() })
              assert.equal(result.status, 200, result.raw.slice(0, 1500)); assert.ok(abort.signal.aborted && result.bytes > 0, '未收到可取消的流内容')
              if (result.error && result.error.code !== 'interrupted') throw result.error
            }],
          ]) await run(name, expected, async () => { needRoute(); await fn(); await recovery() })
        }
        if (current === 'persistence') {
          let called = false
          await run('call', '原有源、模型组和令牌调用成功', async () => { needRoute(); await recovery(); called = true })
          await run('restart', '重启后原有配置和令牌可直接使用', async () => { needRoute(); if (!called) throw blocked('依赖 call 成功'); await instance.restart(); await recovery() })
          await run('update-and-disable', '配置修改保存，禁用阻止调用，重新启用后恢复', async () => {
            needRoute()
            await admin(`model-sources/${id}`, { ...source(target, id), name: 'Updated audit source' }, 'PUT')
            assert.equal((await admin('model-sources')).items.find(s => s.id === id)?.name, 'Updated audit source')
            await admin(`model-sources/${id}/enabled`, { enabled: false }, 'PATCH')
            try { const result = await call(protocol, id); assert.ok(!result.error, result.error?.message); assert.ok(result.status >= 400, '禁用源后仍可调用') }
            finally { await admin(`model-sources/${id}/enabled`, { enabled: true }, 'PATCH') }
            await recovery()
          })
          await run('revoke-token', '删除令牌后立即拒绝该令牌', async () => {
            needRoute(); const token = randomUUID(); secrets.push(token); clean = redactor(secrets)
            await admin('api-tokens', { name: `revoke-${id}`, token, enabled: true, allowedGroups: [id], scopes: [] })
            checkReply(await call(protocol, id, false, { token }), protocol, replyOptions)
            await admin(`api-tokens/revoke-${id}`, undefined, 'DELETE')
            await call(protocol, id, false, { token, expected: 401 }); await recovery()
          })
        }
      }
      if (current === 'protocol') await check(current, 'matrix', '所有渠道直连及四种客户端协议的转换场景分别通过', async item => {
        const path = join(outputDir, 'protocol')
        const config = { ...plan.api, codeChecks: [], ...(routes.length ? { gateway: { baseUrl: instance.baseUrl, apiKey: instance.token, routes } } : {}) }
        const result = await runAudit(config, { outputDir: path, env, signal, onProgress: c => { log(`${c.status} ${c.id}${c.issues.length ? ` issues=${JSON.stringify(c.issues)}` : ''}`); onProgress(`${c.status}: ${c.id}`) } })
        item.summary = true
        const prefix = relative(outputDir, path).split('\\').join('/')
        item.evidence.push(`${prefix}/report.md`, `${prefix}/run.log`)
        for (const c of result.cases) report.cases.push({ ...c, id: `${current}/${c.id}`, group: current, source: 'live', expected: '响应结构和场景断言通过', evidence: c.evidence.map(p => `${prefix}/${p}`), ...(c.issues.length ? { error: { code: c.issues[0].code, message: c.issues.map(i => `${i.path}: ${i.message}`).join('\n') } } : {}) })
        if (result.status === 'incomplete') throw blocked('请求预算不足，部分协议用例未执行')
        if (result.status === 'interrupted') throw new Error('Run interrupted')
        assert.equal(result.status, 'passed', `${result.counts.failed} 个协议用例失败，见详细报告`)
      })
      if (current === 'sdk') await mapConcurrent(sdkTasks, plan.api.concurrency, task => task())
      if (current === 'code') {
        const defaults = [
          { id: 'backend-tests', command: ['go', 'test', './...', '-count=1'], cwd: 'backend' },
          { id: 'backend-vet', command: ['go', 'vet', './...'], cwd: 'backend' },
          { id: 'frontend-types', command: [process.execPath, join(plan.root, 'node_modules/typescript/bin/tsc'), '--noEmit'], cwd: 'packages/webui' },
          { id: 'frontend-lint', command: [process.execPath, join(plan.root, 'node_modules/eslint/bin/eslint.js'), '.', '--ext', 'ts,tsx', '--max-warnings', '0'], cwd: 'packages/webui' },
          { id: 'frontend-build', command: process.platform === 'win32' ? ['cmd.exe', '/d', '/c', 'npm', 'run', 'build:webui'] : ['npm', 'run', 'build:webui'], cwd: '.' },
        ]
        for (const task of input.codeChecks ?? defaults) await check(current, task.id, '项目检查命令退出码为 0', async () => {
          if (!existsSync(join(plan.root, 'backend/go.mod'))) throw blocked('找不到源码；请配置 projectRoot')
          if (task.id.startsWith('frontend-') && !existsSync(join(plan.root, 'node_modules'))) throw blocked('项目依赖未安装；请先在项目根目录执行 npm install')
          await command(task.id, task.command, resolve(plan.root, task.cwd || '.'), task.timeoutMs)
        }, 'source-code')
        if (input.codeChecks?.length === 0) await check(current, 'not-configured', '至少配置一项代码检查', async () => { throw blocked('codeChecks 为空；删除该字段可使用默认检查') }, 'source-code')
      }
    }
  } catch (error) {
    report.cases.push({ id: 'runner/fatal', group: 'runner', source: 'runner', status: 'failed', error: { message: error.message, stack: error.stack }, evidence: [] })
    log(`FATAL ${error.stack || error.message}`)
  } finally {
    if (instance) {
      try { await instance.close(); log('BACKEND stopped and temporary data removed') }
      catch (error) { report.cases.push({ id: 'runner/cleanup', group: 'runner', source: 'runner', status: 'failed', error: { message: error.message }, evidence: [] }); log(`CLEANUP ERROR ${error.message}`) }
      await evidence('backend.log', instance.log(), report.cases.find(c => c.group === 'setup'))
    }
    report.status = signal?.aborted ? 'interrupted' : report.cases.some(c => c.status === 'failed') ? 'failed' : report.cases.some(c => !['passed', 'not_applicable'].includes(c.status)) ? 'incomplete' : 'passed'
    report.finishedAt = new Date().toISOString(); await save()
    log(`RUN END ${report.status} counts=${JSON.stringify(report.counts)}`)
  }
  return clean(report)
}

export async function mainSuite(input, options) {
  const configDir = options.config ? dirname(resolve(options.config)) : process.cwd()
  const plan = planSuite(input, options.group || 'all', configDir)
  console.log(`Groups: ${plan.groups.join(', ')}\nProject: ${plan.root}\nConcurrency: ${plan.api.concurrency}`)
  if (options.dryRun) { console.log(`Real channels: ${plan.api.targets.length}; automatic isolated Elysia backend. No requests or commands executed.`); return }
  const outputDir = resolve(options.out || join(toolDir, 'results', `${new Date().toISOString().replaceAll(':', '-')}-${randomUUID().slice(0, 8)}`))
  console.log(`Report: ${join(outputDir, 'report.md')}\nLog: ${join(outputDir, 'run.log')}`)
  const controller = new AbortController(), abort = () => controller.abort()
  process.once('SIGINT', abort); process.once('SIGTERM', abort)
  try {
    const report = await runSuite(input, { group: options.group || 'all', configDir, outputDir, signal: controller.signal, onProgress: console.log })
    console.log(`Report: ${join(outputDir, 'report.md')}\nResult: ${report.status}`)
    process.exitCode = report.status === 'passed' ? 0 : report.status === 'failed' ? 1 : report.status === 'interrupted' ? 130 : 2
  } finally { process.off('SIGINT', abort); process.off('SIGTERM', abort) }
}
