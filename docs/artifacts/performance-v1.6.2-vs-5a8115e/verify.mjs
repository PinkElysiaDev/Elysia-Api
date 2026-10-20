import { readFile, readdir, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { dirname, isAbsolute, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const directory = dirname(fileURLToPath(import.meta.url))
const sha = bytes => createHash('sha256').update(bytes).digest('hex')
const fileAt = (base, path) => {
  const file = resolve(base, path)
  const local = relative(base, file)
  if (!local || local.startsWith('..') || isAbsolute(local)) throw new Error(`Unsafe evidence path: ${path}`)
  return file
}
const archive = JSON.parse(await readFile(join(directory, 'archive.json')))
let originalFiles = 0
for (const group of archive.groups) {
  const manifestFile = fileAt(directory, group.manifest)
  const bytes = await readFile(manifestFile)
  if (sha(bytes) !== group.manifestSHA256) throw new Error(`Original manifest changed: ${group.name}`)
  const entries = JSON.parse(bytes)
  if (entries.length !== group.evidenceFiles) throw new Error(`Original evidence count changed: ${group.name}`)
  for (const entry of entries) {
    const file = fileAt(dirname(manifestFile), entry.path)
    if (sha(await readFile(file)) !== entry.sha256) throw new Error(`Original evidence changed: ${file}`)
    originalFiles++
  }
}
const files = []
async function visit(folder) {
  for (const entry of await readdir(folder, { withFileTypes: true })) {
    const file = join(folder, entry.name)
    if (entry.isDirectory()) await visit(file)
    else if (file !== join(directory, 'manifest.json')) files.push({ path: relative(directory, file).split('\\').join('/'), sha256: sha(await readFile(file)) })
  }
}
await visit(directory)
files.sort((a, b) => a.path.localeCompare(b.path, 'en'))
const manifestFile = join(directory, 'manifest.json')
if (process.argv.includes('--seal')) await writeFile(manifestFile, JSON.stringify(files, null, 2) + '\n')
const manifest = JSON.parse(await readFile(manifestFile))
if (JSON.stringify(files) !== JSON.stringify(manifest)) throw new Error('Archive checksum/file inventory mismatch; do not reseal modified original evidence')
const profiles = files.filter(file => file.path.endsWith('.pprof'))
const cpu = profiles.filter(file => file.path.endsWith('/cpu.pprof'))
const images = files.filter(file => file.path.endsWith('.png'))
if (originalFiles !== 357 || cpu.length !== 12 || images.length !== 24) throw new Error('Profiling archive is incomplete')
if (process.argv.includes('--index')) {
  const root = execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd: directory, encoding: 'utf8' }).trim()
  for (const entry of [...files, { path: 'manifest.json', sha256: sha(await readFile(manifestFile)) }]) {
    const file = relative(root, join(directory, entry.path)).split('\\').join('/')
    const bytes = execFileSync('git', ['show', `:${file}`], { cwd: root, maxBuffer: 64 << 20 })
    if (sha(bytes) !== entry.sha256) throw new Error(`Git index altered evidence bytes: ${file}`)
  }
}
console.log(JSON.stringify({ status: 'passed', originalEvidenceFiles: originalFiles, archiveFiles: files.length + 1, profiles: profiles.length, cpuProfiles: cpu.length, flamegraphs: images.length, gitIndexChecked: process.argv.includes('--index') }, null, 2))
