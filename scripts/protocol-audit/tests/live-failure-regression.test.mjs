import './network-guard.mjs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { failureAnalysis, failureCategory, providerFailures } from '../src/evidence.mjs'
import { consume } from '../src/sdk.mjs'
import { codeCheck, runAudit } from '../src/audit.mjs'
import { runSuite } from '../src/suite.mjs'
import { inspectReply } from '../src/protocols.mjs'
import { replyWire } from './fixtures.mjs'

const limit = { code: 'gateway_concurrency_limit', message: 'Synthetic limit' }
const errorWire = `data: ${JSON.stringify({ type: '***.failed', response: { status: 'failed', error: limit } })}\n\n`

async function directory(t) {
  const path = await mkdtemp(join(tmpdir(), 'audit-failure-'))
  t.after(() => rm(path, { recursive: true, force: true }))
  return path
}
async function serve(t, handler) {
  const server = createServer(handler)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  return `http://127.0.0.1:${server.address().port}`
}

test('HTTP 200 nested provider limits retain secondary conversion failures and separate status meanings', () => {
  const record = { statusCode: 429, providerFailures: providerFailures(errorWire), conversionIssues: [{ code: 'upstream_contract_violation', severity: 'error', path: '/type' }, { code: 'unsupported_capability', severity: 'error' }] }
  const input = { kind: 'gateway', httpStatuses: [200], record }
  assert.equal(failureCategory(input), 'rate_limited')
  const result = failureAnalysis(input)
  assert.equal(result.primary.code, limit.code)
  assert.equal(result.secondaryIssues.length, 2)
  assert.deepEqual(result.clientHttpStatuses, [200])
  assert.deepEqual(result.upstreamHttpStatuses, [])
  assert.equal(result.logicalStatus, 429)
  assert.deepEqual(failureAnalysis({ ...input, record: { ...record, upstreamHttpStatus: 200 } }).upstreamHttpStatuses, [200])
  assert.deepEqual(providerFailures('{"output_text":"rate_limit_error"}'), [])
  assert.deepEqual(providerFailures('{"error":{"details":{"private":"secret"}}}'), [])
})

test('a failed official SDK does not prevent Vercel from executing or hide its status', async t => {
  let calls = 0
  const baseUrl = await serve(t, (req, res) => {
    req.resume()
    if (++calls === 1) { res.writeHead(429, { 'content-type': 'application/json' }); res.end(JSON.stringify({ error: limit })); return }
    res.writeHead(200, { 'content-type': 'application/json' }); res.end(replyWire('chat', 'OK', false, false))
  })
  await assert.rejects(consume('chat', { baseUrl, apiKey: 'synthetic', model: 'm' }, false), error => {
    assert.deepEqual(error.sdk.map(s => [s.name, s.status]), [['openai', 'failed'], ['@ai-sdk/openai', 'passed']])
    return true
  })
  assert.equal(calls, 2)
})

test('failed generation stops tool and text assertions without hiding protocol diagnostics', async t => {
  const root = await directory(t)
  const baseUrl = await serve(t, (req, res) => { req.resume(); res.writeHead(200, { 'content-type': 'text/event-stream' }); res.end(errorWire) })
  const report = await runAudit({ targets: [{ id: 'test', protocol: 'responses', baseUrl, model: 'm', auth: 'none' }], streams: [true], scenarios: ['text', 'tools'], concurrency: 1 }, { outputDir: join(root, 'result') })
  assert.equal(report.status, 'failed')
  for (const item of report.cases) {
    assert.equal(item.failureCategory, 'rate_limited')
    assert.ok(item.issues.some(i => i.code === 'stream_error'))
    assert.ok(item.issues.every(i => !['unexpected_text', 'tool_call_missing'].includes(i.code)))
  }
})

test('Responses distinguishes missing initial fields from changing established fields', () => {
  for (const [alter, code] of [[r => { delete r.created_at; delete r.model }, 'stream_missing_initial_field'], [r => { r.created_at++ }, 'stream_identity_changed']]) {
    const raw = replyWire('responses', 'OK', false, true).replace(/data: (.+)/g, (line, data) => {
      const event = JSON.parse(data)
      if (event.type !== 'response.created') return line
      alter(event.response); return `data: ${JSON.stringify(event)}`
    })
    assert.ok(inspectReply('responses', raw, true).issues.some(i => i.code === code))
  }
})

test('Go package progress is emitted before completion and ignores test output', async () => {
  const progress = []
  const events = [{ Action: 'start', Package: 'example/p' }, { Action: 'run', Package: 'example/p', Test: 'TestPrivate' }, { Action: 'pass', Package: 'example/p' }]
  const result = await codeCheck({ command: [process.execPath, '-e', `console.log(${JSON.stringify(events.map(e => JSON.stringify(e)).join('\n'))})`] }, '.', 10000, undefined, undefined, event => progress.push(event))
  assert.equal(result.exitCode, 0)
  assert.deepEqual(progress.map(p => p.action), ['start', 'pass'])
})

test('code-check timeout records incomplete acceptance and keeps later checks running', async t => {
  const root = await directory(t)
  await mkdir(join(root, 'backend')); await writeFile(join(root, 'backend/go.mod'), 'module example.invalid/test\ngo 1.25\n')
  const report = await runSuite({ projectRoot: root, codeChecks: [
    { id: 'slow', command: [process.execPath, '-e', 'setInterval(() => {}, 1000)'], timeoutMs: 100 },
    { id: 'later', command: [process.execPath, '-e', 'process.exit(0)'] },
  ] }, { group: 'code', outputDir: join(root, 'result') })
  assert.equal(report.status, 'incomplete')
  assert.deepEqual(report.cases.map(c => c.status), ['incomplete', 'passed'])
  assert.equal(report.counts.incomplete, 1)
})
