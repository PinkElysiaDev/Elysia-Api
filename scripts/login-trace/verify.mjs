import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { gunzipSync } from 'node:zlib'

const root = fileURLToPath(new URL('../../', import.meta.url))
const assets = join(root, 'packages/webui/public/assets')
const readJson = (path) => JSON.parse(readFileSync(path, 'utf8'))
const sha256 = (data) => createHash('sha256').update(data).digest('hex')
const manifest = readJson(join(assets, 'elysia-character-trace.json'))
const revision = readJson(join(root, 'packages/webui/src/lib/login-media-version.json'))
const compressed = readFileSync(join(assets, manifest.payload))

function requireCondition(condition, description) {
  if (!condition) throw new Error(`Character trace: ${description}. Regenerate with python scripts/login-trace/extract.py`)
}

requireCondition(manifest.version === 1 && manifest.coordinateEncoding === 'pixel-delta-i16', 'unsupported format')
requireCondition(sha256(readFileSync(join(assets, manifest.source.file))) === manifest.source.sha256, 'video checksum mismatch')
requireCondition(sha256(readFileSync(join(assets, '../role-mask.png'))) === manifest.target.sha256, 'target checksum mismatch')
requireCondition(sha256(readFileSync(join(root, 'scripts/login-trace/annotations.json'))) === manifest.annotationSha256, 'annotations changed')
requireCondition(sha256(compressed) === manifest.payloadSha256, 'payload checksum mismatch')
requireCondition(revision.video === manifest.source.sha256 && revision.trace === manifest.payloadSha256 && revision.target === manifest.target.sha256, 'browser revision mismatch')
const payload = gunzipSync(compressed, { maxOutputLength: 96 * 1024 * 1024 })
requireCondition(payload.length === manifest.decodedBytes, 'decoded size mismatch')
requireCondition(sha256(payload) === manifest.decodedSha256, 'decoded checksum mismatch')
requireCondition(manifest.frameTimes.length === 960 && manifest.frameOffsets.length === 960, 'incomplete video coverage')
requireCondition(manifest.source.width === 1920 && manifest.source.height === 1080 && manifest.source.duration === 16, 'unexpected source geometry')
let vertices = 0
for (let frame = 0; frame < manifest.frameTimes.length; frame++) {
  requireCondition(frame === 0 || manifest.frameTimes[frame] > manifest.frameTimes[frame - 1], 'unordered presentation timestamps')
  let offset = manifest.frameOffsets[frame]
  const end = manifest.frameOffsets[frame + 1] ?? payload.length
  const count = payload.readUInt32LE(offset)
  offset += 4
  const groups = new Set()
  const identifiers = new Set()
  for (let path = 0; path < count; path++) {
    const identifier = payload.readUInt32LE(offset)
    const group = payload.readUInt8(offset + 4)
    const closed = payload.readUInt8(offset + 5)
    const points = payload.readUInt16LE(offset + 6)
    requireCondition(group < 4 && closed <= 1 && points >= 2 && !identifiers.has(identifier), `invalid path at frame ${frame}`)
    identifiers.add(identifier)
    groups.add(group)
    offset += 8
    let horizontal = 0
    let vertical = 0
    for (let point = 0; point < points; point++) {
      horizontal += payload.readInt16LE(offset)
      vertical += payload.readInt16LE(offset + 2)
      offset += 4
      requireCondition(horizontal >= 0 && horizontal <= manifest.source.width && vertical >= 0 && vertical <= manifest.source.height, `out-of-bounds coordinate at frame ${frame}`)
    }
    vertices += points
  }
  requireCondition(offset === end && groups.size === 4, `invalid frame ${frame}`)
}
requireCondition(manifest.target.paths.length > 0 && manifest.target.paths.every((path) => path.points.length >= 4 && path.points.length % 2 === 0 && path.points.every((point) => Number.isFinite(point) && point >= 0 && point <= 1)), 'invalid target strokes')
console.log(`Character trace verified: 960 frames, ${vertices.toLocaleString()} vertices, ${(compressed.length / 1024 / 1024).toFixed(2)} MiB compressed.`)
