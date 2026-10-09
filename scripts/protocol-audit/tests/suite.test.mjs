import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, readFile, readdir, writeFile, copyFile, cp, realpath, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { spawn } from 'node:child_process'
import { inflateSync } from 'node:zlib'
import { checkReply, groups, planSuite, runSuite } from '../src/suite.mjs'
import { protocols, requestBody } from '../src/protocols.mjs'
import { runAudit, defaults } from '../src/audit.mjs'
import { exampleConfig, mockUpstream, replyWire } from './fixtures.mjs'

const configuredTargets = () => protocols.map(protocol => ({ protocol, baseUrl: 'http://127.0.0.1:1', apiKey: 'synthetic-inline', model: 'test' }))

async function directory(t) {
  const root = await mkdtemp(join(tmpdir(), 'audit-suite-test-'))
  await mkdir(join(root, 'backend')); await writeFile(join(root, 'backend/go.mod'), 'module example.invalid/test\ngo 1.25\n')
  t.after(() => rm(root, { recursive: true, force: true }))
  return root
}

async function copyPackage(root) {
  await copyFile(new URL('../run.mjs', import.meta.url), join(root, 'run.mjs'))
  await writeFile(join(root, 'config.example.json'), JSON.stringify(exampleConfig()))
  await cp(new URL('../src/', import.meta.url), join(root, 'src'), { recursive: true })
  // The standalone package now uses a source-span JSON parser for redaction.
  // Copy its installed, pinned dependency without fetching during offline tests.
  await cp(new URL('../node_modules/jsonc-parser/', import.meta.url), join(root, 'node_modules/jsonc-parser'), { recursive: true })
}

test('default selects every group with four independently configured protocols', () => {
  const config = { targets: configuredTargets() }
  assert.deepEqual(planSuite(config).groups, groups)
  assert.equal(planSuite(config).api.targets[1].id, 'responses-2')
  for (const group of groups) assert.deepEqual(planSuite(config, group).groups, [group])
  assert.throws(() => planSuite({}, 'typo'), /Unknown group/)
  for (const config of [{ typo: true }, { targets: {} }, { maxRequests: -1 }]) assert.throws(() => planSuite(config))
})

test('code group runs independently of channel credentials and never enables inherited paid tests', async t => {
  const root = await directory(t)
  const report = await runSuite({ projectRoot: root, targets: [{ id: 'unused', protocol: 'chat', model: 'test', baseUrl: 'http://127.0.0.1:1', apiKeyEnv: 'ABSENT_KEY' }], codeChecks: [
    { id: 'env', command: [process.execPath, '-e', 'if (process.env.ELYSIA_LIVE_TESTS || process.env.UPDATE_AGENT_CLI_DOCS) process.exit(1)'] },
    { id: 'fail', command: [process.execPath, '-e', 'console.error("intentional failure"); process.exit(7)'] },
    { id: 'later', command: [process.execPath, '-e', 'console.log("later test executed")'] },
  ] }, { group: 'code', outputDir: join(root, 'result'), env: { ELYSIA_LIVE_TESTS: '1', UPDATE_AGENT_CLI_DOCS: '1' } })
  assert.equal(report.status, 'failed')
  assert.deepEqual(report.cases.map(c => [c.id, c.status]), [['code/env', 'passed'], ['code/fail', 'failed'], ['code/later', 'passed']])
  assert.equal(report.cases[1].exitCode, 7)
  assert.match(await readFile(join(root, 'result', report.cases[1].evidence[0]), 'utf8'), /intentional failure/)
  const log = await readFile(join(root, 'result/run.log'), 'utf8')
  for (const pattern of [/RUN START/, /START code\/fail/, /COMMAND END code\/fail exit=7/, /END code\/fail failed/, /code\/later/, /RUN END failed/]) assert.match(log, pattern)
})

test('missing setup blocks dependent daily cases and preserves a readable report', async t => {
  const root = await directory(t)
  const report = await runSuite({ projectRoot: join(root, 'missing'), targets: configuredTargets() }, { group: 'daily', outputDir: join(root, 'result') })
  assert.equal(report.status, 'incomplete')
  assert.equal(report.cases[0].status, 'blocked')
  assert.ok(report.cases.slice(1).every(c => c.status === 'blocked'))
  assert.match(await readFile(join(root, 'result/report.md'), 'utf8'), /setup\/isolated-backend/)
})

test('empty required code checks and interruption cannot be reported as passed', async t => {
  const root = await directory(t)
  const config = { projectRoot: root, codeChecks: [] }
  const result = await runSuite(config, { group: 'code', outputDir: join(root, 'empty') })
  assert.equal(result.status, 'incomplete')
  const interrupted = await runSuite(config, { group: 'code', outputDir: join(root, 'interrupted'), signal: AbortSignal.abort() })
  assert.equal(interrupted.status, 'interrupted')
})

test('system instructions, Chinese, longer history and image transport work for every protocol', async t => {
  const root = await directory(t), upstream = await mockUpstream()
  t.after(() => upstream.close())
  const targets = protocols.map(protocol => ({ id: protocol, protocol, baseUrl: `${upstream.baseUrl}/${protocol}/normal`, model: `mock-${protocol}`, auth: 'none', vision: true }))
  const report = await runAudit({ scenarios: ['system', 'chinese', 'history', 'image'], targets }, { outputDir: join(root, 'result') })
  assert.equal(report.status, 'passed', JSON.stringify(report.cases.filter(c => c.status === 'failed')))
  assert.equal(report.counts.passed, 32)
  const image = requestBody('gemini', 'test', false, 'image', '', defaults.maxOutputTokens).contents[0].parts[1].inlineData.data
  const png = Buffer.from(image, 'base64'); let offset = 8, pixels
  while (offset < png.length) { const size = png.readUInt32BE(offset); if (png.toString('ascii', offset + 4, offset + 8) === 'IDAT') pixels = inflateSync(png.subarray(offset + 8, offset + 8 + size)); offset += size + 12 }
  assert.ok(pixels); assert.deepEqual([...pixels.subarray(0, 4)], [0, 255, 0, 0])
  const omitted = await runAudit({ scenarios: ['image'], targets: [{ ...targets[0], vision: false }] }, { outputDir: join(root, 'unsupported') })
  assert.equal(omitted.requestsSent, 0); assert.equal(omitted.counts.not_applicable, 2)
})

test('copied script defaults to all groups and can execute just code with one config file', async t => {
  const root = await directory(t)
  await copyPackage(root)
  await writeFile(join(root, 'config.local.json'), JSON.stringify({ projectRoot: '.', targets: configuredTargets(), codeChecks: [{ id: 'smoke', command: [process.execPath, '-e', 'console.log("OK")'] }] }))
  const run = args => new Promise(resolve => {
    const child = spawn(process.execPath, [join(root, 'run.mjs'), ...args], { cwd: root })
    let output = ''; child.stdout.on('data', v => { output += v }); child.stderr.on('data', v => { output += v })
    child.on('close', code => resolve({ code, output }))
  })
  const dry = await run(['--dry-run']); assert.equal(dry.code, 0, dry.output)
  assert.ok(dry.output.includes(groups.join(', ')))
  const one = await run(['--group', 'code', '--out', join(root, 'result')]); assert.equal(one.code, 0, one.output)
  const report = JSON.parse(await readFile(join(root, 'result/report.json'), 'utf8'))
  assert.deepEqual(report.groups, ['code']); assert.equal(report.cases.length, 1)
  assert.match(one.output, /Report: .*report.md/); assert.match(one.output, /Log: .*run.log/)
  const explicit = await run(['--config', join(root, 'config.local.json'), '--group', 'code', '--out', join(root, 'explicit')])
  assert.equal(explicit.code, 0, explicit.output)
  const automatic = await run(['--group', 'code']); assert.equal(automatic.code, 0, automatic.output)
  const results = await readdir(join(root, 'results'))
  assert.equal(results.length, 1)
  assert.equal(JSON.parse(await readFile(join(root, 'results', results[0], 'report.json'))).status, 'passed')
  assert.ok(!(await readdir(join(root, 'src'))).includes('results'))
})

test('missing protocols or unresolved credentials stop before any backend, output or code check starts', async t => {
  const root = await directory(t)
  for (const targets of [[], configuredTargets().slice(0, 1), configuredTargets().slice(0, 2), configuredTargets().slice(0, 3), Array.from({ length: 4 }, (_, i) => ({ ...configuredTargets()[0], id: `chat-${i}` }))]) {
    for (const group of ['all', ...groups.filter(g => g !== 'code')]) await assert.rejects(runSuite({ projectRoot: root, targets }, { group, outputDir: join(root, 'result') }), /缺少：.*gemini/)
  }
  const targets = configuredTargets(); delete targets[3].apiKey; targets[3].apiKeyEnv = 'AUDIT_GEMINI_KEY'
  for (const key of [undefined, '', 'YOUR_GEMINI_KEY', 'bad\nkey']) await assert.rejects(runSuite({ projectRoot: root, targets }, { outputDir: join(root, 'result'), env: { AUDIT_GEMINI_KEY: key } }), /gemini-4.*AUDIT_GEMINI_KEY/)
  assert.deepEqual(await readdir(root), ['backend'])
})

test('example configuration requires four real channels and matches runtime defaults', async () => {
  const config = exampleConfig()
  assert.deepEqual(config.targets.map(t => t.protocol).sort(), [...protocols].sort())
  assert.equal(config.maxOutputTokens, defaults.maxOutputTokens); assert.equal(config.timeoutMs, defaults.timeoutMs)
  assert.equal(config.concurrency, 32)
  assert.throws(() => planSuite(config), /示例地址/)
  assert.deepEqual(planSuite(config, 'code').groups, ['code'])
  for (const [field, value] of [['baseUrl', 'https://example.com/v1'], ['baseUrl', 'https://chat.provider.example/v1'], ['model', 'YOUR_CHAT_MODEL'], ['apiKey', 'YOUR_CHAT_KEY']]) {
    const targets = configuredTargets(); targets[0][field] = value
    assert.throws(() => planSuite({ targets }), new RegExp(`chat-1.${field}`))
  }
})

test('CLI requires copying the example, never creates or overwrites config, and allows code without it', async t => {
  const root = await realpath(await directory(t))
  await copyPackage(root)
  const run = (args = []) => new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [join(root, 'run.mjs'), ...args], { cwd: root })
    let output = ''; child.stdout.on('data', v => { output += v }); child.stderr.on('data', v => { output += v })
    child.on('error', reject); child.on('close', code => resolve({ code, output }))
  })
  for (const args of [[], ['--dry-run']]) {
    const first = await run(args); assert.equal(first.code, 2); assert.match(first.output, /缺少配置/)
    assert.ok(first.output.includes(`cp '${join(root, 'config.example.json')}' '${join(root, 'config.local.json')}'`), first.output)
    assert.ok(!(await readdir(root)).includes('config.local.json'))
  }
  const codeOnly = await run(['--group', 'code', '--dry-run']); assert.equal(codeOnly.code, 0, codeOnly.output)
  const path = join(root, 'config.local.json')
  await copyFile(join(root, 'config.example.json'), path)
  const template = JSON.parse(await readFile(path, 'utf8'))
  const second = await run(); assert.equal(second.code, 2); assert.match(second.output, /仍为示例地址/)
  template.targets[0].model = 'user-edited-model'
  const edited = JSON.stringify(template); await writeFile(path, edited)
  const third = await run(); assert.equal(third.code, 2); assert.equal(await readFile(path, 'utf8'), edited)
  assert.ok(!(await readdir(root)).includes('results'))
})

test('mid-run interruption saves command output, execution log and remaining skipped cases', async t => {
  const root = await directory(t), controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 500)
  try {
    const result = await runSuite({ projectRoot: root, codeChecks: [
      { id: 'running', command: [process.execPath, '-e', 'console.log("partial command output"); setInterval(() => {}, 1000)'] },
      { id: 'later', command: [process.execPath, '-e', 'console.log("should not run")'] },
    ] }, { group: 'code', outputDir: join(root, 'result'), signal: controller.signal })
    assert.equal(result.status, 'interrupted')
    assert.deepEqual(result.cases.map(c => c.status), ['skipped', 'skipped'])
    assert.match(await readFile(join(root, 'result', result.cases[0].evidence[0]), 'utf8'), /partial command output/)
    assert.match(await readFile(join(root, 'result/run.log'), 'utf8'), /RUN END interrupted/)
  } finally { clearTimeout(timer) }
})

test('self-tests block external fetches in the test process and CLI subprocesses', async () => {
  await assert.rejects(fetch('https://example.invalid'), /Self-test blocked an external request/)
  const child = spawn(process.execPath, ['-e', 'fetch("https://example.invalid").then(() => process.exit(1), e => { console.log(e.message); process.exitCode = e.message === "Self-test blocked an external request" ? 0 : 1 })'])
  let output = ''; child.stdout.on('data', value => { output += value }); child.stderr.on('data', value => { output += value })
  const code = await new Promise((resolve, reject) => { child.on('error', reject); child.on('close', resolve) })
  assert.equal(code, 0, output)
  assert.match(output, /Self-test blocked an external request/)
})

test('protocol summary remains visible without adding a second pass or failure to counts', async t => {
  const root = await directory(t), upstream = await mockUpstream()
  t.after(() => upstream.close())
  for (const fail of [false, true]) {
    const targets = protocols.map(protocol => ({ protocol, baseUrl: `${upstream.baseUrl}/${fail && protocol === 'chat' ? 'wrong' : protocol}`, auth: 'none', model: 'test' }))
    const report = await runSuite({ projectRoot: join(root, 'missing'), targets, scenarios: ['text'], streams: [false] }, { group: 'protocol', outputDir: join(root, `matrix-${fail}`) })
    assert.equal(report.cases.find(c => c.id === 'protocol/matrix').summary, true)
    assert.equal(report.counts.failed, fail ? 1 : 0)
    assert.equal(report.counts.passed, fail ? 3 : 4)
    assert.equal(Object.values(report.counts).reduce((a, b) => a + b, 0), report.cases.length - 1)
    const saved = JSON.parse(await readFile(join(root, `matrix-${fail}/report.json`), 'utf8'))
    assert.deepEqual(saved.counts, report.counts)
    assert.match(await readFile(join(root, `matrix-${fail}/report.md`), 'utf8'), /不含汇总项/)
  }
})

test('availability accepts valid nonempty replies while memory markers, schema and HTTP errors remain strict', () => {
  for (const protocol of protocols) for (const stream of [false, true]) {
    const response = text => ({ status: 200, headers: { 'content-type': stream ? 'text/event-stream' : 'application/json' }, raw: replyWire(protocol, text, false, stream) })
    assert.equal(checkReply(response('好的'), protocol, { stream }).text, '好的')
    assert.throws(() => checkReply(response('好的'), protocol, { stream, expectedText: 'AUDIT_expected' }), /Expected AUDIT_expected/)
    assert.equal(checkReply(response('AUDIT_expected'), protocol, { stream, expectedText: 'AUDIT_expected' }).text, 'AUDIT_expected')
    assert.throws(() => checkReply(response('  '), protocol, { stream }), /nonempty response/)
    assert.throws(() => checkReply({ ...response('好的'), status: 502 }, protocol, { stream }))
    assert.throws(() => checkReply({ ...response('好的'), error: new Error('request failed') }, protocol, { stream }), /request failed/)
    if (stream) assert.throws(() => checkReply({ ...response('好的'), headers: {} }, protocol, { stream }), /text\\\/event-stream/)
    else assert.throws(() => checkReply({ ...response('好的'), raw: '{"text":"好的"}' }, protocol))
  }
})

test('availability accepts Gemini optional usage breakdowns but still honours requireUsage', () => {
  const body = JSON.parse(replyWire('gemini', 'OK'))
  body.usageMetadata = { promptTokenCount: 5, thoughtsTokenCount: 94, totalTokenCount: 99 }
  const response = () => ({ status: 200, headers: {}, raw: JSON.stringify(body) })
  assert.equal(checkReply(response(), 'gemini').text, 'OK')
  delete body.usageMetadata
  assert.throws(() => checkReply(response(), 'gemini'), /usage_missing/)
  assert.equal(checkReply(response(), 'gemini', { requireUsage: false }).text, 'OK')
})
