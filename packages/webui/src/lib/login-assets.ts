import mediaVersion from './login-media-version.json'
import { CharacterTrace, type CharacterTraceAsset } from './character-trace'

export async function loadCharacterTrace(signal: AbortSignal): Promise<CharacterTrace> {
  if (typeof DecompressionStream === 'undefined') throw new Error('Trace decompression unavailable')
  const base = `${import.meta.env.BASE_URL}assets/`
  const response = await fetch(`${base}elysia-character-trace.json?v=${mediaVersion.trace}`, { signal })
  if (!response.ok) throw new Error('Character trace unavailable')
  const manifest = await response.json() as CharacterTraceAsset
  if (manifest.source.sha256 !== mediaVersion.video || manifest.payloadSha256 !== mediaVersion.trace || manifest.target.sha256 !== mediaVersion.target) throw new Error('Stale character trace assets')
  if (manifest.payload !== 'elysia-character-trace.bin.gz' || manifest.target.file !== 'role-mask.png'
    || manifest.decodedBytes > 96 * 1024 * 1024) throw new Error('Unexpected character trace manifest')
  const payloadResponse = await fetch(`${base}${manifest.payload}?v=${manifest.payloadSha256}`, { signal })
  if (!payloadResponse.ok) throw new Error('Character trace payload unavailable')
  const received = await payloadResponse.arrayBuffer()
  const header = new Uint8Array(received, 0, Math.min(2, received.byteLength))
  const compressed = header[0] === 0x1f && header[1] === 0x8b
  const checksum = async (data: ArrayBuffer, expected: string) => {
    if (!crypto.subtle) return
    const actual = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', data)), (byte) => byte.toString(16).padStart(2, '0')).join('')
    if (actual !== expected) throw new Error('Character trace checksum mismatch')
  }
  if (compressed) await checksum(received, manifest.payloadSha256)
  const decoded = compressed ? await new Response(new Blob([received]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer() : received
  await checksum(decoded, manifest.decodedSha256)
  if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
  const targetImage = new Image()
  targetImage.src = `${import.meta.env.BASE_URL}role-mask.png?v=${manifest.target.sha256}`
  await targetImage.decode()
  if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
  return new CharacterTrace(manifest, decoded, targetImage)
}
