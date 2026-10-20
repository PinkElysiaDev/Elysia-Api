import { readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import { dirname, resolve, relative, isAbsolute } from 'node:path'

const root = dirname(fileURLToPath(import.meta.url))
const manifest = JSON.parse(await readFile(resolve(root, 'manifest.json')))
let profiles = 0
let cpuProfiles = 0
for (const entry of manifest) {
  const path = resolve(root, entry.path)
  const local = relative(root, path)
  if (local.startsWith('..') || isAbsolute(local)) throw new Error('Invalid archive path')
  const data = await readFile(path)
  if (data.length !== entry.bytes || createHash('sha256').update(data).digest('hex') !== entry.sha256) {
    throw new Error(`Evidence mismatch: ${entry.path}`)
  }
  if (entry.path.endsWith('.pprof')) profiles++
  if (entry.path.endsWith('/cpu.pprof')) cpuProfiles++
}
if (profiles !== 60 || cpuProfiles !== 12) throw new Error('Incomplete profiling archive')
console.log(JSON.stringify({ status: 'passed', files: manifest.length, profiles, cpuProfiles }, null, 2))
