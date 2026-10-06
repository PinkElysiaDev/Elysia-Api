import mediaVersion from './login-media-version.json'
import { CharacterTrace, frameAtTime, type CharacterTraceAsset } from './character-trace'
import { STORAGE_KEYS } from './storage-keys'

const base = `${import.meta.env.BASE_URL}assets/login-trace/`
const assetURL = (file: string) => `${base}${file}?v=${mediaVersion.trace}`

async function fetchAsset(file: string): Promise<ArrayBuffer> {
  const response = await fetch(assetURL(file))
  if (!response.ok) throw new Error(`Character trace unavailable: ${file}`)
  const payload = await response.json() as { encoding?: string; data?: unknown }
  if (payload.encoding !== 'gzip-base64' || typeof payload.data !== 'string') throw new Error('Invalid character trace encoding')
  // HTTP 返回的是普通 JSON；只在网页内部还原并解压，不暴露压缩包下载请求。
  return Uint8Array.from(atob(payload.data), (character) => character.charCodeAt(0)).buffer
}

async function checksum(data: ArrayBuffer, expected: string) {
  if (!crypto.subtle) return
  const actual = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', data)), (byte) => byte.toString(16).padStart(2, '0')).join('')
  if (actual !== expected) throw new Error('Character trace checksum mismatch')
}

async function prepareCharacterTrace() {
  if (typeof DecompressionStream === 'undefined') throw new Error('Trace decompression unavailable')
  const targetImage = new Image()
  targetImage.src = `${import.meta.env.BASE_URL}role-mask.png?v=${mediaVersion.target}`
  // 清单、首块和目标图并发，且在 React 挂载登录页之前即可开始。
  const [manifestResponse, firstChunk] = await Promise.all([
    fetch(assetURL('index-json-v1.json')), fetchAsset('000.json'), targetImage.decode(),
  ])
  if (!manifestResponse.ok) throw new Error('Character trace manifest unavailable')
  const manifest = await manifestResponse.json() as CharacterTraceAsset
  if (manifest.source.sha256 !== mediaVersion.video || manifest.payloadSha256 !== mediaVersion.trace || manifest.target.sha256 !== mediaVersion.target) throw new Error('Stale character trace assets')
  if (manifest.target.file !== 'role-mask.png' || !Number.isSafeInteger(manifest.decodedBytes)
    || manifest.decodedBytes <= 0 || manifest.decodedBytes > 96 * 1024 * 1024) throw new Error('Unexpected character trace manifest')
  const chunks = manifest.chunks
  if (!chunks?.length || chunks.length > 960) throw new Error('Missing character trace chunks')
  let nextFrame = 0
  for (const [index, chunk] of chunks.entries()) {
    const endFrame = chunk.firstFrame + chunk.frameCount
    if (chunk.file !== `${String(index).padStart(3, '0')}.json` || chunk.firstFrame !== nextFrame
      || !Number.isInteger(chunk.frameCount) || chunk.frameCount <= 0 || endFrame > manifest.frameTimes.length
      || chunk.decodedBytes !== (manifest.frameOffsets[endFrame] ?? manifest.decodedBytes) - manifest.frameOffsets[nextFrame]) throw new Error('Invalid character trace chunk')
    nextFrame = endFrame
  }
  if (nextFrame !== manifest.frameTimes.length) throw new Error('Incomplete character trace chunks')
  const payload = new ArrayBuffer(manifest.decodedBytes)
  const ready = new Set<number>()
  const requests = new Map<number, Promise<void>>()
  const chunkForFrame = (frame: number) => chunks.findIndex((chunk) => frame >= chunk.firstFrame && frame < chunk.firstFrame + chunk.frameCount)
  const loadChunk = (index: number): Promise<void> => {
    const existing = requests.get(index)
    if (existing) return existing
    const chunk = chunks[index]
    const request = (async () => {
      const received = index === 0 ? firstChunk : await fetchAsset(chunk.file)
      await checksum(received, chunk.sha256)
      const decoded = await new Response(new Blob([received]).stream().pipeThrough(new DecompressionStream('gzip'))).arrayBuffer()
      if (decoded.byteLength !== chunk.decodedBytes) throw new Error('Character trace chunk size mismatch')
      await checksum(decoded, chunk.decodedSha256)
      new Uint8Array(payload, manifest.frameOffsets[chunk.firstFrame], decoded.byteLength).set(new Uint8Array(decoded))
      ready.add(index)
    })()
    requests.set(index, request)
    return request
  }
  const trace = new CharacterTrace(manifest, payload, targetImage, (frame) => ready.has(chunkForFrame(frame)))
  const prepareAt = (time: number) => {
    const current = chunkForFrame(frameAtTime(manifest.frameTimes, manifest.source.duration, time))
    const currentRequest = loadChunk(current)
    // 壁纸播放期间提前两秒取数据，循环接缝也走同一缓存。
    for (const ahead of [1, 2]) void loadChunk((current + ahead) % chunks.length).catch(() => undefined)
    return currentRequest
  }
  await loadChunk(0)
  return { trace, prepareAt }
}

// StrictMode 重挂载、暂停/恢复及再次登录共用一份解码数据。
let prepared: ReturnType<typeof prepareCharacterTrace> | undefined
function getPreparedTrace() {
  if (!prepared) {
    const request = prepareCharacterTrace()
    prepared = request
    void request.catch(() => { if (prepared === request) prepared = undefined })
  }
  return prepared
}

export function preloadLoginAssets(): void {
  try {
    if (localStorage.getItem(STORAGE_KEYS.panelToken) || localStorage.getItem(STORAGE_KEYS.loginMotion) === 'paused'
      || document.hidden || matchMedia('(prefers-reduced-motion: reduce)').matches
      || (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData) return
  } catch { return }
  void getPreparedTrace().catch(() => undefined)
  void import('./login-renderer').catch(() => undefined)
}

export async function loadCharacterTrace(signal: AbortSignal, video: HTMLVideoElement | null = null): Promise<CharacterTrace> {
  const { trace, prepareAt } = await getPreparedTrace()
  if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
  await prepareAt(video?.currentTime ?? 0)
  if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
  const preload = () => { void prepareAt(video?.currentTime ?? 0).catch(() => undefined) }
  video?.addEventListener('timeupdate', preload)
  video?.addEventListener('seeking', preload)
  signal.addEventListener('abort', () => {
    video?.removeEventListener('timeupdate', preload)
    video?.removeEventListener('seeking', preload)
  }, { once: true })
  return trace
}
