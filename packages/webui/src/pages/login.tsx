import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { ArrowRight, Eye, EyeOff, Loader2, Pause, Play } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { ThemeToggle } from '@/components/theme-toggle'
import { LoginCinematic } from '@/components/login-cinematic'
import { useTheme } from '@/lib/theme'
import { setToken, ARRIVED_FROM_LOGIN_KEY } from '@/lib/auth'
import { verifyToken } from '@/lib/api'
import type { CharacterTrace } from '@/lib/character-trace'
import { loadCharacterTrace } from '@/lib/login-assets'
import { LOGIN_VIDEO_URL, useLoginMotion } from '@/lib/login-motion'
import { ROLE_ANCHOR_CLASS, roleMaskStyle } from '@/lib/role-presentation'
import './login.css'

const MEDIA_BASE = `${import.meta.env.BASE_URL}assets/`

function arrivalFlag(animated: boolean): void {
  try {
    if (animated) sessionStorage.setItem(ARRIVED_FROM_LOGIN_KEY, '1')
    else sessionStorage.removeItem(ARRIVED_FROM_LOGIN_KEY)
  } catch { return }
}

export function LoginPage() {
  const { theme } = useTheme()
  const [value, setValue] = useState('')
  const [visible, setVisible] = useState(false)
  const [error, setError] = useState('')
  const [errorOpen, setErrorOpen] = useState(false)
  const [phase, setPhase] = useState<'login' | 'entering'>('login')
  const [verifying, setVerifying] = useState(false)
  const [traceReady, setTraceReady] = useState(false)
  const rootRef = useRef<HTMLElement>(null)
  const sceneRef = useRef<HTMLDivElement>(null)
  const cameraRef = useRef<HTMLDivElement>(null)
  const targetRef = useRef<HTMLDivElement>(null)
  const videoRef = useRef<HTMLVideoElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const traceRef = useRef<CharacterTrace | null>(null)
  const pendingToken = useRef<string | null>(null)
  const mounted = useRef(true)
  const requestGeneration = useRef({ value: 0 })
  const submitting = useRef(false)
  const motion = useLoginMotion(rootRef, videoRef)
  const latestMotion = useRef(motion)
  latestMotion.current = motion

  const finish = useCallback((animated: boolean) => {
    const token = pendingToken.current
    if (!mounted.current || !token) return
    pendingToken.current = null
    arrivalFlag(animated)
    setToken(token)
  }, [])

  useEffect(() => {
    mounted.current = true
    document.title = '登录控制台 · Elysia API'
    const generationState = requestGeneration.current
    return () => {
      mounted.current = false
      generationState.value++
      pendingToken.current = null
    }
  }, [])

  useEffect(() => {
    let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    const previous = meta?.content
    const created = !meta
    if (!meta) {
      meta = document.createElement('meta')
      meta.name = 'theme-color'
      document.head.appendChild(meta)
    }
    meta.content = theme === 'dark' ? '#18141c' : '#fdfbfc'
    return () => {
      if (created) meta.remove()
      else meta.content = previous ?? ''
    }
  }, [theme])

  useEffect(() => {
    if (!motion.allowed || traceRef.current) return
    const controller = new AbortController()
    void Promise.all([loadCharacterTrace(controller.signal), import('@/lib/login-renderer')])
      .then(([trace]) => {
        if (controller.signal.aborted) return
        traceRef.current = trace
        setTraceReady(true)
      }).catch(() => {
        if (!controller.signal.aborted) setTraceReady(false)
      })
    return () => controller.abort()
  }, [motion.allowed])

  useEffect(() => {
    if (phase === 'entering' && !motion.allowed) finish(false)
  }, [phase, motion.allowed, finish])

  const showError = (message: string) => {
    setError(message)
    setErrorOpen(true)
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (phase !== 'login' || submitting.current) return
    const token = value.trim()
    if (!token) { showError('请输入访问令牌'); return }
    submitting.current = true
    setVerifying(true)
    setError('')
    const generation = ++requestGeneration.current.value
    void import('./overview').catch(() => undefined)
    let failure = ''
    try { await verifyToken(token) }
    catch (caught) { failure = caught instanceof Error && caught.message ? caught.message : '无法连接到后端，请检查网络与服务状态' }
    // generation 失配说明已有更新的提交或组件经历重挂载，本次结果作废；仍需复位提交锁。
    if (!mounted.current || generation !== requestGeneration.current.value) {
      submitting.current = false
      return
    }
    submitting.current = false
    setVerifying(false)
    if (failure) {
      showError(failure)
      return
    }
    pendingToken.current = token
    const video = videoRef.current
    if (latestMotion.current.allowed && latestMotion.current.videoReady && traceRef.current
      && video && !video.paused && video.readyState >= 2 && typeof video.requestVideoFrameCallback === 'function') setPhase('entering')
    else finish(false)
  }

  const toggleMotion = () => {
    motion.toggle()
    if (phase === 'entering') finish(false)
  }
  const motionLabel = motion.reason || (motion.allowed ? '暂停动态效果' : '播放动态效果')

  return (
    <main ref={rootRef} className="garden" data-phase={phase} data-motion={motion.allowed ? 'playing' : 'paused'} data-intro={motion.introEnabled} data-scene-intro={motion.sceneIntroEnabled} data-trace-ready={traceReady}>
      <div ref={sceneRef} className="garden-scene" aria-hidden="true">
        <div ref={cameraRef} className="garden-camera">
          <img className="garden-image" src={`${MEDIA_BASE}elysia-login-poster.jpg`} alt="" />
          <video ref={videoRef} className={`garden-video ${motion.videoReady && !motion.videoFailed && !motion.reduced ? 'is-ready' : ''}`}
            src={motion.requestedVideo ? LOGIN_VIDEO_URL : undefined}
            muted loop playsInline preload="none" onPlaying={motion.onPlaying} onError={motion.onError} />
        </div>
      </div>
      <div className="garden-wash" aria-hidden="true" />
      <div className="garden-aurora" aria-hidden="true" />

      <header className="garden-header">
        <div className="garden-brand" aria-label="Elysia API 控制台">
          <img src={`${import.meta.env.BASE_URL}logo-color.png`} alt="" width={32} height={32} />
          <span>Elysia API</span><span className="garden-brand-divider" /><span className="garden-console">Console</span>
        </div>
        <div className="garden-actions">
          <Tooltip>
            <TooltipTrigger asChild>
              <button type="button" className="garden-tool garden-motion-toggle" onClick={toggleMotion}
                disabled={Boolean(motion.reason)} aria-label={motionLabel} aria-pressed={!motion.allowed}>
                {motion.allowed ? <Pause size={20} aria-hidden="true" /> : <Play size={20} aria-hidden="true" />}
              </button>
            </TooltipTrigger>
            <TooltipContent>{motionLabel}</TooltipContent>
          </Tooltip>
          <ThemeToggle tooltip />
        </div>
      </header>

      <section className="garden-content" aria-label="登录控制台">
        <div className="garden-login-motion">
          <div className="garden-login" aria-hidden={phase !== 'login'}>
            <h1>Elysia <i>API</i><span className="garden-title-dot">.</span></h1>
            <p className="garden-greeting">嗨，想我了吗？♪</p>
            <form className="garden-form" onSubmit={handleSubmit} noValidate>
              <label htmlFor="token" className="sr-only">访问令牌 Panel Access Token</label>
              <div className="garden-input-wrap" data-invalid={Boolean(error)}>
                <input ref={inputRef} id="token" type={visible ? 'text' : 'password'} autoComplete="off" spellCheck={false}
                  autoCapitalize="none" placeholder="Panel Access Token" value={value} disabled={verifying || phase !== 'login'}
                  aria-invalid={Boolean(error)} aria-describedby={error ? 'token-error' : undefined}
                  onChange={(event) => { setValue(event.target.value); setError('') }} />
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button type="button" className="garden-tool" aria-label={visible ? '隐藏' : '显示'} aria-pressed={visible}
                      disabled={verifying || phase !== 'login'} onClick={() => setVisible(!visible)}>
                      {visible ? <EyeOff size={18} /> : <Eye size={18} />}
                    </button>
                  </TooltipTrigger>
                  <TooltipContent>{visible ? '隐藏令牌' : '显示令牌'}</TooltipContent>
                </Tooltip>
              </div>
              {error && <span role="status" className="sr-only" id="token-error">{error}</span>}
              <div className="garden-submit-wrap">
                <button className="garden-submit" type="submit" disabled={verifying || phase !== 'login'} aria-busy={verifying || phase === 'entering'}>
                  <span>{verifying ? '正在验证' : phase === 'entering' ? '正在进入控制台' : '立即登录'}</span>
                  {verifying || phase === 'entering' ? <Loader2 className="garden-spinner" size={18} /> : <ArrowRight size={19} />}
                </button>
              </div>
            </form>
          </div>
        </div>
      </section>

      <div ref={targetRef} className={`garden-echo ${ROLE_ANCHOR_CLASS}`} aria-hidden="true" style={roleMaskStyle()} />
      {phase === 'entering' && traceRef.current && (
        <LoginCinematic rootRef={rootRef} sceneRef={sceneRef} cameraRef={cameraRef} videoRef={videoRef}
          targetRef={targetRef} trace={traceRef.current} onFinish={finish} />
      )}
      <footer className="garden-footer"><p className="garden-signature">「长风化作她的轺车，<wbr />四海落成她的圆圃」</p></footer>

      <Dialog open={errorOpen} onOpenChange={setErrorOpen}>
        <DialogContent className="garden-error-dialog" onCloseAutoFocus={(event) => { event.preventDefault(); inputRef.current?.focus() }}>
          <DialogHeader><DialogTitle>登录失败</DialogTitle><DialogDescription>{error}</DialogDescription></DialogHeader>
          <DialogFooter><DialogClose asChild><Button type="button">我知道了</Button></DialogClose></DialogFooter>
        </DialogContent>
      </Dialog>
    </main>
  )
}
