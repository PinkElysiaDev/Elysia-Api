import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { gunzipSync, gzipSync } from 'node:zlib'

// 从已校验的逐帧数据无损切块，不重提几何、不抽帧。开发和构建使用同一产物。
const assets = fileURLToPath(new URL('../../packages/webui/public/assets/', import.meta.url))
const manifest = JSON.parse(readFileSync(join(assets, 'elysia-character-trace.json'), 'utf8'))
const revision = JSON.parse(readFileSync(new URL('../../packages/webui/src/lib/login-media-version.json', import.meta.url), 'utf8'))
const hash = (data) => createHash('sha256').update(data).digest('hex')
const compressed = readFileSync(join(assets, manifest.payload))
const payload = gunzipSync(compressed, { maxOutputLength: 96 * 1024 * 1024 })
if (hash(compressed) !== revision.trace || hash(payload) !== manifest.decodedSha256
  || payload.length !== manifest.decodedBytes || manifest.source.sha256 !== revision.video || manifest.target.sha256 !== revision.target) {
  throw new Error('Character trace delivery: stale source assets')
}
const output = join(assets, 'login-trace')
mkdirSync(output, { recursive: true })
const chunks = []
for (let firstFrame = 0; firstFrame < manifest.frameTimes.length;) {
  let endFrame = firstFrame + 1
  while (endFrame < manifest.frameTimes.length && manifest.frameTimes[endFrame] < manifest.frameTimes[firstFrame] + 1 - 0.00001) endFrame++
  const start = manifest.frameOffsets[firstFrame]
  const end = manifest.frameOffsets[endFrame] ?? payload.length
  const decoded = payload.subarray(start, end)
  const encoded = gzipSync(decoded, { level: 9 })
  // 用真实 JSON 封装压缩数据，避免 IDM 等扩展按 .gz / gzip MIME 接管请求。
  const file = `${String(chunks.length).padStart(3, '0')}.json`
  writeFileSync(join(output, file), JSON.stringify({ encoding: 'gzip-base64', data: encoded.toString('base64') }))
  chunks.push({ file, firstFrame, frameCount: endFrame - firstFrame, decodedBytes: decoded.length, sha256: hash(encoded), decodedSha256: hash(decoded) })
  firstFrame = endFrame
}
// 格式更换使用新清单 URL，避免已强缓存的二进制清单继续引用 .gz。
writeFileSync(join(output, 'index-json-v1.json'), JSON.stringify({ ...manifest, chunks }))
console.log(`Character trace delivery: ${chunks.length} lossless chunks; startup ${(readFileSync(join(output, chunks[0].file)).length / 1024).toFixed(0)} KiB instead of ${(compressed.length / 1024 / 1024).toFixed(2)} MiB.`)
