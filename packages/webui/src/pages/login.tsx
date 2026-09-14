import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ArrowRight, Eye, EyeOff, Loader2 } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { ThemeToggle } from '@/components/theme-toggle'
import { useTheme } from '@/lib/theme'
import { setToken, ARRIVED_FROM_LOGIN_KEY } from '@/lib/auth'
import { verifyToken } from '@/lib/api'
import '@fontsource/fraunces/600-italic.css'
import './login.css'

const MEDIA_BASE = `${import.meta.env.BASE_URL}assets/`

/**
 * 登录页「花庭」：人像视频场景 + 右侧下划线令牌表单。
 * 视频仅在省流关闭、
 * 未开启减动效、页面可见时加载，海报图作为常驻兜底。
 */
export function LoginPage() {
  const { theme } = useTheme()
  const [value, setValue] = useState('')
  const [visible, setVisible] = useState(false)
  const [error, setError] = useState('')
  const [phase, setPhase] = useState<'login' | 'entering'>('login')
  const [bloom, setBloom] = useState(false)
  const [verifying, setVerifying] = useState(false)

  const [reduced, setReduced] = useState(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches)
  const [saveData, setSaveData] = useState(
    () => Boolean((navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData),
  )
  const [autoplayBlocked, setAutoplayBlocked] = useState(false)
  const [pageHidden, setPageHidden] = useState(document.hidden)
  const [videoReady, setVideoReady] = useState(false)
  const [videoFailed, setVideoFailed] = useState(false)
  const [requestedVideo, setRequestedVideo] = useState(false)
  const videoMoving = !autoplayBlocked && !saveData && !reduced && !pageHidden && !videoFailed

  const rootRef = useRef<HTMLElement>(null)
  const videoRef = useRef<HTMLVideoElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const petalsRef = useRef<HTMLDivElement>(null)
  const settleTimer = useRef<number | undefined>(undefined)

  useEffect(() => {
    document.title = '登录控制台 · Elysia API'
    return () => window.clearTimeout(settleTimer.current)
  }, [])

  useEffect(() => {
    if (error && !verifying) inputRef.current?.focus()
  }, [error, verifying])

  // 移动端浏览器地址栏颜色跟随花庭底色；离开登录页即移除，交还控制台默认。
  useEffect(() => {
    let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    if (!meta) {
      meta = document.createElement('meta')
      meta.name = 'theme-color'
      document.head.appendChild(meta)
    }
    meta.content = theme === 'dark' ? '#18141c' : '#fdfbfc'
    return () => meta.remove()
  }, [theme])

  useEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)')
    const onMotion = () => setReduced(media.matches)
    const onVisibility = () => setPageHidden(document.hidden)
    const onConnection = () =>
      setSaveData(Boolean((navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData))
    media.addEventListener('change', onMotion)
    document.addEventListener('visibilitychange', onVisibility)
    // saveData 用户中途切换网络时恢复视频。
    ;(navigator as Navigator & { connection?: EventTarget }).connection?.addEventListener?.('change', onConnection)
    return () => {
      media.removeEventListener('change', onMotion)
      document.removeEventListener('visibilitychange', onVisibility)
      ;(navigator as Navigator & { connection?: EventTarget }).connection?.removeEventListener?.('change', onConnection)
    }
  }, [])

  useEffect(() => {
    if (videoMoving) setRequestedVideo(true)
    const video = videoRef.current
    if (!video) return
    if (videoMoving && requestedVideo) {
      void video.play().catch((err: DOMException) => {
        // 自动播放被浏览器拒绝时降级为海报静帧，不报错。
        if (err.name === 'NotAllowedError') setAutoplayBlocked(true)
      })
    } else {
      video.pause()
    }
  }, [videoMoving, requestedVideo])

  // 指针视差：场景随指针平移并带一点 3D 倾斜（触屏与减动效下关闭）。
  useEffect(() => {
    if (reduced || window.matchMedia('(pointer: coarse)').matches) return
    const root = rootRef.current
    if (!root) return
    let targetX = 0
    let targetY = 0
    let x = 0
    let y = 0
    let raf = 0
    const tick = () => {
      x += (targetX - x) * 0.06
      y += (targetY - y) * 0.06
      root.style.setProperty('--par-x', x.toFixed(4))
      root.style.setProperty('--par-y', y.toFixed(4))
      raf = Math.abs(targetX - x) > 0.002 || Math.abs(targetY - y) > 0.002 ? requestAnimationFrame(tick) : 0
    }
    const onMove = (event: PointerEvent) => {
      targetX = (event.clientX / window.innerWidth) * 2 - 1
      targetY = (event.clientY / window.innerHeight) * 2 - 1
      if (!raf) raf = requestAnimationFrame(tick)
    }
    const onLeave = () => {
      targetX = 0
      targetY = 0
      if (!raf) raf = requestAnimationFrame(tick)
    }
    window.addEventListener('pointermove', onMove)
    document.documentElement.addEventListener('pointerleave', onLeave)
    return () => {
      window.removeEventListener('pointermove', onMove)
      document.documentElement.removeEventListener('pointerleave', onLeave)
      if (raf) cancelAnimationFrame(raf)
      root.style.removeProperty('--par-x')
      root.style.removeProperty('--par-y')
    }
  }, [reduced])

  // 落英归位：引言浮现之后，褪色的人像化作花瓣，自画面人物处缓缓飘出，
  // 朝着右上角线稿的画心落去——花落之处，正是水印驻留的位置。
  // 花瓣用 WAAPI 即抛即毁，延迟对齐引言的散场时刻。
  useEffect(() => {
    if (phase !== 'entering' || reduced) return
    const host = petalsRef.current
    if (!host) return
    const vw = window.innerWidth
    const vh = window.innerHeight
    // 起点：视频人物的画面位置；落点：线稿水印的画心（右上角区域）。
    const origin = { x: Math.min(vw * 0.4, 620), y: vh * 0.45 }
    const target = { x: vw - Math.min(vw * 0.2, 280), y: vh * 0.28 }
    const petals: HTMLSpanElement[] = []
    for (let i = 0; i < 16; i++) {
      const petal = document.createElement('span')
      petal.className = 'garden-petal'
      const size = 7 + Math.random() * 7
      petal.style.width = `${size}px`
      petal.style.height = `${size * 1.3}px`
      const sx = origin.x + (Math.random() - 0.5) * 300
      const sy = origin.y + (Math.random() - 0.5) * 320
      petal.style.left = `${sx}px`
      petal.style.top = `${sy}px`
      host.appendChild(petal)
      petals.push(petal)
      const tx = target.x - sx + (Math.random() - 0.5) * 150
      const ty = target.y - sy + (Math.random() - 0.5) * 130
      const sway = 30 + Math.random() * 54
      const lift = 26 + Math.random() * 70
      // 约三成花瓣做前景虚化（景深），其余清晰小巧
      if (Math.random() < 0.35) petal.style.filter = 'blur(1.4px)'
      petal.animate(
        [
          { transform: 'translate(0, 0) rotate(0deg) scale(1)', opacity: 0 },
          { transform: `translate(${tx * 0.18}px, ${ty * 0.12 - lift}px) rotate(55deg)`, opacity: 0.9, offset: 0.28 },
          { transform: `translate(${tx * 0.42 - sway}px, ${ty * 0.38}px) rotate(125deg) scale(0.92)`, opacity: 0.72, offset: 0.54 },
          { transform: `translate(${tx * 0.74 + sway * 0.5}px, ${ty * 0.76}px) rotate(205deg) scale(0.8)`, opacity: 0.55, offset: 0.8 },
          { transform: `translate(${tx}px, ${ty}px) rotate(285deg) scale(0.55)`, opacity: 0 },
        ],
        { duration: 1400 + Math.random() * 500, delay: 1500 + Math.random() * 300, easing: 'cubic-bezier(0.45, 0.05, 0.35, 0.95)', fill: 'both' },
      )
    }
    return () => petals.forEach((petal) => petal.remove())
  }, [phase, reduced])

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (phase !== 'login' || verifying) return
    const token = value.trim()
    if (!token) {
      setError('请输入访问令牌')
      inputRef.current?.focus()
      return
    }
    setError('')

    // 先静默验证：失败直接提示，不打扰花庭；成功才绽开晶辉、播放过场。
    setVerifying(true)
    let valid = false
    let failure = ''
    try {
      valid = await verifyToken(token)
    } catch (err) {
      failure = (err as Error).message || '无法连接到后端'
    }
    setVerifying(false)
    if (!valid || failure) {
      setError(failure || 'Token 无效，请确认与后端 config.json 中的 panelAccessToken 一致')
      return
    }

    if (!reduced) setBloom(true)
    setPhase('entering')
    // 光影退行的完整呼吸：视频褪色 + 引言浮现散去 + 花瓣归位，随后交棒。
    settleTimer.current = window.setTimeout(() => {
      // 通知总览的 ElysiaStage 播放入画收尾（读后即删）。
      try {
        sessionStorage.setItem(ARRIVED_FROM_LOGIN_KEY, '1')
      } catch {
        /* 无存储能力时安静跳过 */
      }
      setToken(token)
    }, reduced ? 100 : 3500)
  }

  return (
    <main ref={rootRef} className="garden" data-phase={phase} data-motion={videoMoving ? 'playing' : 'paused'}>
      <div className="garden-scene" aria-hidden="true">
        <div className="garden-camera">
          <img className="garden-image" src={`${MEDIA_BASE}elysia-login-poster.jpg`} alt="" />
          <video
            ref={videoRef}
            className={`garden-video ${videoReady && !videoFailed && !reduced ? 'is-ready' : ''}`}
            src={requestedVideo && !reduced ? `${MEDIA_BASE}elysia-login.mp4` : undefined}
            muted
            loop
            playsInline
            preload="none"
            onPlaying={() => setVideoReady(true)}
            onError={() => setVideoFailed(true)}
          />
        </div>
      </div>
      <div className="garden-wash" aria-hidden="true" />
      <div className="garden-aurora" aria-hidden="true" />
      <div className="garden-transition" aria-hidden="true" />

      <header className="garden-header">
        <div className="garden-brand" aria-label="Elysia API 控制台">
          <img src={`${import.meta.env.BASE_URL}logo-color.png`} alt="" width={32} height={32} />
          <span>Elysia API</span>
          <span className="garden-brand-divider" />
          <span className="garden-console">Console</span>
        </div>
        <ThemeToggle />
      </header>

      <section className="garden-content" aria-label="登录控制台">
        <div className="garden-login" aria-hidden={phase !== 'login'}>
          <h1>
            Elysia <i>API</i>
            <span className="garden-title-dot">.</span>
          </h1>
          <p className="garden-greeting">嗨，想我了吗？♪</p>

          <form className="garden-form" onSubmit={handleSubmit} noValidate>
            <label htmlFor="token">
              访问令牌 <span>Panel Access Token</span>
            </label>
            <div className="garden-input-wrap" data-invalid={Boolean(error)}>
              <input
                ref={inputRef}
                id="token"
                type={visible ? 'text' : 'password'}
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="none"
                placeholder="Panel Access Token"
                value={value}
                disabled={verifying || phase !== 'login'}
                aria-invalid={Boolean(error)}
                aria-describedby={error ? 'token-error' : undefined}
                onChange={(event) => {
                  setValue(event.target.value)
                  setError('')
                }}
              />
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    className="garden-tool"
                    aria-label={visible ? '隐藏' : '显示'}
                    aria-pressed={visible}
                    disabled={verifying || phase !== 'login'}
                    onClick={() => setVisible(!visible)}
                  >
                    {visible ? <EyeOff size={18} /> : <Eye size={18} />}
                  </button>
                </TooltipTrigger>
                <TooltipContent>{visible ? '隐藏令牌' : '显示令牌'}</TooltipContent>
              </Tooltip>
            </div>
            <div className="garden-error" id="token-error" role="alert">
              {error}
            </div>
            <div className="garden-submit-wrap">
              <button className="garden-submit" type="submit" disabled={verifying || phase !== 'login'} aria-busy={verifying || phase === 'entering'}>
                <span>{verifying ? '正在验证' : phase === 'entering' ? '正在进入控制台' : '立即登录'}</span>
                {verifying || phase === 'entering' ? (
                  <Loader2 className="garden-spinner" size={18} />
                ) : (
                  <ArrowRight size={19} />
                )}
              </button>
              {bloom && <span className="garden-bloom" aria-hidden="true" />}
            </div>
          </form>
        </div>
      </section>

      {/* 光影退行：视频溶解时，右上角以完整浓度预印她的侧影——位置、尺寸
          与控制台水印一致，登录页卸载瞬间线稿原地常驻，随后缓缓淡入水印浓度。 */}
      <div
        className="garden-echo pointer-events-none fixed right-[calc(100%_-_100vw_-_6px)] top-[14px] w-[280px] sm:w-[380px] md:w-[480px] lg:w-[560px] xl:w-[640px]"
        aria-hidden="true"
        style={{
          WebkitMaskImage: `url(${import.meta.env.BASE_URL}role-mask.png)`,
          maskImage: `url(${import.meta.env.BASE_URL}role-mask.png)`,
          WebkitMaskSize: 'contain',
          maskSize: 'contain',
          WebkitMaskRepeat: 'no-repeat',
          maskRepeat: 'no-repeat',
          WebkitMaskPosition: 'top right',
          maskPosition: 'top right',
        }}
      />
      <div className="garden-petals" ref={petalsRef} aria-hidden="true" />

      {phase === 'entering' && (
        <div className="garden-interlude" aria-hidden="true">
          <span className="garden-interlude-cn">因你而在的故事</span>
          <span className="garden-interlude-en">TruE</span>
        </div>
      )}

      <footer className="garden-footer">
        <p className="garden-signature">
          「长风化作她的轺车，<wbr />
          四海落成她的圆圃」
        </p>
      </footer>
    </main>
  )
}
