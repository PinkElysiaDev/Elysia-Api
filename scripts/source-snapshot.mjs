import { cpSync, existsSync, mkdirSync, readFileSync, realpathSync, statSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { dirname, isAbsolute, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const manifestPath = 'scripts/source-snapshot.json'
const formatVersion = 1
const documentation = /^docs\/(?:(?:protocol|conversion)-.*|(?:agent-cli|webui-api|webui-data-model|deployment)(?:\.en)?)\.md$/

function isSnapshotSource(path) {
  if (path.startsWith('backend/server/_snapshot/') || path.startsWith('backend/webui/')) return false
  if (/(?:^|\/)(?:config(?:\.[^/]+)?\.json|credentials[^/]*|\.env[^/]*|\.master-key|master\.key)$/.test(path)) return false
  return /^backend\/.*\.go$/.test(path)
    || /^backend\/storage\/sql\/.*\.sql$/.test(path)
    || /^backend\/(?:.*\/(?:testdata|definitions|presets)\/.*|server\/model_catalog_snapshot)\.json$/.test(path)
    || /^backend\/.*\/testdata\/.*\.sse$/.test(path)
    || /^backend\/protocol\/builtin\/packages\/.*\.json$/.test(path)
    || /^packages\/webui\/src\/.*\.(?:ts|tsx|css|json)$/.test(path)
    || documentation.test(path)
    || /^docs\/examples\/.*\.(?:md|json|go|ts)$/.test(path)
}

/** List existing tracked sources and explicitly selected new source files. */
export function listSnapshotSources(root, additions = []) {
  const result = spawnSync('git', ['ls-files', '-z', '--cached'], { cwd: root, encoding: 'utf8' })
  if (result.status !== 0) throw new Error('Updating the source manifest requires a Git checkout')
  const tracked = result.stdout.split('\0').filter(path => isSnapshotSource(path) && existsSync(join(root, path)))
  for (const path of additions) {
    resolveSnapshotSource(realpathSync(root), path)
  }
  return [...new Set([...tracked, ...additions])].sort()
}

function resolveSnapshotSource(repository, path) {
  if (typeof path !== 'string' || path.includes('\\') || path.split('/').some(part => !part || part === '.' || part === '..') || isAbsolute(path) || !isSnapshotSource(path)) {
    throw new Error(`Invalid source snapshot entry: ${path}`)
  }
  const source = realpathSync(join(repository, path))
  const relativeSource = relative(repository, source)
  if (relativeSource.startsWith('..') || isAbsolute(relativeSource) || !statSync(source).isFile()) throw new Error(`Source snapshot entry escapes repository or is not a file: ${path}`)
  return { path, source }
}

/** Resolve the shipped manifest without scanning local configuration or history. */
export function readSnapshotPlan(root) {
  const repository = realpathSync(root)
  const manifest = JSON.parse(readFileSync(join(repository, manifestPath), 'utf8'))
  if (manifest.version !== formatVersion || !Array.isArray(manifest.files)) throw new Error('Unsupported source snapshot manifest')
  const seen = new Set()
  const plan = manifest.files.map((path) => {
    if (seen.has(path)) {
      throw new Error(`Invalid source snapshot entry: ${path}`)
    }
    seen.add(path)
    return resolveSnapshotSource(repository, path)
  })
  if (existsSync(join(repository, '.git')) && JSON.stringify([...manifest.files].sort()) !== JSON.stringify(listSnapshotSources(repository, manifest.files))) {
    throw new Error('Source snapshot manifest is stale; run node scripts/source-snapshot.mjs --update and select new sources with --include=PATH')
  }
  return plan
}

/** Copy only a prevalidated source plan into an empty build directory. */
export function copySnapshotPlan(plan, destination) {
  for (const { path, source } of plan) {
    const output = join(destination, path)
    mkdirSync(dirname(output), { recursive: true })
    cpSync(source, output)
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv[2] !== '--update' || process.argv.slice(3).some(arg => !arg.startsWith('--include='))) throw new Error('Usage: node scripts/source-snapshot.mjs --update [--include=PATH ...]')
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const previous = JSON.parse(readFileSync(join(root, manifestPath), 'utf8')).files.filter(path => existsSync(join(root, path)))
  const additions = process.argv.slice(3).map(arg => arg.slice('--include='.length))
  writeFileSync(join(root, manifestPath), JSON.stringify({ version: formatVersion, files: listSnapshotSources(root, [...previous, ...additions]) }, null, 2) + '\n')
  readSnapshotPlan(root)
}
