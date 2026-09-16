export interface TracePath {
  id: number
  group: number
  closed: boolean
  points: Float32Array
}

export interface CharacterTraceAsset {
  version: 1
  coordinateEncoding: 'pixel-delta-i16'
  source: { file: string; sha256: string; width: number; height: number; duration: number; frameRate: number }
  target: { file: string; sha256: string; width: number; height: number; paths: Array<Omit<TracePath, 'points'> & { points: number[] }> }
  annotationSha256: string
  groups: string[]
  payload: string
  payloadSha256: string
  decodedSha256: string
  decodedBytes: number
  frameTimes: number[]
  frameOffsets: number[]
}

export function frameAtTime(times: readonly number[], duration: number, mediaTime: number): number {
  const normalized = ((mediaTime % duration) + duration) % duration
  let lower = 0
  let upper = times.length - 1
  while (lower < upper) {
    const middle = Math.ceil((lower + upper) / 2)
    if (times[middle] <= normalized + 0.00001) lower = middle
    else upper = middle - 1
  }
  return lower
}

export class CharacterTrace {
  private readonly bytes: DataView
  private cachedFrame = -1
  private cachedPaths: TracePath[] = []
  readonly targetPaths: TracePath[]

  constructor(readonly manifest: CharacterTraceAsset, payload: ArrayBuffer, readonly targetImage: HTMLImageElement) {
    if (manifest.version !== 1 || manifest.coordinateEncoding !== 'pixel-delta-i16' || payload.byteLength !== manifest.decodedBytes || !manifest.frameTimes.length
      || manifest.frameTimes.length !== manifest.frameOffsets.length || manifest.source.duration <= 0) {
      throw new Error('Invalid character trace asset')
    }
    this.bytes = new DataView(payload)
    this.targetPaths = manifest.target.paths.map((path) => ({ ...path, points: new Float32Array(path.points) }))
    for (let index = 0; index < manifest.frameTimes.length; index++) {
      if (index && (manifest.frameTimes[index] <= manifest.frameTimes[index - 1] || manifest.frameOffsets[index] <= manifest.frameOffsets[index - 1])) {
        throw new Error('Non-monotonic character trace')
      }
      if (manifest.frameOffsets[index] + 4 > payload.byteLength) throw new Error('Truncated character trace')
    }
  }

  sampleCharacterPaths(mediaTime: number): TracePath[] {
    const frame = frameAtTime(this.manifest.frameTimes, this.manifest.source.duration, mediaTime)
    if (frame === this.cachedFrame) return this.cachedPaths
    let offset = this.manifest.frameOffsets[frame]
    const count = this.bytes.getUint32(offset, true)
    offset += 4
    const paths: TracePath[] = []
    const end = this.manifest.frameOffsets[frame + 1] ?? this.bytes.byteLength
    for (let index = 0; index < count; index++) {
      if (offset + 8 > end) throw new Error('Truncated trace path')
      const id = this.bytes.getUint32(offset, true)
      const group = this.bytes.getUint8(offset + 4)
      const closed = Boolean(this.bytes.getUint8(offset + 5))
      const pointCount = this.bytes.getUint16(offset + 6, true)
      offset += 8
      if (group > 3 || pointCount < 2 || offset + pointCount * 4 > end) throw new Error('Invalid trace path')
      const points = new Float32Array(pointCount * 2)
      let horizontal = 0
      let vertical = 0
      for (let point = 0; point < pointCount; point++) {
        horizontal += this.bytes.getInt16(offset + point * 4, true)
        vertical += this.bytes.getInt16(offset + point * 4 + 2, true)
        points[point * 2] = horizontal / this.manifest.source.width
        points[point * 2 + 1] = vertical / this.manifest.source.height
      }
      offset += pointCount * 4
      paths.push({ id, group, closed, points })
    }
    if (offset !== end) throw new Error('Invalid trace frame length')
    this.cachedFrame = frame
    this.cachedPaths = paths
    return paths
  }
}

export function smoothRange(start: number, end: number, value: number): number {
  const progress = Math.max(0, Math.min(1, (value - start) / (end - start)))
  return progress * progress * (3 - 2 * progress)
}

export function interludeAtTime(seconds: number): { opacity: number; blur: number } {
  const arriving = smoothRange(0.35, 0.8, seconds)
  const leaving = smoothRange(4.0, 5.0, seconds)
  return { opacity: arriving * (1 - leaving), blur: (1 - arriving) * 6 + leaving * 12 }
}
