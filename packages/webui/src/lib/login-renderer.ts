import { CharacterTrace, smoothRange, type TracePath } from './character-trace'

export interface Point { horizontal: number; vertical: number }
export interface SceneProjection {
  matrix: DOMMatrix
  width: number
  height: number
  fitWidth: number
  fitHeight: number
  offsetHorizontal: number
  offsetVertical: number
  bounds: DOMRect
  rootBounds: DOMRect
  mobile: boolean
}

export function coverPlacement(sourceWidth: number, sourceHeight: number, width: number, height: number, positionHorizontal: number, positionVertical: number) {
  const scale = Math.max(width / sourceWidth, height / sourceHeight)
  return { fitWidth: sourceWidth * scale, fitHeight: sourceHeight * scale,
    offsetHorizontal: (width - sourceWidth * scale) * positionHorizontal,
    offsetVertical: (height - sourceHeight * scale) * positionVertical }
}

function numbers(value: string): number[] {
  return value.split(' ').map((part) => Number.parseFloat(part) || 0)
}

/** 展开 CSS 值里的 var() 引用（自定义属性的 computed value 保持声明 token，
 * 不会被序列化成 oklch() 等现代颜色语法，可稳定解析）。 */
function expandCssVariables(value: string, depth = 0): string {
  if (depth > 4 || !value.includes('var(')) return value
  const expanded = value.replace(/var\(\s*(--[\w-]+)\s*(?:,\s*([^()]*))?\)/g, (_, name: string, fallback: string) => {
    const resolved = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
    return resolved || fallback.trim()
  })
  return expanded === value ? expanded : expandCssVariables(expanded, depth + 1)
}

function hslToRgb(hue: number, saturation: number, lightness: number): [number, number, number] {
  const sat = saturation / 100
  const light = lightness / 100
  const chroma = (1 - Math.abs(2 * light - 1)) * sat
  const sector = (((hue % 360) + 360) % 360) / 60
  const secondary = chroma * (1 - Math.abs((sector % 2) - 1))
  const [r, g, b] = sector < 1 ? [chroma, secondary, 0] : sector < 2 ? [secondary, chroma, 0]
    : sector < 3 ? [0, chroma, secondary] : sector < 4 ? [0, secondary, chroma]
    : sector < 5 ? [secondary, 0, chroma] : [chroma, 0, secondary]
  const match = light - chroma / 2
  return [Math.round((r + match) * 255), Math.round((g + match) * 255), Math.round((b + match) * 255)]
}

function parseColorChannels(value: string): [number, number, number] | null {
  const trimmed = value.trim()
  const hex = /^#([0-9a-f]{6})$/i.exec(trimmed)
  if (hex) {
    const packed = Number.parseInt(hex[1], 16)
    return [(packed >> 16) & 255, (packed >> 8) & 255, packed & 255]
  }
  const hsl = /^hsl\(\s*([\d.]+)(?:deg)?[\s,]+([\d.]+)%[\s,]+([\d.]+)%\s*\)$/i.exec(trimmed)
  if (hsl) return hslToRgb(Number(hsl[1]), Number(hsl[2]), Number(hsl[3]))
  const rgb = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec(trimmed)
  if (rgb) return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])]
  return null
}

/** 过场水印的渐变端色（0..1）：优先解析 --grad-a/--grad-b 变量链，
 * 失败再回退解析目标元素 backgroundImage 的 rgb() 序列化。 */
function readGradientColors(target: Element): number[][] {
  const styles = getComputedStyle(document.documentElement)
  const colors: number[][] = []
  for (const name of ['--grad-a', '--grad-b']) {
    const channels = parseColorChannels(expandCssVariables(styles.getPropertyValue(name)))
    if (channels) colors.push(channels.map((channel) => channel / 255))
  }
  if (colors.length === 2) return colors
  return Array.from(
    getComputedStyle(target).backgroundImage.matchAll(/rgba?\(([^)]+)\)/g),
    (match) => match[1].split(/[,\s]+/).filter(Boolean).slice(0, 3).map((channel) => Number(channel) / 255),
  )
}

export function readSceneProjection(root: HTMLElement, scene: HTMLElement, camera: HTMLElement, video: HTMLVideoElement): SceneProjection {
  const cameraStyle = getComputedStyle(camera)
  const sceneStyle = getComputedStyle(scene)
  const origin = numbers(cameraStyle.transformOrigin)
  const sceneOrigin = numbers(sceneStyle.transformOrigin)
  const translation = numbers(sceneStyle.translate)
  const scale = Number.parseFloat(cameraStyle.scale) || 1
  const rootBounds = root.getBoundingClientRect()
  const matrix = new DOMMatrix()
    .translate(rootBounds.left + scene.offsetLeft + (translation[0] || 0), rootBounds.top + scene.offsetTop + (translation[1] || 0))
    .translate(sceneOrigin[0], sceneOrigin[1])
    .multiply(new DOMMatrix(sceneStyle.transform === 'none' ? undefined : sceneStyle.transform))
    .translate(-sceneOrigin[0], -sceneOrigin[1])
    .translate(origin[0], origin[1]).scale(scale)
    .multiply(new DOMMatrix(cameraStyle.transform === 'none' ? undefined : cameraStyle.transform))
    .translate(-origin[0], -origin[1])
  const position = numbers(getComputedStyle(video).objectPosition)
  return { matrix, width: camera.offsetWidth, height: camera.offsetHeight,
    ...coverPlacement(video.videoWidth, video.videoHeight, camera.offsetWidth, camera.offsetHeight, position[0] / 100, position[1] / 100),
    bounds: scene.getBoundingClientRect(), rootBounds, mobile: window.innerWidth <= 700 }
}

export function projectVideoPoint(horizontal: number, vertical: number, projection: SceneProjection): Point {
  const localHorizontal = horizontal * projection.fitWidth + projection.offsetHorizontal
  const localVertical = vertical * projection.fitHeight + projection.offsetVertical
  const matrix = projection.matrix
  const weight = matrix.m14 * localHorizontal + matrix.m24 * localVertical + matrix.m44
  return { horizontal: (matrix.m11 * localHorizontal + matrix.m21 * localVertical + matrix.m41) / weight,
    vertical: (matrix.m12 * localHorizontal + matrix.m22 * localVertical + matrix.m42) / weight }
}

export function particlePoint(source: Point, target: Point, progress: number, bend: number): Point {
  const remaining = 1 - progress
  const distance = target.horizontal - source.horizontal
  const first = { horizontal: source.horizontal + distance * 0.28 + bend, vertical: source.vertical - 0.12 }
  const second = { horizontal: target.horizontal - distance * 0.2 - bend, vertical: target.vertical - 0.1 }
  return {
    horizontal: remaining ** 3 * source.horizontal + 3 * remaining ** 2 * progress * first.horizontal + 3 * remaining * progress ** 2 * second.horizontal + progress ** 3 * target.horizontal,
    vertical: remaining ** 3 * source.vertical + 3 * remaining ** 2 * progress * first.vertical + 3 * remaining * progress ** 2 * second.vertical + progress ** 3 * target.vertical,
  }
}

export function clipTraceSegment(from: Point, to: Point, left: number, top: number, right: number, bottom: number): { from: Point; to: Point } | null {
  const horizontal = to.horizontal - from.horizontal
  const vertical = to.vertical - from.vertical
  let start = 0
  let end = 1
  for (const [direction, distance] of [[-horizontal, from.horizontal - left], [horizontal, right - from.horizontal], [-vertical, from.vertical - top], [vertical, bottom - from.vertical]]) {
    if (direction === 0) { if (distance < 0) return null; continue }
    const fraction = distance / direction
    if (direction < 0) start = Math.max(start, fraction)
    else end = Math.min(end, fraction)
    if (start > end) return null
  }
  return { from: { horizontal: from.horizontal + start * horizontal, vertical: from.vertical + start * vertical },
    to: { horizontal: from.horizontal + end * horizontal, vertical: from.vertical + end * vertical } }
}

const TEXTURE_VERTEX = `#version 300 es
in vec4 position;
in vec2 texturePoint;
out vec2 textureUV;
void main() { gl_Position = position; textureUV = texturePoint; }
`
const TEXTURE_FRAGMENT = `#version 300 es
precision highp float;
uniform sampler2D image;
uniform vec2 viewport;
uniform vec2 resolution;
uniform vec4 sceneBounds;
uniform vec4 rootBounds;
uniform float dark;
uniform float mobile;
uniform float compact;
uniform float opacity;
uniform float targetMode;
uniform float desaturate;
uniform vec3 gradientStart;
uniform vec3 gradientEnd;
uniform vec2 targetSize;
in vec2 textureUV;
out vec4 outputColor;
float ramp(float start, float end, float value) { return clamp((value-start)/(end-start),0.0,1.0); }
void main() {
  vec4 sampleColor = texture(image, textureUV);
  vec2 screen = vec2(gl_FragCoord.x/resolution.x, 1.0-gl_FragCoord.y/resolution.y)*viewport;
  if (targetMode > 0.5) {
    float gradientPosition = dot((textureUV-0.5)*targetSize,vec2(0.1391731,0.9902681))/dot(targetSize,vec2(0.1391731,0.9902681))+0.5;
    float alpha = sampleColor.a*opacity;
    outputColor = vec4(mix(gradientStart,gradientEnd,ramp(0.08,0.92,gradientPosition))*alpha,alpha);
    return;
  }
  if (screen.x < sceneBounds.x || screen.x > sceneBounds.z || screen.y < sceneBounds.y || screen.y > sceneBounds.w) discard;
  float horizontal = (screen.x-rootBounds.x)/rootBounds.z;
  float vertical = (screen.y-rootBounds.y)/rootBounds.w;
  float wash;
  float veil;
  float edge = 1.0;
  if (mobile > 0.5) {
    wash = mix(0.03,0.04,ramp(0.15,0.28,vertical));
    wash = mix(wash,0.8,ramp(0.28,0.43,vertical));
    wash = mix(wash,1.0,ramp(0.43,0.57,vertical));
    veil = mix(0.7,0.6,dark)*(1.0-ramp(0.0,0.15,vertical));
  } else {
    wash = mix(mix(0.03,0.04,dark),mix(0.14,0.16,dark),ramp(0.20,0.43,horizontal));
    wash = mix(wash,mix(0.91,0.94,dark),ramp(0.43,0.66,horizontal));
    wash = mix(wash,1.0,ramp(0.66,1.0,horizontal));
    if (compact > 0.5 && dark < 0.5) {
      wash = mix(0.0,0.3,ramp(0.10,0.40,horizontal));
      wash = mix(wash,0.94,ramp(0.40,0.66,horizontal));
      wash = mix(wash,1.0,ramp(0.66,1.0,horizontal));
    }
    veil = mix(0.7,0.55,dark)*(1.0-ramp(0.0,0.18,vertical))+mix(0.9,0.8,dark)*ramp(0.8,1.0,vertical);
    edge = 1.0-ramp(0.65,1.0,(screen.x-sceneBounds.x)/(sceneBounds.z-sceneBounds.x));
  }
  float alpha = opacity*edge*(1.0-wash)*(1.0-veil);
  float gray = dot(sampleColor.rgb,vec3(0.2126,0.7152,0.0722));
  outputColor = vec4(mix(sampleColor.rgb,vec3(gray),desaturate)*alpha,alpha);
}
`
const STROKE_VERTEX = `#version 300 es
in vec4 segment;
in vec4 style;
in vec3 color;
uniform vec2 viewport;
out vec2 localUV;
out vec4 strokeColor;
out float particle;
out float clipped;
const vec2 corners[6] = vec2[6](vec2(0.,-1.),vec2(1.,-1.),vec2(0.,1.),vec2(0.,1.),vec2(1.,-1.),vec2(1.,1.));
void main() {
  vec2 corner = corners[gl_VertexID];
  vec2 direction = segment.zw-segment.xy;
  vec2 normal = length(direction)>0.01 ? normalize(vec2(-direction.y,direction.x)) : vec2(0.,1.);
  vec2 point = mix(segment.xy,segment.zw,corner.x)+normal*corner.y*style.x;
  if (style.z>0.5) point = segment.xy+vec2(corner.x*2.0-1.0,corner.y)*style.x;
  gl_Position = vec4(point.x/viewport.x*2.0-1.0,1.0-point.y/viewport.y*2.0,0.,1.);
  localUV = vec2(corner.x*2.0-1.0,corner.y);
  strokeColor = vec4(color,style.y);
  particle = style.z;
  clipped = style.w;
}
`
const STROKE_FRAGMENT = `#version 300 es
precision highp float;
uniform vec2 viewport;
uniform vec2 resolution;
uniform vec4 sceneBounds;
in vec2 localUV;
in vec4 strokeColor;
in float particle;
in float clipped;
out vec4 outputColor;
void main() {
  vec2 screen = vec2(gl_FragCoord.x/resolution.x,1.0-gl_FragCoord.y/resolution.y)*viewport;
  if (clipped>0.5 && (screen.x<sceneBounds.x || screen.x>sceneBounds.z || screen.y<sceneBounds.y || screen.y>sceneBounds.w)) discard;
  float softness = particle>0.5 ? exp(-dot(localUV,localUV)*3.0) : 1.0-smoothstep(0.25,1.0,abs(localUV.y));
  float alpha = strokeColor.a*softness;
  outputColor = vec4(strokeColor.rgb*alpha,alpha);
}
`

function randomUnit(seed: number): number {
  let state = Math.imul(seed ^ 0x9e3779b9, 0x85ebca6b)
  state = Math.imul(state ^ state >>> 13, 0xc2b2ae35)
  return ((state ^ state >>> 16) >>> 0) / 4294967296
}

function createProgram(context: WebGL2RenderingContext, vertex: string, fragment: string) {
  const program = context.createProgram()
  if (!program) throw new Error('WebGL program unavailable')
  let linked = false
  try {
    for (const [type, source] of [[context.VERTEX_SHADER, vertex], [context.FRAGMENT_SHADER, fragment]] as const) {
      const shader = context.createShader(type)
      if (!shader) throw new Error('WebGL shader unavailable')
      context.shaderSource(shader, source)
      context.compileShader(shader)
      if (!context.getShaderParameter(shader, context.COMPILE_STATUS)) {
        const message = context.getShaderInfoLog(shader) || 'Shader compilation failed'
        context.deleteShader(shader)
        throw new Error(message)
      }
      context.attachShader(program, shader)
      context.deleteShader(shader)
    }
    context.linkProgram(program)
    if (!context.getProgramParameter(program, context.LINK_STATUS)) throw new Error('WebGL linking failed')
    linked = true
  } finally {
    if (!linked) context.deleteProgram(program)
  }
  const locations = new Map<string, WebGLUniformLocation | null>()
  return { program, uniform: (name: string) => {
    if (!locations.has(name)) locations.set(name, context.getUniformLocation(program, name))
    return locations.get(name) ?? null
  } }
}

function resourceScope() {
  const releases: Array<() => void> = []
  return {
    own<Resource>(resource: Resource | null, release: (value: Resource) => void): Resource {
      if (!resource) throw new Error('WebGL resource unavailable')
      releases.push(() => release(resource))
      return resource
    },
    defer(release: () => void) { releases.push(release) },
    dispose() { releases.splice(0).reverse().forEach((release) => release()) },
  }
}

interface Flight {
  group: number
  target: Point
  sourceFraction: number
  birth: number
  arrival: number
  seed: number
  source?: Point
  started?: number
}

function targetFlights(paths: TracePath[], count: number, width: number, height: number): Flight[] {
  const segments: Array<{ from: Point; to: Point; group: number; length: number }> = []
  let total = 0
  for (const path of paths) {
    const pairs = path.points.length / 2
    for (let index = 0; index < pairs - (path.closed ? 0 : 1); index++) {
      const next = (index + 1) % pairs
      const from = { horizontal: path.points[index * 2], vertical: path.points[index * 2 + 1] }
      const to = { horizontal: path.points[next * 2], vertical: path.points[next * 2 + 1] }
      const length = Math.hypot((to.horizontal - from.horizontal) * width, (to.vertical - from.vertical) * height)
      segments.push({ from, to, group: path.group, length })
      total += length
    }
  }
  const flights: Flight[] = []
  let segmentIndex = 0
  let distanceBefore = 0
  for (let index = 0; index < count; index++) {
    const distance = total * (index + 0.5) / count
    while (segmentIndex < segments.length - 1 && distanceBefore + segments[segmentIndex].length < distance) distanceBefore += segments[segmentIndex++].length
    const segment = segments[segmentIndex]
    if (!segment) continue
    const fraction = (distance - distanceBefore) / Math.max(segment.length, 0.0001)
    const rank = index / Math.max(1, count - 1)
    const birth = 0.875 + rank * 2.25
    flights.push({ group: segment.group, target: {
      horizontal: segment.from.horizontal + (segment.to.horizontal - segment.from.horizontal) * fraction,
      vertical: segment.from.vertical + (segment.to.vertical - segment.from.vertical) * fraction,
    }, sourceFraction: 0, birth, arrival: 3.19 + rank * 1.81, seed: index + 1 })
  }
  for (let group = 0; group < 4; group++) {
    const members = flights.filter((flight) => flight.group === group)
    members.forEach((flight, index) => { flight.sourceFraction = (index + 0.5) / members.length })
  }
  if (flights.length) flights[flights.length - 1].arrival = 5
  return flights
}

interface TransitionOptions {
  canvas: HTMLCanvasElement
  root: HTMLElement
  scene: HTMLElement
  camera: HTMLElement
  video: HTMLVideoElement
  target: HTMLElement
  trace: CharacterTrace
  onReady: () => void
  onFrame: (seconds: number) => void
  onFinish: (animated: boolean) => void
}

export function runLoginTransition(options: TransitionOptions): () => void {
  const { canvas, video, trace } = options
  const context = canvas.getContext('webgl2', { alpha: true, premultipliedAlpha: true, antialias: false, powerPreference: 'low-power' })
  if (!context || typeof video.requestVideoFrameCallback !== 'function') throw new Error('Synchronized renderer unavailable')
  if (video.videoWidth !== trace.manifest.source.width || video.videoHeight !== trace.manifest.source.height
    || Math.abs(video.duration - trace.manifest.source.duration) > 0.04) throw new Error('Video and trace do not match')
  const resources = resourceScope()
  try {
    startTransition(options, context, resources)
    return () => resources.dispose()
  } catch (error) {
    resources.dispose()
    throw error
  }
}

function startTransition(options: TransitionOptions, context: WebGL2RenderingContext, resources: ReturnType<typeof resourceScope>): void {
  const { canvas, root, scene, camera, video, target, trace } = options
  const textureProgram = createProgram(context, TEXTURE_VERTEX, TEXTURE_FRAGMENT)
  resources.own(textureProgram.program, (program) => context.deleteProgram(program))
  const strokeProgram = createProgram(context, STROKE_VERTEX, STROKE_FRAGMENT)
  resources.own(strokeProgram.program, (program) => context.deleteProgram(program))
  const quadBuffer = resources.own(context.createBuffer(), (buffer) => context.deleteBuffer(buffer))
  const strokeBuffer = resources.own(context.createBuffer(), (buffer) => context.deleteBuffer(buffer))
  const quadArray = resources.own(context.createVertexArray(), (array) => context.deleteVertexArray(array))
  const strokeArray = resources.own(context.createVertexArray(), (array) => context.deleteVertexArray(array))
  const videoTexture = resources.own(context.createTexture(), (texture) => context.deleteTexture(texture))
  const targetTexture = resources.own(context.createTexture(), (texture) => context.deleteTexture(texture))
  for (const texture of [videoTexture, targetTexture]) {
    context.bindTexture(context.TEXTURE_2D, texture)
    context.texParameteri(context.TEXTURE_2D, context.TEXTURE_MIN_FILTER, context.LINEAR)
    context.texParameteri(context.TEXTURE_2D, context.TEXTURE_MAG_FILTER, context.LINEAR)
    context.texParameteri(context.TEXTURE_2D, context.TEXTURE_WRAP_S, context.CLAMP_TO_EDGE)
    context.texParameteri(context.TEXTURE_2D, context.TEXTURE_WRAP_T, context.CLAMP_TO_EDGE)
  }
  context.bindTexture(context.TEXTURE_2D, targetTexture)
  context.texImage2D(context.TEXTURE_2D, 0, context.RGBA, context.RGBA, context.UNSIGNED_BYTE, trace.targetImage)
  context.bindVertexArray(quadArray)
  context.bindBuffer(context.ARRAY_BUFFER, quadBuffer)
  for (const [name, size, offset] of [['position', 4, 0], ['texturePoint', 2, 16]] as const) {
    const location = context.getAttribLocation(textureProgram.program, name)
    context.enableVertexAttribArray(location)
    context.vertexAttribPointer(location, size, context.FLOAT, false, 24, offset)
  }
  context.bindVertexArray(strokeArray)
  context.bindBuffer(context.ARRAY_BUFFER, strokeBuffer)
  for (const [name, size, offset] of [['segment', 4, 0], ['style', 4, 16], ['color', 3, 32]] as const) {
    const location = context.getAttribLocation(strokeProgram.program, name)
    context.enableVertexAttribArray(location)
    context.vertexAttribPointer(location, size, context.FLOAT, false, 44, offset)
    context.vertexAttribDivisor(location, 1)
  }
  context.enable(context.BLEND)
  context.blendFunc(context.ONE, context.ONE_MINUS_SRC_ALPHA)
  if (context.getError() !== context.NO_ERROR) throw new Error('WebGL initialization failed')
  const flights = targetFlights(trace.targetPaths, window.innerWidth <= 700 ? 650 : 1600, trace.manifest.target.width, trace.manifest.target.height)
  const flightGroups = [0, 1, 2, 3].map((group) => flights.filter((flight) => flight.group === group))
  let animation = 0
  let videoCallback = 0
  let started: number | undefined
  let lastVideoFrame = performance.now()
  let paths: TracePath[] = []
  let disposed = false
  let finished = false
  let ready = false
  let mediaTime = 0
  let maxFrameDrift = 0
  let lastDrawTime: number | undefined
  const watchdog = window.setTimeout(() => finish(false), 6500)
  const finish = (animated: boolean) => {
    if (finished || disposed) return
    finished = true
    options.onFinish(animated)
  }
  const lost = (event: Event) => { event.preventDefault(); finish(false) }
  resources.defer(() => {
    disposed = true
    clearTimeout(watchdog)
    cancelAnimationFrame(animation)
    video.cancelVideoFrameCallback(videoCallback)
    canvas.removeEventListener('webglcontextlost', lost)
  })
  canvas.addEventListener('webglcontextlost', lost)
  const updateVideo: VideoFrameRequestCallback = (_time, metadata) => {
    if (disposed || finished) return
    let snapshot: VideoFrame | undefined
    try {
      if (typeof VideoFrame === 'function') snapshot = new VideoFrame(video)
      else if (performance.now() - metadata.expectedDisplayTime > 500 / trace.manifest.source.frameRate) {
        videoCallback = video.requestVideoFrameCallback(updateVideo)
        return
      }
      mediaTime = snapshot ? snapshot.timestamp / 1000000 : metadata.mediaTime
      const timeDifference = Math.abs(mediaTime - metadata.mediaTime)
      maxFrameDrift = Math.max(maxFrameDrift, Math.min(timeDifference, Math.abs(trace.manifest.source.duration - timeDifference)) * 1000)
      context.bindTexture(context.TEXTURE_2D, videoTexture)
      context.texImage2D(context.TEXTURE_2D, 0, context.RGBA, context.RGBA, context.UNSIGNED_BYTE, snapshot ?? video)
      paths = trace.sampleCharacterPaths(mediaTime)
      lastVideoFrame = performance.now()
      if (started === undefined) started = lastVideoFrame
      videoCallback = video.requestVideoFrameCallback(updateVideo)
    } catch { finish(false) }
    finally { snapshot?.close() }
  }
  videoCallback = video.requestVideoFrameCallback(updateVideo)

  const render = (now: number) => {
    if (disposed || finished) return
    if (document.hidden || now - lastVideoFrame > 900) { finish(false); return }
    if (started === undefined) { animation = requestAnimationFrame(render); return }
    const interval = 1000 / (window.innerWidth <= 700 ? 30 : 60)
    if (lastDrawTime !== undefined && now - lastDrawTime < interval && now - started < 5000) {
      animation = requestAnimationFrame(render)
      return
    }
    lastDrawTime = lastDrawTime === undefined ? now : now - (now - lastDrawTime) % interval
    try {
      const seconds = Math.max(0, Math.min(5, (now - started) / 1000))
      const width = window.innerWidth
      const height = window.innerHeight
      const ratio = Math.min(devicePixelRatio || 1, width <= 700 ? 1.5 : 2)
      if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
        canvas.width = Math.round(width * ratio)
        canvas.height = Math.round(height * ratio)
        context.viewport(0, 0, canvas.width, canvas.height)
      }
      const projection = readSceneProjection(root, scene, camera, video)
      const targetBounds = target.getBoundingClientRect()
      const gradientColors = readGradientColors(target)
      const dark = document.documentElement.classList.contains('dark')
      const targetReveal = smoothRange(3.125, 5, seconds)
      context.clearColor(0, 0, 0, 0)
      context.clear(context.COLOR_BUFFER_BIT)
      context.useProgram(textureProgram.program)
      context.bindVertexArray(quadArray)
      context.uniform2f(textureProgram.uniform('viewport'), width, height)
      context.uniform2f(textureProgram.uniform('resolution'), canvas.width, canvas.height)
      context.uniform4f(textureProgram.uniform('sceneBounds'), projection.bounds.left, projection.bounds.top, projection.bounds.right, projection.bounds.bottom)
      context.uniform4f(textureProgram.uniform('rootBounds'), projection.rootBounds.left, projection.rootBounds.top, projection.rootBounds.width, projection.rootBounds.height)
      context.uniform1f(textureProgram.uniform('dark'), dark ? 1 : 0)
      context.uniform1f(textureProgram.uniform('mobile'), projection.mobile ? 1 : 0)
      context.uniform1f(textureProgram.uniform('compact'), width <= 1100 ? 1 : 0)
      context.uniform2f(textureProgram.uniform('targetSize'), targetBounds.width, targetBounds.height)
      context.uniform3fv(textureProgram.uniform('gradientStart'), gradientColors[0] ?? [0.76, 0.4, 0.59])
      context.uniform3fv(textureProgram.uniform('gradientEnd'), gradientColors[1] ?? [0.96, 0.25, 0.52])
      const quad: number[] = []
      for (const [horizontal, vertical] of [[0, 0], [1, 0], [0, 1], [0, 1], [1, 0], [1, 1]]) {
        const point = projection.matrix.transformPoint(new DOMPoint(horizontal * projection.width, vertical * projection.height))
        quad.push(point.x * 2 / width - point.w, point.w - point.y * 2 / height, 0, point.w,
          (horizontal * projection.width - projection.offsetHorizontal) / projection.fitWidth,
          (vertical * projection.height - projection.offsetVertical) / projection.fitHeight)
      }
      context.bindBuffer(context.ARRAY_BUFFER, quadBuffer)
      context.bufferData(context.ARRAY_BUFFER, new Float32Array(quad), context.DYNAMIC_DRAW)
      context.bindTexture(context.TEXTURE_2D, videoTexture)
      context.uniform1f(textureProgram.uniform('targetMode'), 0)
      context.uniform1f(textureProgram.uniform('opacity'), (1 - smoothRange(0.875, 5, seconds)) * Number(getComputedStyle(scene).opacity))
      context.uniform1f(textureProgram.uniform('desaturate'), smoothRange(0.875, 3.75, seconds) * 0.75)
      context.drawArrays(context.TRIANGLES, 0, 6)
      quad.length = 0
      for (const [horizontal, vertical] of [[0, 0], [1, 0], [0, 1], [0, 1], [1, 0], [1, 1]]) {
        quad.push((targetBounds.left + horizontal * targetBounds.width) * 2 / width - 1,
          1 - (targetBounds.top + vertical * targetBounds.height) * 2 / height, 0, 1, horizontal, vertical)
      }
      context.bufferData(context.ARRAY_BUFFER, new Float32Array(quad), context.DYNAMIC_DRAW)
      context.bindTexture(context.TEXTURE_2D, targetTexture)
      context.uniform1f(textureProgram.uniform('targetMode'), 1)
      context.uniform1f(textureProgram.uniform('opacity'), targetReveal * (dark ? 0.4 : 0.45))
      context.drawArrays(context.TRIANGLES, 0, 6)

      const strokes: number[] = []
      const grouped: Array<Array<{ from: Point; to: Point; length: number; end: number }>> = [[], [], [], []]
      const groupLengths = [0, 0, 0, 0]
      for (const path of paths) {
        const pairs = path.points.length / 2
        let from = projectVideoPoint(path.points[0], path.points[1], projection)
        for (let index = 1; index < pairs + (path.closed ? 1 : 0); index++) {
          const next = index % pairs
          const to = projectVideoPoint(path.points[next * 2], path.points[next * 2 + 1], projection)
          const clipped = clipTraceSegment(from, to, Math.max(0, projection.bounds.left), Math.max(0, projection.bounds.top), Math.min(width, projection.bounds.right), Math.min(height, projection.bounds.bottom))
          if (clipped) {
            const length = Math.hypot(clipped.to.horizontal - clipped.from.horizontal, clipped.to.vertical - clipped.from.vertical)
            groupLengths[path.group] += length
            grouped[path.group].push({ ...clipped, length, end: groupLengths[path.group] })
          }
          from = to
        }
      }
      for (let group = 0; group < 4; group++) {
        const members = flightGroups[group]
        for (const segment of grouped[group]) {
          const fraction = (segment.end - segment.length / 2) / Math.max(0.001, groupLengths[group])
          const birth = members[Math.min(members.length - 1, Math.floor(fraction * members.length))]?.birth ?? 2.5
          const opacity = smoothRange(0, 0.7, seconds) * (1 - smoothRange(birth - 0.04, birth + 0.08, seconds))
          if (opacity > 0.001) {
            // 线稿色同一份在暗底上对比度是亮底的 ~7 倍(11.4:1 vs 1.56:1),
            // 夜间整体压暗到 ~0.65 使两主题的感知强度对齐(约 5:1)。
            const stroke = dark ? 0.65 : 1
            strokes.push(segment.from.horizontal, segment.from.vertical, segment.to.horizontal, segment.to.vertical,
              1.35, opacity * (dark ? 0.7 : 0.9), 0, 1, stroke, 0.72 * stroke, 0.89 * stroke)
          }
        }
      }
      let activeParticles = 0
      for (const flight of flights) {
        if (seconds < flight.birth) continue
        if (!flight.source) {
          const segments = grouped[flight.group]
          if (!segments.length) continue
          const distance = groupLengths[flight.group] * flight.sourceFraction
          let lower = 0
          let upper = segments.length - 1
          while (lower < upper) {
            const middle = Math.floor((lower + upper) / 2)
            if (segments[middle].end < distance) lower = middle + 1
            else upper = middle
          }
          const segment = segments[lower]
          const fraction = segment.length ? (distance - segment.end + segment.length) / segment.length : 0
          flight.source = { horizontal: (segment.from.horizontal + (segment.to.horizontal - segment.from.horizontal) * fraction) / width,
            vertical: (segment.from.vertical + (segment.to.vertical - segment.from.vertical) * fraction) / height }
          flight.started = seconds
        }
        const destination = { horizontal: (targetBounds.left + flight.target.horizontal * targetBounds.width) / width,
          vertical: (targetBounds.top + flight.target.vertical * targetBounds.height) / height }
        const progress = smoothRange(flight.started ?? flight.birth, flight.arrival, seconds)
        const point = particlePoint(flight.source, destination, progress, (randomUnit(flight.seed + 91) - 0.5) * 0.075)
        const opacity = (1 - smoothRange(flight.arrival - 0.08, flight.arrival, seconds)) * Math.min(1, (seconds - (flight.started ?? 0)) * 8)
        if (opacity <= 0.001) continue
        activeParticles++
        strokes.push(point.horizontal * width, point.vertical * height, point.horizontal * width, point.vertical * height,
          2 + randomUnit(flight.seed) * 2.8, opacity * (dark ? 0.75 : 1), 1, 0, 1, dark ? 0.55 : 0.67, 0.89)
      }
      context.useProgram(strokeProgram.program)
      context.bindVertexArray(strokeArray)
      context.bindBuffer(context.ARRAY_BUFFER, strokeBuffer)
      context.bufferData(context.ARRAY_BUFFER, new Float32Array(strokes), context.DYNAMIC_DRAW)
      context.uniform2f(strokeProgram.uniform('viewport'), width, height)
      context.uniform2f(strokeProgram.uniform('resolution'), canvas.width, canvas.height)
      context.uniform4f(strokeProgram.uniform('sceneBounds'), projection.bounds.left, projection.bounds.top, projection.bounds.right, projection.bounds.bottom)
      context.drawArraysInstanced(context.TRIANGLES, 0, 6, strokes.length / 11)
      if (!ready) { ready = true; options.onReady() }
      options.onFrame(seconds)
      if (import.meta.env.DEV) {
        canvas.dataset.mediaTime = mediaTime.toFixed(6)
        canvas.dataset.elapsed = seconds.toFixed(4)
        canvas.dataset.paths = String(paths.length)
        canvas.dataset.activeParticles = String(activeParticles)
        canvas.dataset.maxFrameDrift = maxFrameDrift.toFixed(3)
      }
      if (seconds >= 5) animation = requestAnimationFrame(() => finish(true))
      else animation = requestAnimationFrame(render)
    } catch { finish(false) }
  }
  animation = requestAnimationFrame(render)
}
