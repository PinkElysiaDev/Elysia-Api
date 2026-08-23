// Update the embedded model capability catalog snapshot (backend/server/model_catalog_snapshot.json).
//
// Data source: models.dev api.json (193+ providers, 7000+ models). The raw file is
// ~4MB; this script trims it to the fields the catalog actually consumes
// (id/name/type/tool_call/structured_output/modalities/reasoning/limit), cutting
// the embedded size by ~4x.
//
// Usage:
//   node scripts/update-model-catalog.mjs [--proxy http://127.0.0.1:7890] [--url <api.json url>]
//
// The output uses the same envelope as the runtime disk cache ({"fetchedAt", "body"}),
// so the Go loader treats snapshot and cache uniformly and can pick the newer one.
// Commit the generated file; re-run occasionally (or when new models ship) to refresh.

import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const proxy = valueOf('--proxy')
const url = valueOf('--url') ?? 'https://models.dev/api.json'
const outPath = join(repoRoot, 'backend', 'server', 'model_catalog_snapshot.json')

function valueOf(flag) {
  const index = args.indexOf(flag)
  return index >= 0 ? args[index + 1] : undefined
}

function log(message) {
  console.log(`==> ${message}`)
}

// Download via curl: dependency-free and proxy-friendly (global fetch needs extra
// setup for proxies; curl -x works everywhere).
const curlArgs = ['-sS', '--fail', '--max-time', '120']
if (proxy) curlArgs.push('-x', proxy)
curlArgs.push(url)
log(`Fetching ${url}${proxy ? ` via ${proxy}` : ''}`)
const raw = execFileSync('curl', curlArgs, { encoding: 'utf8', maxBuffer: 64 << 20 })
const providers = JSON.parse(raw)
log(`Loaded ${Object.keys(providers).length} providers`)

// Trim to the catalog-consumed fields; keep the provider→models object shape the
// Go parser expects (mirror arrays are handled at runtime, snapshots stay canonical).
let modelCount = 0
const trimmed = {}
for (const [slug, provider] of Object.entries(providers)) {
  const models = provider?.models
  if (!models) continue
  const entries = Array.isArray(models) ? models.map((m) => [m.id, m]) : Object.entries(models)
  const trimmedModels = {}
  for (const [id, model] of entries) {
    if (!id) continue
    const slim = {}
    if (model.name) slim.name = model.name
    if (model.type) slim.type = model.type
    if (model.tool_call !== undefined) slim.tool_call = model.tool_call
    if (model.structured_output !== undefined) slim.structured_output = model.structured_output
    if (model.modalities?.input?.length) slim.modalities = { input: model.modalities.input }
    if (model.reasoning?.supported !== undefined) slim.reasoning = { supported: model.reasoning.supported }
    if (model.limit?.context) slim.limit = { context: model.limit.context }
    trimmedModels[id] = slim
    modelCount++
  }
  if (Object.keys(trimmedModels).length > 0) trimmed[slug] = { models: trimmedModels }
}

const payload = JSON.stringify({
  fetchedAt: new Date().toISOString(),
  body: trimmed,
})
writeFileSync(outPath, payload)
const sizeKb = Math.round(readFileSync(outPath).length / 1024)
log(`Snapshot written: ${outPath}`)
log(`Providers: ${Object.keys(trimmed).length}, models: ${modelCount}, size: ${sizeKb} KB`)
