import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'

import mediaVersion from './login-media-version.json'

export const LOGIN_VIDEO_URL = `${import.meta.env.BASE_URL}assets/elysia-login.mp4?v=${mediaVersion.video}`

export const LOGIN_MOTION_KEY = 'elysia-webui.login-motion'

function readPaused(): boolean {
  try { return localStorage.getItem(LOGIN_MOTION_KEY) === 'paused' } catch { return false }
}

function readSaveData(): boolean {
  return Boolean((navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData)
}

export function useLoginMotion(rootRef: RefObject<HTMLElement>, videoRef: RefObject<HTMLVideoElement>) {
  const [userPaused, setUserPaused] = useState(readPaused)
  const [reduced, setReduced] = useState(() => matchMedia('(prefers-reduced-motion: reduce)').matches)
  const [saveData, setSaveData] = useState(readSaveData)
  const [hidden, setHidden] = useState(document.hidden)
  const [coarse, setCoarse] = useState(() => matchMedia('(pointer: coarse)').matches)
  const [autoplayBlocked, setAutoplayBlocked] = useState(false)
  const [videoFailed, setVideoFailed] = useState(false)
  const [videoReady, setVideoReady] = useState(false)
  const [requestedVideo, setRequestedVideo] = useState(false)
  const [introEnabled, setIntroEnabled] = useState(() => !userPaused && !reduced && !saveData && !hidden)
  const [sceneIntroEnabled] = useState(introEnabled)
  const position = useRef({ horizontal: 0, vertical: 0 })
  const allowed = !userPaused && !reduced && !saveData && !hidden && !autoplayBlocked && !videoFailed

  useEffect(() => { if (!allowed) setIntroEnabled(false) }, [allowed])

  useEffect(() => {
    const motion = matchMedia('(prefers-reduced-motion: reduce)')
    const pointer = matchMedia('(pointer: coarse)')
    const connection = (navigator as Navigator & { connection?: EventTarget }).connection
    const updateMotion = () => setReduced(motion.matches)
    const updatePointer = () => setCoarse(pointer.matches)
    const updateVisibility = () => setHidden(document.hidden)
    const updateConnection = () => setSaveData(readSaveData())
    const updateStorage = () => setUserPaused(readPaused())
    motion.addEventListener('change', updateMotion)
    pointer.addEventListener('change', updatePointer)
    document.addEventListener('visibilitychange', updateVisibility)
    connection?.addEventListener('change', updateConnection)
    window.addEventListener('storage', updateStorage)
    return () => {
      motion.removeEventListener('change', updateMotion)
      pointer.removeEventListener('change', updatePointer)
      document.removeEventListener('visibilitychange', updateVisibility)
      connection?.removeEventListener('change', updateConnection)
      window.removeEventListener('storage', updateStorage)
    }
  }, [])

  useEffect(() => {
    if (allowed) setRequestedVideo(true)
    const video = videoRef.current
    if (!video) return
    let cancelled = false
    if (allowed && requestedVideo) {
      void video.play().catch((error: DOMException) => {
        if (cancelled || error.name === 'AbortError') return
        if (error.name === 'NotAllowedError') setAutoplayBlocked(true)
        else setVideoFailed(true)
      })
    } else video.pause()
    return () => { cancelled = true }
  }, [allowed, requestedVideo, videoRef])

  useEffect(() => {
    const root = rootRef.current
    if (!root || !allowed || coarse) return
    let targetHorizontal = position.current.horizontal
    let targetVertical = position.current.vertical
    let animation = 0
    let previousTime = 0
    const tick = (time: number) => {
      const delta = previousTime ? Math.min(64, time - previousTime) : 16.67
      previousTime = time
      const damping = 1 - Math.exp(-delta / 270)
      position.current.horizontal += (targetHorizontal - position.current.horizontal) * damping
      position.current.vertical += (targetVertical - position.current.vertical) * damping
      root.style.setProperty('--par-x', position.current.horizontal.toFixed(5))
      root.style.setProperty('--par-y', position.current.vertical.toFixed(5))
      animation = Math.abs(targetHorizontal - position.current.horizontal) + Math.abs(targetVertical - position.current.vertical) > 0.001 ? requestAnimationFrame(tick) : 0
      if (!animation) previousTime = 0
    }
    const move = (event: PointerEvent) => {
      if (event.pointerType === 'touch') return
      targetHorizontal = event.clientX / window.innerWidth * 2 - 1
      targetVertical = event.clientY / window.innerHeight * 2 - 1
      if (!animation) animation = requestAnimationFrame(tick)
    }
    const leave = () => {
      targetHorizontal = 0
      targetVertical = 0
      if (!animation) animation = requestAnimationFrame(tick)
    }
    window.addEventListener('pointermove', move)
    document.documentElement.addEventListener('pointerleave', leave)
    return () => {
      window.removeEventListener('pointermove', move)
      document.documentElement.removeEventListener('pointerleave', leave)
      cancelAnimationFrame(animation)
    }
  }, [allowed, coarse, rootRef])

  const toggle = useCallback(() => {
    if (reduced || saveData || videoFailed) return
    const pause = !userPaused && !autoplayBlocked
    setUserPaused(pause)
    try { localStorage.setItem(LOGIN_MOTION_KEY, pause ? 'paused' : 'playing') } catch { /* 写入失败（如隐私模式）：本次会话内仍生效，仅不持久化 */ }
    const video = videoRef.current
    if (pause) video?.pause()
    else {
      setAutoplayBlocked(false)
      setRequestedVideo(true)
      if (video) {
        if (!video.getAttribute('src')) video.src = LOGIN_VIDEO_URL
        void video.play().catch((error: DOMException) => {
          if (error.name === 'NotAllowedError') setAutoplayBlocked(true)
          else if (error.name !== 'AbortError') setVideoFailed(true)
        })
      }
    }
  }, [autoplayBlocked, reduced, saveData, userPaused, videoFailed, videoRef])

  const reason = reduced ? '已遵循系统减少动态效果设置' : saveData ? '省流模式下已暂停动态效果' : videoFailed ? '动态壁纸加载失败' : ''
  return { allowed, reduced, videoReady, videoFailed, requestedVideo, introEnabled, sceneIntroEnabled, toggle, reason,
    onPlaying: () => setVideoReady(true), onError: () => setVideoFailed(true) }
}
