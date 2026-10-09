#!/usr/bin/env node
// Interactive acceptance companion. Uses an isolated VS Code profile/workspace,
// a temporary gateway, and the user's locally installed extension. No CLI install.
import { readFile, writeFile, mkdir, cp } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { resolve, dirname, join, basename } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawn } from 'node:child_process'
import { localInstance } from './src/local.mjs'
import { persistedCall, eventSequence } from './src/evidence.mjs'
import { redactor } from './src/audit.mjs'
import { captureProxy } from './src/plugin-proxy.mjs'
import { serialize } from './src/concurrency.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const args = {}
for (let i = 2; i < process.argv.length; i += 2) {
  if (!['--config', '--extension', '--out'].includes(process.argv[i]) || !process.argv[i+1]) throw new Error('Usage: node plugin.mjs --config config.local.json --extension INSTALLED_EXTENSION_DIRECTORY --out NEW_DIRECTORY')
  args[process.argv[i].slice(2)] = resolve(process.argv[i+1])
}
if (!args.extension || !args.out) throw new Error('--extension and --out are required')
const config = JSON.parse(await readFile(args.config || join(root, 'scripts/protocol-audit/config.local.json'), 'utf8'))
const target = config.targets.find(t => t.protocol === 'anthropic')
if (!target) throw new Error('Anthropic target is required for native plugin acceptance')
const key = target.apiKey || process.env[target.apiKeyEnv]
if (!key) throw new Error('Anthropic credential is missing')
await mkdir(args.out) // Refuse reuse: evidence and profiles belong to one run.
const profile = join(args.out, 'profile'), workspace = join(args.out, 'workspace'), extensions = join(args.out, 'extensions')
await mkdir(join(profile, 'User'), { recursive: true }); await mkdir(workspace); await mkdir(extensions)
const extensionPackage = JSON.parse(await readFile(join(args.extension, 'package.json'), 'utf8'))
if (extensionPackage.publisher.toLowerCase() !== 'anthropic' || extensionPackage.name !== 'claude-code') throw new Error('Expected installed Anthropic Claude Code extension')
await cp(args.extension, join(extensions, basename(args.extension)), { recursive: true })
await writeFile(join(workspace, 'audit-note.txt'), 'This is a synthetic protocol audit workspace.\nThe marker is PLUGIN_AUDIT_7.\n')
let clean = redactor([key]), gateway, proxy, count = 0
const report = { startedAt: new Date().toISOString(), extension: { version: extensionPackage.version, path: args.extension }, calls: [], acceptance: 'not_verified' }
const save = serialize(() => writeFile(join(args.out, 'report.json'), JSON.stringify(clean(report), null, 2)))
async function command(_name, argv, cwd) {
  await new Promise((resolve, reject) => {
    const child = spawn(argv[0], argv.slice(1), { cwd, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''; child.stdout.on('data', b => { output += b }); child.stderr.on('data', b => { output += b })
    child.on('error', reject); child.on('close', code => code === 0 ? resolve() : reject(new Error(clean(output))))
  })
}
try {
  gateway = await localInstance(root, command, undefined, config.timeoutMs)
  clean = redactor([key, gateway.panel, gateway.token])
  async function admin(path, body) {
    const response = await fetch(`${gateway.baseUrl}/api/admin/${path}`, { method: body === undefined ? 'GET' : 'POST', headers: { authorization: `Bearer ${gateway.panel}`, 'content-type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
    const result = await response.json()
    if (!response.ok || !result.ok) throw new Error(`Management ${path} failed: ${clean(JSON.stringify(result))}`)
    return result.data
  }
  await admin('api-tokens', { name: 'plugin-audit', token: gateway.token, enabled: true, allowedGroups: [], scopes: [] })
  await admin('model-sources', { id: 'plugin-audit', name: 'plugin-audit', baseUrl: target.baseUrl, apiKey: key, platform: 'anthropic', enabled: true, autoFetchModels: false, manualModels: [{ id: target.model, name: target.model, type: 'llm', enabled: true, available: true, toolsCapable: true }] })
  await admin('model-groups', { id: 'plugin-audit', name: 'plugin-audit', enabled: true, models: [`plugin-audit:${target.model}`], strategy: 'sequential', maxRetries: 0, type: 'llm', toolsCapable: true })
  report.runtime = await admin('protocols'); report.bindings = await admin('protocols/bindings')
  proxy = await captureProxy(gateway.baseUrl, async transport => {
    const trace = await persistedCall(gateway.baseUrl, gateway.panel, transport.requestId)
    const id = ++count, record = trace.record
    const entry = { id, ...transport, error: record?.error, evidence: `call-${id}.json` }
    report.calls.push(entry)
    await writeFile(join(args.out, entry.evidence), JSON.stringify(clean({ ...trace, entry, events: eventSequence(record?.downstreamResponse?.content || '', record?.stream) }), null, 2))
    await save()
    console.log(`Captured ${transport.method} ${transport.path.split('?')[0]} HTTP ${transport.status}; persisted=${trace.available}`)
  }, error => { report.captureError = clean(error.message); console.error('Evidence capture failed:', clean(error.message)) })
  const baseUrl = proxy.baseUrl
  const vars = { ANTHROPIC_BASE_URL: baseUrl, ANTHROPIC_AUTH_TOKEN: gateway.token, ANTHROPIC_API_KEY: gateway.token, ANTHROPIC_MODEL: 'plugin-audit', ANTHROPIC_DEFAULT_HAIKU_MODEL: 'plugin-audit', ANTHROPIC_DEFAULT_SONNET_MODEL: 'plugin-audit', ANTHROPIC_DEFAULT_OPUS_MODEL: 'plugin-audit', CLAUDE_CONFIG_DIR: join(args.out, 'claude'), CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1' }
  await writeFile(join(profile, 'User', 'settings.json'), JSON.stringify({ 'claudeCode.environmentVariables': Object.entries(vars).map(([name,value])=>({name,value})), 'claudeCode.disableLoginPrompt': true, 'claudeCode.useTerminal': false, 'claudeCode.attachOpenFile': false, 'telemetry.telemetryLevel': 'off' }, null, 2), { mode: 0o600 })
  await writeFile(join(args.out, 'ready.json'), JSON.stringify({ profile, workspace, extensions, baseUrl, extensionVersion: extensionPackage.version, stopFile: join(args.out, 'STOP') }, null, 2))
  await save()
  console.log(`Isolated plugin audit ready: ${join(args.out, 'ready.json')}`)
  console.log('Use VS Code --user-data-dir PROFILE --extensions-dir EXTENSIONS --new-window WORKSPACE. Create STOP in the output directory to export and stop.')
  await new Promise(resolve => {
    const stop = () => { clearInterval(timer); resolve() }
    const timer = setInterval(() => { if (existsSync(join(args.out, 'STOP'))) stop() }, 500)
    process.once('SIGINT', stop); process.once('SIGTERM', stop)
  })
} finally {
  if (proxy) await proxy.close()
  if (gateway) { await gateway.close(); await writeFile(join(args.out, 'backend.log'), clean(gateway.log())) }
  report.finishedAt = new Date().toISOString(); await save()
}
