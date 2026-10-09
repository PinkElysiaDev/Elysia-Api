import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { runAudit, planAudit, redactor, codeCheck, defaults } from '../src/audit.mjs'
import { protocols } from '../src/protocols.mjs'
import { exampleConfig, fixtureSignature, replyWire } from './fixtures.mjs'

async function fixture(t, handler) {
  const server = createServer((req, res) => Promise.resolve().then(() => handler(req, res)).catch(error => { res.writeHead(500); res.end(error.stack) }))
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const directory = await mkdtemp(join(tmpdir(), 'protocol-audit-'))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); await rm(directory, { recursive: true, force: true }) })
  return { baseUrl: `http://127.0.0.1:${server.address().port}`, outputDir: join(directory, 'result'), directory }
}

const settings = baseUrl => ({ scenarios: ['text'], streams: [false], targets: [{ id: 'chat', protocol: 'chat', baseUrl, model: 'test', apiKeyEnv: 'TEST_KEY' }] })

test('HTTP failures are recorded, secrets are redacted, and later cases still run', async t => {
  let count = 0
  const secret = 'synthetic-key-never-save-this'
  const f = await fixture(t, async (req, res) => {
    assert.equal(req.headers.authorization, `Bearer ${secret}`)
    count++
    res.setHeader('Content-Type', 'application/json')
    if (count === 1) { res.writeHead(502); res.end(JSON.stringify({ error: { code: 'protocol_gateway_error', message: `unsupported_capability at /wire:openai_chat; ${secret}`, api_key: 'unconfigured-provider-secret' } })); return }
    res.end(JSON.stringify({ id: 'c1', object: 'chat.completion', created: 1, model: 'test', choices: [{ index: 0, message: { role: 'assistant', content: 'OK' }, finish_reason: 'stop' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }))
  })
  const config = settings(f.baseUrl)
  config.concurrency = 1
  config.targets.push({ ...config.targets[0], id: 'second' })
  const report = await runAudit(config, { ...f, env: { TEST_KEY: secret } })
  assert.deepEqual(report.cases.map(c => c.status), ['failed', 'passed'])
  assert.equal(report.requestsSent, 2)
  assert.equal(report.cases[0].issues[0].code, 'http_error')
  assert.match(report.cases[0].issues[0].message, /unsupported_capability/)
  for (const name of ['report.json', 'report.md', ...((await readdir(join(f.outputDir, 'evidence'))).map(n => `evidence/${n}`))]) {
    const text = await readFile(join(f.outputDir, name), 'utf8')
    assert.ok(!text.includes(secret), name)
    assert.ok(!text.includes('unconfigured-provider-secret'), name)
  }
})

test('dry plan expands four gateway inputs without contacting or modifying the gateway', () => {
  const config = settings('http://127.0.0.1:1')
  config.gateway = { baseUrl: 'http://127.0.0.1:2', apiKeyEnv: 'GATEWAY_KEY', routes: [{ id: 'to-chat', model: 'chat-group', upstreamTarget: 'chat' }] }
  const plan = planAudit(config)
  assert.equal(plan.cases.length, 5)
  assert.equal(plan.maximumRequests, 5)
  assert.deepEqual(plan.cases.filter(c => c.kind === 'gateway').map(c => c.protocol), ['chat', 'responses', 'anthropic', 'gemini'])
})

test('default and larger configured output budgets reach all four HTTP protocols', async t => {
  let expected = defaults.maxOutputTokens, received = 0
  const f = await fixture(t, async (req, res) => {
    const protocol = req.url.split('/')[1]
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks).toString())
    assert.equal(body.max_completion_tokens ?? body.max_output_tokens ?? body.max_tokens ?? body.generationConfig?.maxOutputTokens, expected)
    received++; res.end(replyWire(protocol))
  })
  const config = { scenarios: ['text'], streams: [false], targets: protocols.map(protocol => ({ id: protocol, protocol, baseUrl: `${f.baseUrl}/${protocol}`, model: 'test', auth: 'none' })) }
  assert.equal(expected, 32768)
  const normal = await runAudit(config, f)
  assert.equal(normal.status, 'passed'); assert.equal(normal.settings.timeoutMs, 180000)
  expected = 128000
  const larger = await runAudit({ ...config, maxOutputTokens: expected }, { outputDir: join(f.directory, 'larger') })
  assert.equal(larger.status, 'passed'); assert.equal(received, 8)
})

test('inline credentials authenticate every protocol and are absent from reports, logs and evidence', async t => {
  const secret = 'synthetic-inline-key-private'
  const f = await fixture(t, (req, res) => {
    const protocol = req.url.split('/')[1]
    assert.equal(protocol === 'anthropic' ? req.headers['x-api-key'] : protocol === 'gemini' ? req.headers['x-goog-api-key'] : req.headers.authorization?.replace('Bearer ', ''), secret)
    if (protocol === 'chat') { res.writeHead(503); res.end(JSON.stringify({ error: { message: `upstream failed: ${secret}` } })) }
    else res.end(replyWire(protocol))
  })
  const targets = protocols.map(protocol => ({ id: protocol, protocol, baseUrl: `${f.baseUrl}/${protocol}`, model: 'test', apiKey: secret }))
  const report = await runAudit({ scenarios: ['text'], streams: [false], targets }, { ...f, env: {} })
  assert.equal(report.counts.failed, 1); assert.equal(report.counts.passed, 3)
  for (const path of ['report.json', 'report.md', 'run.log', ...((await readdir(join(f.outputDir, 'evidence'))).map(n => `evidence/${n}`))]) assert.ok(!(await readFile(join(f.outputDir, path), 'utf8')).includes(secret), path)
  const log = await readFile(join(f.outputDir, 'run.log'), 'utf8')
  for (const pattern of [/RUN START/, /HTTP START.*chat/, /status=503/, /HTTP END.*evidence=/, /upstream failed: \[REDACTED\]/, /RUN END failed/]) assert.match(log, pattern)
  for (const auth of [{ apiKey: '' }, { apiKey: 'bad\nkey' }, { apiKey: secret, apiKeyEnv: 'TEST_KEY' }, { apiKey: secret, auth: 'none' }]) assert.throws(() => planAudit({ targets: [{ ...targets[0], ...auth }] }), /Configuration/)
})

test('Chat and Responses use independent credentials, endpoints, models and outcomes; either can run alone', async t => {
  const example = exampleConfig()
  const targets = example.targets.filter(target => ['chat', 'responses'].includes(target.protocol)).map(({ apiKey, ...target }) => ({ ...target, id: target.protocol, apiKeyEnv: `AUDIT_${target.protocol.toUpperCase()}_KEY` }))
  assert.equal(targets.length, 2)
  for (const field of ['baseUrl', 'model', 'apiKeyEnv']) assert.notEqual(targets[0][field], targets[1][field], field)
  const received = []
  const f = await fixture(t, async (req, res) => {
    received.push(req.url)
    const target = targets.find(target => req.url.startsWith(`/${target.protocol}/`))
    assert.ok(target, 'unexpected protocol endpoint or fallback')
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks).toString())
    assert.equal(body.model, target.model)
    assert.equal(req.headers.authorization, `Bearer synthetic-${target.protocol}-key`)
    if (target.protocol === 'chat') {
      assert.equal(req.url, '/chat/v1/chat/completions')
      assert.ok(Array.isArray(body.messages)); assert.equal(body.input, undefined)
      res.writeHead(502); res.end(JSON.stringify({ error: { message: 'Chat unavailable' } }))
    } else {
      assert.equal(req.url, '/responses/v1/responses')
      assert.ok(Array.isArray(body.input)); assert.equal(body.messages, undefined)
      res.end(replyWire('responses'))
    }
  })
  const config = { scenarios: ['text'], streams: [false], targets: targets.map(target => ({ ...target, baseUrl: `${f.baseUrl}/${target.protocol}/v1` })) }
  const env = Object.fromEntries(targets.map(target => [target.apiKeyEnv, `synthetic-${target.protocol}-key`]))
  const report = await runAudit(config, { ...f, env })
  assert.deepEqual(report.cases.map(c => [c.protocol, c.status]), [['chat', 'failed'], ['responses', 'passed']])
  assert.equal(report.requestsSent, 2)
  for (const target of config.targets) {
    const single = await runAudit({ ...config, targets: [target] }, { outputDir: join(f.directory, target.protocol), env: { [target.apiKeyEnv]: env[target.apiKeyEnv] } })
    assert.equal(single.requestsSent, 1)
    assert.equal(single.cases.length, 1)
    assert.equal(single.cases[0].protocol, target.protocol)
  }
  assert.deepEqual(received, ['/chat/v1/chat/completions', '/responses/v1/responses', '/chat/v1/chat/completions', '/responses/v1/responses'])
})

test('all four protocols and the 4×4 gateway matrix complete JSON/SSE multi-turn and tool cases over HTTP', async t => {
  let received = 0
  const sessions = new Map()
  const f = await fixture(t, async (req, res) => {
    received++
    const protocol = req.url.includes('/responses') ? 'responses' : req.url.includes('/messages') || req.headers['anthropic-version'] ? 'anthropic' : req.url.includes('/v1beta/') ? 'gemini' : 'chat'
    const key = protocol === 'gemini' ? req.headers['x-goog-api-key'] : protocol === 'anthropic' ? req.headers['x-api-key'] : req.headers.authorization?.replace('Bearer ', '')
    assert.equal(key, 'synthetic-key')
    if (protocol === 'anthropic') assert.equal(req.headers['anthropic-version'], '2023-06-01')
    if (req.method === 'GET') { res.end(JSON.stringify(protocol === 'gemini' ? { models: [{ name: 'models/test' }] } : { data: [{ id: 'test' }] })); return }
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks).toString())
    const history = body.input || body.contents || body.messages
    const session = req.headers['x-elysia-session-id'], first = sessions.get(session)
    const text = JSON.stringify(body), marker = text.match(/AUDIT_[a-z0-9]{12}/)?.[0]
    const tool = !!body.tools && !first
    const stream = body.stream || req.url.includes(':streamGenerateContent')
    if (protocol === 'chat' && stream) assert.equal(body.stream_options.include_usage, true)
    if (protocol === 'responses') assert.deepEqual(body.include, ['reasoning.encrypted_content'])
    if (first) {
      assert.deepEqual(history[0], first.history[0])
      assert.ok(history.length >= 3)
      assert.ok(marker)
      if (first.tool) {
        assert.ok(text.includes(fixtureSignature), 'continuation signature lost')
        assert.ok(text.includes('call_1'), 'tool call ID lost')
        if (protocol === 'gemini') assert.deepEqual(history.at(-1).parts[0].functionResponse.response, { value: 7, marker })
      } else assert.equal(marker, first.marker)
    } else {
      assert.equal(history.length, 1)
      if (tool) assert.equal(marker, undefined, 'result marker must only be learned from the tool result')
      sessions.set(session, { history, marker, tool })
    }
    res.setHeader('content-type', stream ? 'text/event-stream' : 'application/json')
    const wire = Buffer.from(replyWire(protocol, first ? marker : 'OK 测试', tool, stream))
    // Split inside a UTF-8 character, independent of event boundaries.
    const split = wire.indexOf(Buffer.from('测')) + 1
    res.write(wire.subarray(0, split)); setImmediate(() => res.end(wire.subarray(split)))
  })
  const config = { targets: protocols.map(protocol => ({ id: protocol, protocol, baseUrl: f.baseUrl, model: 'test', apiKeyEnv: 'TEST_KEY' })), gateway: { baseUrl: f.baseUrl, apiKeyEnv: 'TEST_KEY', routes: protocols.map(protocol => ({ id: `to-${protocol}`, model: 'test', upstreamTarget: protocol })) } }
  const report = await runAudit(config, { ...f, env: { TEST_KEY: 'synthetic-key' } })
  assert.equal(report.status, 'passed', JSON.stringify(report.cases.filter(c => c.status === 'failed')))
  assert.equal(report.counts.passed, 140)
  assert.equal(report.requestsSent, 220)
  assert.equal(received, 220)
  const paths = await readdir(join(f.outputDir, 'evidence'))
  assert.equal(paths.length, received)
  for (const item of report.cases) for (const path of item.evidence) assert.equal(JSON.parse(await readFile(join(f.outputDir, path))).caseId, item.id)
  for (const path of paths) assert.ok(!(await readFile(join(f.outputDir, 'evidence', path), 'utf8')).includes(fixtureSignature), path)
})

test('HTTP 200 schema and in-stream errors are reported with evidence; failed first round is not continued', async t => {
  const f = await fixture(t, (req, res) => {
    if (req.url.includes('/responses')) {
      const body = JSON.parse(replyWire('responses')); delete body.output[0].id
      res.end(JSON.stringify(body))
    } else {
      res.setHeader('content-type', 'text/event-stream')
      res.end('event: error\ndata: {"type":"error","error":{"message":"verification_mismatch at /roundtrip/content/2/payload"}}\n\n')
    }
  })
  const config = settings(f.baseUrl)
  config.targets[0].protocol = 'responses'
  const schema = await runAudit(config, { ...f, env: { TEST_KEY: 'test' } })
  assert.equal(schema.cases[0].issues[0].path, '/output/0/id')
  assert.deepEqual(schema.cases[0].httpStatuses, [200])
  config.targets[0].protocol = 'anthropic'; config.streams = [true]; config.scenarios = ['tools']
  const stream = await runAudit(config, { outputDir: join(f.directory, 'stream'), env: { TEST_KEY: 'test' } })
  assert.equal(stream.requestsSent, 1)
  assert.equal(stream.cases[0].unexecutedRounds, 1)
  assert.match(stream.cases[0].issues[0].message, /verification_mismatch/)
  assert.equal(stream.cases[0].issues[0].code, 'stream_error')
  assert.equal(stream.cases[0].evidence.length, 1)
})

test('protocol instruction checks still reject replies that do not contain the requested OK', async t => {
  const f = await fixture(t, (_req, res) => res.end(replyWire('chat', '好的')))
  const report = await runAudit(settings(f.baseUrl), { ...f, env: { TEST_KEY: 'synthetic' } })
  assert.equal(report.counts.failed, 1)
  assert.ok(report.cases[0].issues.some(issue => issue.code === 'unexpected_text'))
})

test('timeout, body limit and redirects fail safely, retain evidence, and do not retry', async t => {
  let received = 0
  const f = await fixture(t, (req, res) => {
    received++
    if (req.url.startsWith('/timeout')) { res.writeHead(200); res.write('partial-before-timeout'); return }
    if (req.url.startsWith('/large')) { res.end('x'.repeat(1024)); return }
    res.writeHead(302, { location: `${f.baseUrl}/unexpected` }); res.end()
  })
  const config = settings(f.baseUrl)
  config.timeoutMs = 100; config.maxResponseBytes = 128
  config.targets = ['timeout', 'large', 'redirect'].map(id => ({ ...config.targets[0], id, baseUrl: `${f.baseUrl}/${id}` }))
  const report = await runAudit(config, { ...f, env: { TEST_KEY: 'secret' } })
  assert.deepEqual(report.cases.map(c => c.issues[0].code), ['timeout', 'response_too_large', 'network_error'])
  assert.equal(received, 3)
  const timeout = JSON.parse(await readFile(join(f.outputDir, report.cases[0].evidence[0])))
  assert.equal(timeout.response.body, 'partial-before-timeout')
  const large = JSON.parse(await readFile(join(f.outputDir, report.cases[1].evidence[0])))
  assert.equal(large.response.body.length, 128)
})

test('budget and missing credentials are enforced before network requests', async t => {
  let received = 0
  const f = await fixture(t, (req, res) => { received++; res.end(replyWire('chat')) })
  const config = settings(f.baseUrl)
  await assert.rejects(runAudit(config, { ...f, env: {} }), /TEST_KEY/)
  assert.deepEqual(await readdir(f.directory), [])
  config.maxRequests = 1; config.scenarios = ['tools']
  const report = await runAudit(config, { ...f, env: { TEST_KEY: 'test' } })
  assert.equal(report.status, 'incomplete')
  assert.equal(report.cases[0].reason, 'request_budget_exhausted')
  assert.equal(received, 0)
  await assert.rejects(runAudit(config, { ...f, env: { TEST_KEY: 'test' } }), { code: 'EEXIST' })
})

test('cancellation saves partial results and skips remaining cases', async t => {
  const controller = new AbortController()
  let received = 0
  const f = await fixture(t, (req, res) => { received++; res.write('partial'); setImmediate(() => controller.abort()) })
  const config = settings(f.baseUrl)
  config.concurrency = 1
  config.targets.push({ ...config.targets[0], id: 'later' })
  const report = await runAudit(config, { ...f, env: { TEST_KEY: 'test' }, signal: controller.signal })
  assert.equal(report.status, 'interrupted')
  assert.deepEqual(report.cases.map(c => c.status), ['failed', 'skipped'])
  assert.equal(report.cases[0].issues[0].code, 'interrupted')
  assert.equal(received, 1)
  assert.equal(JSON.parse(await readFile(join(f.outputDir, 'report.json'))).status, 'interrupted')
})

test('code checks capture UTF-8 stdout/stderr, nonzero exits, timeout, overflow and spawn errors', async t => {
  const f = await fixture(t, () => assert.fail('code-only must not make HTTP calls'))
  const commands = [
    { id: 'fail', command: [process.execPath, '-e', 'process.stdout.write(Buffer.from("测试").subarray(0,1)); setTimeout(() => { process.stdout.write(Buffer.from("测试").subarray(1)); console.error(process.env.TEST_KEY); process.exitCode=3 }, 10)'] },
    { id: 'timeout', command: [process.execPath, '-e', 'setInterval(() => {}, 1000)'], timeoutMs: 80 },
    { id: 'large', command: [process.execPath, '-e', 'console.log("x".repeat(1024)); setInterval(() => {}, 1000)'] },
    { id: 'missing', command: ['protocol-audit-nonexistent-command'] },
    { id: 'pass', command: [process.execPath, '-e', 'console.log("checks passed")'] },
  ]
  const results = []
  for (const command of commands) results.push(await codeCheck(command, f.directory, 128, undefined, { TEST_KEY: 'synthetic-code-key' }))
  assert.deepEqual(results.map(r => r.error?.code), ['command_failed', 'timeout', 'log_too_large', 'command_error', undefined])
  assert.equal(results[0].exitCode, 3)
  const log = redactor(['synthetic-code-key'])(results[0].raw)
  assert.ok(log.includes('测试'))
  assert.ok(!log.includes('synthetic-code-key'))
})

test('redaction covers JSON, nested JSON strings, SSE, quoted secrets and bearer text', () => {
  const secret = 'key-with-"quote-and-\\slash'
  const clean = redactor([secret])
  const value = { raw: JSON.stringify({ nested: JSON.stringify({ api_key: 'unconfigured' }), password: secret }), sse: `data: ${JSON.stringify({ signature: 'opaque', content: secret })}\n\n`, log: 'Authorization: Bearer something-private' }
  const text = JSON.stringify(clean(value))
  for (const denied of ['unconfigured', 'opaque', 'something-private', 'quote-and']) assert.ok(!text.includes(denied), denied)
})

test('invalid configuration is rejected instead of silently changing test scope', () => {
  for (const patch of [{ unknown: true }, { scenarios: ['typo'] }, { streams: [false, false] }, { maxRequests: -1 }, { retry: 3 }]) assert.throws(() => planAudit({ ...settings('http://localhost'), ...patch }), /Configuration/)
  for (const baseUrl of ['file:///tmp/test', 'https://user:password@example.com', 'https://example.com?key=secret']) assert.throws(() => planAudit(settings(baseUrl)), /Configuration/)
})
