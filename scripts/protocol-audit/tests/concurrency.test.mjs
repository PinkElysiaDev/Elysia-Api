import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { mapConcurrent, serialize } from '../src/concurrency.mjs'
import { runAudit, planAudit } from '../src/audit.mjs'
import { planSuite } from '../src/suite.mjs'
import { replyWire } from './fixtures.mjs'

async function server(t, handler) {
  const http = createServer((req, res) => Promise.resolve(handler(req, res)).catch(error => { res.writeHead(500); res.end(error.stack) }))
  await new Promise(resolve => http.listen(0, '127.0.0.1', resolve))
  const root = await mkdtemp(join(tmpdir(), 'audit-concurrency-'))
  t.after(async () => { http.closeAllConnections(); await new Promise(resolve => http.close(resolve)); await rm(root, { recursive: true, force: true }) })
  return { baseUrl: `http://127.0.0.1:${http.address().port}`, outputDir: join(root, 'result') }
}

const target = (baseUrl, i = 0) => ({ id: `case-${i}`, protocol: 'chat', baseUrl, model: `model-${i}`, auth: 'none' })

test('concurrency defaults to 32, accepts large integers, and rejects invalid values', () => {
  const input = { targets: [target('http://127.0.0.1:1')] }
  assert.equal(planAudit(input).config.concurrency, 32)
  for (const concurrency of [1, 32, 1000000]) {
    assert.equal(planAudit({ ...input, concurrency }).config.concurrency, concurrency)
    assert.equal(planSuite({ ...input, concurrency }, 'code').api.concurrency, concurrency)
  }
  for (const concurrency of [0, -1, 1.5, '32', null, Infinity, NaN]) assert.throws(() => planAudit({ ...input, concurrency }), /concurrency/)
})

for (const concurrency of [undefined, 1, 4, 1000000]) test(`HTTP concurrency ${concurrency ?? 'default'} bounds in-flight requests and keeps every evidence file`, { timeout: 10000 }, async t => {
  const count = 40, width = Math.min(count, concurrency ?? 32), pending = []
  let received = 0, peak = 0
  const f = await server(t, async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks))
    received++; pending.push({ res, model: body.model }); peak = Math.max(peak, pending.length)
    if (pending.length === width || received === count) for (const { res, model } of pending.splice(0)) res.end(replyWire('chat', `OK ${model}`))
  })
  const config = { scenarios: ['text'], streams: [false], targets: Array.from({ length: count }, (_, i) => target(f.baseUrl, i)), ...(concurrency === undefined ? {} : { concurrency }) }
  const report = await runAudit(config, f)
  assert.equal(report.status, 'passed'); assert.equal(peak, width); assert.equal(received, count)
  assert.equal((await readdir(join(f.outputDir, 'evidence'))).length, count)
  for (const item of report.cases) {
    const detail = JSON.parse(await readFile(join(f.outputDir, item.evidence[0])))
    assert.equal(detail.caseId, item.id)
    assert.equal(detail.request.body.model, item.model)
    assert.ok(detail.response.body.includes(item.model))
  }
  assert.deepEqual(JSON.parse(await readFile(join(f.outputDir, 'report.json'))), report)
})

test('concurrent cases reserve follow-up requests and reuse reservations released by failure', async t => {
  let received = 0
  const f = await server(t, async (req, res) => { received++; await delay(20); res.writeHead(503); res.end('intentional failure') })
  const report = await runAudit({ concurrency: 32, maxRequests: 5, scenarios: ['tools', 'text'], streams: [false], targets: Array.from({ length: 8 }, (_, i) => target(f.baseUrl, i)) }, f)
  assert.equal(received, 5); assert.equal(report.requestsSent, 5)
  assert.equal(report.counts.failed, 5); assert.ok(report.counts.skipped > 0)
  assert.ok(report.cases.filter(c => c.scenario === 'tools').every(c => c.evidence.length <= 1))
})

test('cancellation aborts all active HTTP work, wakes budget waiters and skips queued cases', async t => {
  const controller = new AbortController()
  let received = 0
  const f = await server(t, (req, res) => {
    req.resume(); received++; res.write('partial response')
    if (received === 2) setTimeout(() => controller.abort(), 20)
  })
  const report = await runAudit({ concurrency: 4, maxRequests: 4, scenarios: ['tools'], streams: [false], targets: Array.from({ length: 12 }, (_, i) => target(f.baseUrl, i)) }, { ...f, signal: controller.signal })
  assert.equal(report.status, 'interrupted'); assert.equal(received, 2)
  assert.equal(report.counts.failed, 2); assert.equal(report.counts.skipped, 10)
  for (const item of report.cases.filter(c => c.status === 'failed')) {
    assert.equal(item.issues[0].code, 'interrupted')
    assert.equal(JSON.parse(await readFile(join(f.outputDir, item.evidence[0]))).response.body, 'partial response')
  }
})

test('workers drain before failure returns, and serialized writes recover after failure', async () => {
  const finished = []
  await assert.rejects(mapConcurrent([0, 1, 2], 2, async n => {
    if (n === 0) throw new Error('intentional')
    await delay(10); finished.push(n)
  }), /intentional/)
  assert.deepEqual(finished, [1, 2])
  const saved = []
  const save = serialize(async n => { await delay(2); saved.push(n); if (n === 1) throw new Error('write failed') })
  const results = await Promise.allSettled([save(0), save(1), save(2)])
  assert.deepEqual(saved, [0, 1, 2]); assert.deepEqual(results.map(r => r.status), ['fulfilled', 'rejected', 'fulfilled'])
})
