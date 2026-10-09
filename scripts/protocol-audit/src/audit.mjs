import { writeFile, mkdir, rename } from 'node:fs/promises'
import { appendFileSync } from 'node:fs'
import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { randomUUID } from 'node:crypto'
import { spawn } from 'node:child_process'
import { protocols, scenarios, AuditError, endpoint, requestBody, followupBody, inspectReply, inspectModels } from './protocols.mjs'
import { mapConcurrent, serialize } from './concurrency.mjs'

export const defaults = { concurrency: 32, timeoutMs: 180000, maxResponseBytes: 16 * 1024 * 1024, maxRequests: 512, maxOutputTokens: 32768, requireUsage: true }

const object = value => value && typeof value === 'object' && !Array.isArray(value)
const validate = (condition, message) => { if (!condition) throw new Error(`Configuration: ${message}`) }
const issue = error => ({ code: error.code || 'runner_error', path: error.path || '', message: error.message || String(error) })

function fields(value, allowed, name) {
  validate(object(value), `${name} must be an object`)
  for (const key of Object.keys(value)) validate(allowed.includes(key), `unknown field ${name}.${key}`)
}

function connection(value, name) {
  let url
  try { url = new URL(value.baseUrl) } catch { throw new Error(`Configuration: invalid ${name}.baseUrl`) }
  validate(['https:', 'http:'].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash, `${name}.baseUrl must be HTTP(S), without credentials, query or fragment`)
  validate(value.auth === undefined || value.auth === 'none', `${name}.auth only supports "none" (otherwise protocol authentication is used)`)
  validate([value.apiKey !== undefined, value.apiKeyEnv !== undefined, value.auth === 'none'].filter(Boolean).length === 1, `${name}: choose apiKey, apiKeyEnv or auth:"none"`)
  if (value.apiKey !== undefined) validate(typeof value.apiKey === 'string' && value.apiKey.trim() && !/[\r\n]/.test(value.apiKey), `${name}.apiKey must be a nonempty single-line string`)
  if (value.apiKeyEnv !== undefined) validate(typeof value.apiKeyEnv === 'string' && /^[A-Za-z_][A-Za-z0-9_]*$/.test(value.apiKeyEnv), `${name}.apiKeyEnv must be an environment variable name`)
  if (value.headersEnv !== undefined) {
    validate(object(value.headersEnv), `${name}.headersEnv must be an object`)
    for (const [header, env] of Object.entries(value.headersEnv)) {
      validate(/^[\w-]+$/.test(header) && !/^(authorization|x-api-key|x-goog-api-key|host|content-length|cookie)$/i.test(header), `${name}.headersEnv contains a reserved or invalid header`)
      validate(typeof env === 'string' && /^[A-Za-z_][A-Za-z0-9_]*$/.test(env), `${name}.headersEnv values must be environment variable names`)
    }
  }
}

export function planAudit(input) {
  fields(input, [...Object.keys(defaults), 'scenarios', 'streams', 'targets', 'gateway', 'codeChecks'], 'config')
  const config = { ...defaults, scenarios: ['models', 'text', 'multiturn', 'tools'], streams: [false, true], targets: [], codeChecks: [], ...structuredClone(input) }
  validate(Number.isInteger(config.concurrency) && config.concurrency > 0, 'concurrency must be a positive integer')
  for (const [name, minimum, maximum] of [['timeoutMs', 1, 3600000], ['maxResponseBytes', 128, 64 * 1024 * 1024], ['maxRequests', 0, 10000], ['maxOutputTokens', 1, 1000000]]) validate(Number.isInteger(config[name]) && config[name] >= minimum && config[name] <= maximum, `${name} must be an integer between ${minimum} and ${maximum}`)
  validate(typeof config.requireUsage === 'boolean', 'requireUsage must be boolean')
  for (const [name, allowed] of [['scenarios', scenarios], ['streams', [false, true]]]) validate(Array.isArray(config[name]) && config[name].length > 0 && new Set(config[name]).size === config[name].length && config[name].every(v => allowed.includes(v)), `invalid or duplicate ${name}`)
  validate(Array.isArray(config.targets) && Array.isArray(config.codeChecks), 'targets and codeChecks must be arrays')
  const cases = [], ids = new Set(), sources = new Map()
  const id = value => { validate(typeof value === 'string' && /^[a-zA-Z0-9_-]{1,64}$/.test(value) && !ids.has(value), 'IDs must be unique, 1–64 ASCII letters/digits/underscores/hyphens'); ids.add(value) }
  const add = (target, kind, protocol, upstream) => {
    for (const scenario of config.scenarios) for (const stream of scenario === 'models' ? [false] : config.streams) cases.push({ id: `${kind}/${target.id}/${protocol}/${scenario}/${stream ? 'sse' : 'json'}`, kind, protocol, target: target.id, upstream, model: target.model, scenario, stream, maximumRequests: ['tools', 'multiturn'].includes(scenario) ? 2 : 1, connection: target, ...(scenario === 'image' && !(kind === 'gateway' ? sources.get(upstream) : target)?.vision ? { skipReason: '目标未声明 vision:true，图片场景不适用' } : {}) })
  }
  for (const target of config.targets) {
    fields(target, ['id', 'protocol', 'baseUrl', 'model', 'apiKey', 'apiKeyEnv', 'auth', 'headersEnv', 'vision'], 'target')
    validate(target.vision === undefined || typeof target.vision === 'boolean', 'target.vision must be boolean')
    id(target.id); connection(target, target.id)
    validate(protocols.includes(target.protocol), `${target.id}.protocol must be one of ${protocols.join(', ')}`)
    validate(typeof target.model === 'string' && target.model.trim(), `${target.id}.model is required`)
    sources.set(target.id, target)
    add(target, 'direct', target.protocol, target.id)
  }
  if (config.gateway) {
    const gateway = config.gateway
    fields(gateway, ['baseUrl', 'apiKey', 'apiKeyEnv', 'auth', 'headersEnv', 'protocols', 'routes'], 'gateway')
    connection(gateway, 'gateway')
    const ingress = gateway.protocols || protocols
    validate(Array.isArray(ingress) && ingress.length && new Set(ingress).size === ingress.length && ingress.every(p => protocols.includes(p)), 'gateway.protocols is invalid')
    validate(Array.isArray(gateway.routes) && gateway.routes.length, 'gateway.routes is required')
    for (const route of gateway.routes) {
      fields(route, ['id', 'model', 'upstreamTarget'], 'route'); id(route.id)
      validate(typeof route.model === 'string' && route.model.trim(), `${route.id}.model is required`)
      validate(sources.has(route.upstreamTarget), `${route.id}.upstreamTarget must reference a target ID`)
      for (const protocol of ingress) add({ ...gateway, ...route }, 'gateway', protocol, route.upstreamTarget)
    }
  }
  for (const check of config.codeChecks) {
    fields(check, ['id', 'command', 'cwd', 'timeoutMs'], 'codeCheck'); id(check.id)
    validate(Array.isArray(check.command) && check.command.length && check.command.every(s => typeof s === 'string' && s.length > 0 && !s.includes('\0')), `${check.id}.command must be a nonempty string array`)
    validate(check.cwd === undefined || typeof check.cwd === 'string', `${check.id}.cwd must be a string`)
    validate(check.timeoutMs === undefined || Number.isInteger(check.timeoutMs) && check.timeoutMs > 0 && check.timeoutMs <= 7200000, `${check.id}.timeoutMs is invalid`)
  }
  validate(cases.length > 0, 'no tests selected')
  return { config, cases, maximumRequests: cases.reduce((sum, item) => sum + item.maximumRequests, 0) }
}

const sensitive = /^(authorization|proxy-authorization|api[-_]?key|x-api-key|x-goog-api-key|access[-_]?token|refresh[-_]?token|id[-_]?token|token|password|secret|cookie|set-cookie|signature|thought_signature|thoughtSignature|encrypted_content|elysia_continuation)$/i

export function redactor(secrets) {
  const values = [...new Set(secrets.flatMap(s => [s, JSON.stringify(s).slice(1, -1), encodeURIComponent(s)]))].filter(Boolean).sort((a, b) => b.length - a.length)
  const text = raw => {
    let result = String(raw)
    for (const secret of values) result = result.split(secret).join('[REDACTED]')
    try {
      const parsed = JSON.parse(result)
      if (object(parsed) || Array.isArray(parsed)) return JSON.stringify(clean(parsed))
    } catch { /* Non-JSON logs and SSE still receive field/credential masking. */ }
    result = result.replace(/^data: ?(.+)$/gm, (line, data) => {
      try { return `data: ${JSON.stringify(clean(JSON.parse(data)))}` } catch { return line }
    })
    result = result.replace(/(Bearer\s+)[^\s"'\\]+/gi, '$1[REDACTED]')
    return result.replace(/("(?:api[-_]?key|x-api-key|x-goog-api-key|access[-_]?token|refresh[-_]?token|id[-_]?token|token|authorization|proxy-authorization|password|secret|cookie|set-cookie|signature|thoughtSignature|thought_signature|encrypted_content|elysia_continuation)"\s*:\s*)"(?:\\.|[^"\\])*"/gi, '$1"[REDACTED]"')
  }
  const clean = value => {
    if (typeof value === 'string') return text(value)
    if (Array.isArray(value)) return value.map(clean)
    if (object(value)) return Object.fromEntries(Object.entries(value).map(([key, val]) => [text(key), sensitive.test(key) ? '[REDACTED]' : clean(val)]))
    return value
  }
  return clean
}

export function runLogger(outputDir, clean) {
  return message => appendFileSync(join(outputDir, 'run.log'), `${new Date().toISOString()} ${clean(message)}\n`, { mode: 0o600 })
}

function markdown(report) {
  const cell = value => String(value ?? '').replaceAll('|', '\\|').replace(/[\r\n]+/g, ' ').replaceAll('<', '&lt;')
  const lines = ['# 协议自动测试报告', '', `- 开始：${report.startedAt}`, `- 状态：${report.status}`, `- HTTP 请求：${report.requestsSent} / 上限 ${report.settings.maxRequests}`, `- 用例：${JSON.stringify(report.counts)}`, '- [执行日志](run.log)', '', '网关上游由配置声明，未读取管理接口核实实际路由。直接请求与网关请求是独立生成，不能据此精确比较两次用量。', '', '| 用例 | 状态 | HTTP | 耗时 ms | 首个问题 |', '|---|---|---|---|---|']
  for (const item of report.cases) lines.push(`| ${cell(item.id)} | ${item.status} | ${cell(item.httpStatuses?.join(', '))} | ${item.elapsedMs ?? ''} | ${cell(item.issues?.[0]?.message || item.reason || '')} |`)
  lines.push('', '## 失败详情', '')
  for (const item of report.cases.filter(c => c.status === 'failed')) {
    lines.push(`### ${item.id}`, '')
    for (const error of item.issues) lines.push(`- **${cell(error.code)}** ${cell(error.path)}：${cell(error.message)}`)
    for (const path of item.evidence || []) lines.push(`- [请求/响应证据](${path})`)
    lines.push('')
  }
  lines.push('## 检查边界', '', '本工具检查常用响应字段、基本 SSE 生命周期、文本、多轮和合成工具往返；不是供应商完整 JSON Schema 或官方 SDK 认证。不执行模型返回的任意工具，不修改网关渠道、模型组或数据库。模型列表仅检查一页。', '', `独立用例最多 ${report.settings.concurrency} 个并行，同一用例的多轮请求保持顺序，不自动重试；请求上限不约束网关内部的重试或计费。证据会脱敏配置引用的密钥、认证字段和已知签名字段，但响应正文仍可能包含其他私有信息，分享前应审阅。`, '')
  return lines.join('\n')
}

async function atomicFile(path, text) {
  await writeFile(`${path}.tmp`, text, { mode: 0o600 })
  await rename(`${path}.tmp`, path)
}

export async function exchange(url, body, headers, settings, signal, { method, rawBody, onChunk } = {}) {
  const controller = new AbortController(), started = performance.now()
  let timedOut = false, response, chunks = [], bytes = 0, storedBytes = 0, error, firstByteMs
  const abort = () => controller.abort()
  signal?.addEventListener('abort', abort, { once: true })
  if (signal?.aborted) abort()
  const timer = setTimeout(() => { timedOut = true; controller.abort() }, settings.timeoutMs)
  try {
    response = await fetch(url, { method: method || (body === undefined && rawBody === undefined ? 'GET' : 'POST'), headers, body: rawBody ?? (body === undefined ? undefined : JSON.stringify(body)), signal: controller.signal, redirect: 'error' })
    if (response.body) {
      const reader = response.body.getReader()
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        firstByteMs ??= Math.round(performance.now() - started)
        bytes += value.byteLength
        const room = settings.maxResponseBytes - storedBytes
        if (room > 0) { const chunk = Buffer.from(value.subarray(0, room)); chunks.push(chunk); storedBytes += chunk.length }
        if (bytes > settings.maxResponseBytes) { controller.abort(); throw new AuditError('response_too_large', 'Response exceeded maxResponseBytes') }
        onChunk?.(value)
      }
    }
  } catch (err) {
    error = signal?.aborted ? new AuditError('interrupted', 'Run interrupted') : timedOut ? new AuditError('timeout', `Request exceeded ${settings.timeoutMs} ms`) : err instanceof AuditError ? err : new AuditError('network_error', `${err.message}${err.cause?.code ? ` (${err.cause.code})` : ''}`)
  } finally { clearTimeout(timer); signal?.removeEventListener('abort', abort) }
  return { status: response?.status ?? null, headers: response ? Object.fromEntries(['content-type', 'x-request-id', 'request-id', 'retry-after'].map(k => [k, response.headers.get(k)]).filter(([, v]) => v !== null)) : {}, raw: Buffer.concat(chunks).toString('utf8'), bytes, firstByteMs, elapsedMs: Math.round(performance.now() - started), error }
}

export async function codeCheck(item, configDir, maxBytes, signal, env) {
  return await new Promise(resolveResult => {
    const child = spawn(item.command[0], item.command.slice(1), { cwd: resolve(configDir, item.cwd || '.'), env: { ...process.env, ...env }, shell: false, detached: process.platform !== 'win32', windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
    let chunks = [], bytes = 0, reason, finished = false, killTimer
    const kill = force => {
      if (!child.pid) return
      if (process.platform === 'win32') {
        const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true })
        killer.on('error', () => child.kill())
      } else { try { process.kill(-child.pid, force ? 'SIGKILL' : 'SIGTERM') } catch (err) { if (err.code !== 'ESRCH') child.kill() } }
    }
    const stop = why => { if (reason || finished) return; reason = why; kill(false); killTimer = setTimeout(() => kill(true), 1000) }
    const abort = () => stop('interrupted')
    signal?.addEventListener('abort', abort, { once: true })
    if (signal?.aborted) abort()
    const timer = setTimeout(() => stop('timeout'), item.timeoutMs || 600000)
    const collect = data => {
      const room = maxBytes - bytes
      if (room > 0) { const part = data.subarray(0, room); chunks.push(part); bytes += part.length }
      if (data.length > room) stop('log_too_large')
    }
    child.stdout.on('data', collect); child.stderr.on('data', collect)
    const finish = (code, error) => {
      if (finished) return
      finished = true; clearTimeout(timer); clearTimeout(killTimer); signal?.removeEventListener('abort', abort)
      resolveResult({ exitCode: code, raw: Buffer.concat(chunks).toString('utf8'), error: reason ? new AuditError(reason, `Code check stopped: ${reason}`) : error ? new AuditError('command_error', error.message) : code === 0 ? undefined : new AuditError('command_failed', `Command exited with code ${code}`) })
    }
    child.once('error', error => finish(null, error))
    child.once('close', code => finish(code))
  })
}

export async function runAudit(input, { outputDir, env = process.env, signal, onProgress = () => {} } = {}) {
  const plan = planAudit(input), config = plan.config
  const secrets = [config.gateway, ...config.targets].filter(Boolean).flatMap(target => [target.apiKey, ...[target.apiKeyEnv, ...Object.values(target.headersEnv || {})].filter(Boolean).map(name => env[name])]).filter(value => typeof value === 'string' && value.length > 0)
  for (const item of plan.cases) {
    const refs = [item.connection.apiKeyEnv, ...Object.values(item.connection.headersEnv || {})].filter(Boolean)
    for (const name of refs) {
      validate(typeof env[name] === 'string' && env[name].length > 0 && !/[\r\n]/.test(env[name]), `missing or invalid environment variable ${name}`)
    }
  }
  const clean = redactor(secrets)
  outputDir = resolve(outputDir || join(dirname(fileURLToPath(import.meta.url)), '..', 'results', `${new Date().toISOString().replaceAll(':', '-')}-${randomUUID().slice(0, 8)}`))
  await mkdir(dirname(outputDir), { recursive: true })
  await mkdir(outputDir, { mode: 0o700 })
  await mkdir(join(outputDir, 'evidence'), { mode: 0o700 })
  const log = runLogger(outputDir, clean)
  log(`RUN START cases=${plan.cases.length} budget=${config.maxRequests} concurrency=${config.concurrency}`)
  const report = { startedAt: new Date().toISOString(), runtime: { node: process.version, platform: process.platform, arch: process.arch }, status: 'running', settings: Object.fromEntries(Object.keys(defaults).map(k => [k, config[k]])), maximumPlannedRequests: plan.maximumRequests, requestsSent: 0, cases: plan.cases.map(({ connection: _, ...item }) => ({ ...item, status: 'not_run', issues: [], warnings: [], evidence: [] })) }
  const save = serialize(async () => {
    report.counts = Object.fromEntries(['passed', 'failed', 'skipped', 'not_applicable', 'running', 'not_run'].map(status => [status, report.cases.filter(c => c.status === status).length]))
    const snapshot = clean(report)
    await atomicFile(join(outputDir, 'report.json'), JSON.stringify(snapshot, null, 2) + '\n')
    await atomicFile(join(outputDir, 'report.md'), markdown(snapshot))
  })
  let reserved = 0
  const budgetWaiters = new Set()
  const release = count => {
    reserved -= count
    for (const resume of budgetWaiters) resume()
    budgetWaiters.clear()
  }
  await save()
  await mapConcurrent(plan.cases, config.concurrency, async (task, index) => {
    const item = report.cases[index], started = performance.now()
    if (task.skipReason) { item.status = 'not_applicable'; item.reason = task.skipReason; log(`${item.status} ${item.id}: ${item.reason}`); await save(); return }
    // Reserve the whole conversation so other cases cannot consume its follow-up budget.
    while (!signal?.aborted && reserved > 0 && report.requestsSent + reserved + task.maximumRequests > config.maxRequests) await new Promise(resume => budgetWaiters.add(resume))
    if (signal?.aborted || report.requestsSent + reserved + task.maximumRequests > config.maxRequests) {
      item.status = 'skipped'; item.reason = signal?.aborted ? 'interrupted' : 'request_budget_exhausted'; log(`${item.status} ${item.id}: ${item.reason}`); await save(); return
    }
    let unused = task.maximumRequests
    reserved += unused
    const caseSignal = signal ? AbortSignal.any([signal]) : undefined
    item.status = 'running'; item.startedAt = new Date().toISOString()
    const marker = `AUDIT_${randomUUID().replaceAll('-', '').slice(0, 12)}`, session = randomUUID()
    try {
      await save(); log(`START ${item.id}`)
      const target = task.connection
      const url = endpoint(target.baseUrl, task.protocol, task.model, task.stream, task.scenario === 'models')
      const headers = { 'content-type': 'application/json', accept: task.stream ? 'text/event-stream' : 'application/json', 'x-elysia-session-id': session }
      if (target.auth !== 'none') {
        const key = target.apiKey ?? env[target.apiKeyEnv]
        if (task.protocol === 'anthropic') headers['x-api-key'] = key
        else if (task.protocol === 'gemini') headers['x-goog-api-key'] = key
        else headers.authorization = `Bearer ${key}`
      }
      if (task.protocol === 'anthropic') headers['anthropic-version'] = '2023-06-01'
      for (const [name, ref] of Object.entries(target.headersEnv || {})) headers[name] = env[ref]
      let body = task.scenario === 'models' ? undefined : requestBody(task.protocol, task.model, task.stream, task.scenario, marker, config.maxOutputTokens)
      for (let round = 1; round <= task.maximumRequests; round++) {
        if (signal?.aborted) throw new AuditError('interrupted', 'Run interrupted')
        const requestNumber = ++report.requestsSent
        reserved--; unused--
        log(`HTTP START ${item.id} round=${round} ${body === undefined ? 'GET' : 'POST'} ${url}`)
        const outcome = await exchange(url, body, headers, config, caseSignal)
        const path = `evidence/request-${String(requestNumber).padStart(4, '0')}.json`
        const evidence = { caseId: item.id, round, request: { method: body === undefined ? 'GET' : 'POST', url, headers, body }, response: { status: outcome.status, headers: outcome.headers, body: outcome.raw, receivedBytes: outcome.bytes }, firstByteMs: outcome.firstByteMs, elapsedMs: outcome.elapsedMs, ...(outcome.error ? { error: issue(outcome.error) } : {}) }
        await writeFile(join(outputDir, path), JSON.stringify(clean(evidence), null, 2) + '\n', { mode: 0o600 })
        item.evidence.push(path); (item.httpStatuses ||= []).push(outcome.status); await save()
        log(`HTTP END ${item.id} round=${round} status=${outcome.status} elapsedMs=${outcome.elapsedMs} evidence=${path}${outcome.error ? ` error=${outcome.error.message}` : ''}`)
        if (outcome.error) throw outcome.error
        if (outcome.status < 200 || outcome.status >= 300) {
          let message = outcome.raw.slice(0, 3000)
          try { const parsed = JSON.parse(outcome.raw); message = parsed.error?.message || parsed.message || message; item.providerError = clean(parsed.error || parsed) } catch { /* Keep a bounded non-JSON error excerpt. */ }
          throw new AuditError('http_error', `HTTP ${outcome.status}: ${message}`)
        }
        const type = outcome.headers['content-type'] || ''
        if (task.stream && !type.toLowerCase().includes('text/event-stream')) throw new AuditError('content_type', `Expected text/event-stream, received ${type}`)
        const reply = task.scenario === 'models' ? inspectModels(task.protocol, outcome.raw, task.model) : inspectReply(task.protocol, outcome.raw, task.stream, config.requireUsage)
        item.issues.push(...reply.issues); item.warnings.push(...reply.warnings)
        if (reply.usage) (item.usage ||= []).push(reply.usage)
        if (task.scenario === 'models') { item.modelFound = reply.modelFound; item.modelCount = reply.modelCount }
        else if (round === 1 && task.scenario === 'tools') {
          if (!reply.calls.length) item.issues.push({ code: 'tool_call_missing', path: '', message: 'Model did not return the requested audit_echo call' })
          const ids = new Set()
          for (const call of reply.calls) {
            if (call.name !== 'audit_echo' || call.args?.value !== 7 || Object.keys(call.args).length !== 1) item.issues.push({ code: 'tool_call_mismatch', path: '', message: 'Only audit_echo({value:7}) is accepted; no arbitrary tool is executed' })
            if (call.id && ids.has(call.id)) item.issues.push({ code: 'duplicate_tool_id', path: '', message: 'Tool calls reused the same ID' })
            ids.add(call.id)
          }
        } else {
          const expected = round === 2 || ['system', 'history'].includes(task.scenario) ? marker : task.scenario === 'chinese' ? '测试成功' : task.scenario === 'image' ? 'red' : 'OK'
          const matched = expected === 'OK' ? /\bOK\b/.test(reply.text) : expected === 'red' ? /\bred\b/i.test(reply.text) : reply.text.includes(expected)
          if (!matched) item.issues.push({ code: 'unexpected_text', path: '', message: `Response did not contain expected ${expected === marker ? 'memory/system/tool-result marker' : expected}` })
        }
        if (item.issues.length) { item.unexecutedRounds = task.maximumRequests - round; break }
        if (round < task.maximumRequests) body = followupBody(task.protocol, body, reply, task.scenario, marker)
      }
    } catch (error) { item.issues.push(issue(error)) }
    finally { release(unused) }
    item.elapsedMs = Math.round(performance.now() - started); item.finishedAt = new Date().toISOString()
    item.status = item.issues.length ? 'failed' : 'passed'
    log(`END ${item.id} ${item.status} elapsedMs=${item.elapsedMs}${item.issues.length ? ` issues=${JSON.stringify(item.issues)}` : ''}`)
    await save(); onProgress(clean({ id: item.id, status: item.status, issues: item.issues }))
  })
  report.status = signal?.aborted ? 'interrupted' : report.cases.some(c => c.status === 'failed') ? 'failed' : report.cases.some(c => !['passed', 'not_applicable'].includes(c.status)) ? 'incomplete' : 'passed'
  report.finishedAt = new Date().toISOString(); await save()
  log(`RUN END ${report.status} counts=${JSON.stringify(report.counts)}`)
  return clean(report)
}
