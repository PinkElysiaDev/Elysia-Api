import { readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { gunzipSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { dirname, resolve, relative, isAbsolute } from 'node:path'

const root = dirname(fileURLToPath(import.meta.url))
const manifest = JSON.parse(await readFile(resolve(root, 'archive-manifest.json')))
let compressedMeasurements = 0
for (const file of manifest) {
  const path = resolve(root, file.path)
  const local = relative(root, path)
  if (isAbsolute(local) || local.startsWith('..')) throw new Error('Invalid archive path')
  const data = await readFile(path)
  if (data.length !== file.bytes || createHash('sha256').update(data).digest('hex') !== file.sha256) {
    throw new Error(`Evidence mismatch: ${file.path}`)
  }
  if (file.path.endsWith('/compressed-measurements.json')) {
    for (const original of JSON.parse(data)) {
      const raw = gunzipSync(await readFile(resolve(dirname(path), original.path)))
      if (createHash('sha256').update(raw).digest('hex') !== original.sha256) throw new Error('Measurement migration mismatch')
      compressedMeasurements++
    }
  }
}
console.log(JSON.stringify({ status: 'passed', files: manifest.length, compressedMeasurements }, null, 2))
