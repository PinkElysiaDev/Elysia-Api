import { useEffect, useRef, type RefObject } from 'react'
import { interludeAtTime, type CharacterTrace } from '@/lib/character-trace'

interface Props {
  rootRef: RefObject<HTMLElement>
  sceneRef: RefObject<HTMLDivElement>
  cameraRef: RefObject<HTMLDivElement>
  videoRef: RefObject<HTMLVideoElement>
  targetRef: RefObject<HTMLDivElement>
  trace: CharacterTrace
  onFinish: (animated: boolean) => void
}

export function LoginCinematic({ rootRef, sceneRef, cameraRef, videoRef, targetRef, trace, onFinish }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const textRef = useRef<HTMLSpanElement>(null)
  useEffect(() => {
    let cancelled = false
    let dispose: (() => void) | undefined
    const root = rootRef.current
    const scene = sceneRef.current
    const camera = cameraRef.current
    const video = videoRef.current
    const target = targetRef.current
    const canvas = canvasRef.current
    if (!root || !scene || !camera || !video || !target || !canvas) { onFinish(false); return }
    void import('@/lib/login-renderer').then(({ runLoginTransition }) => {
      if (cancelled) return
      dispose = runLoginTransition({ root, scene, camera, video, target, canvas, trace, onFinish,
        onReady: () => { root.dataset.cinematic = 'ready' },
        onFrame: (seconds) => {
          const text = textRef.current
          if (!text) return
          const presentation = interludeAtTime(seconds)
          text.style.opacity = String(presentation.opacity)
          text.style.filter = `blur(${presentation.blur}px)`
        },
      })
    }).catch(() => { if (!cancelled) onFinish(false) })
    return () => {
      cancelled = true
      dispose?.()
      delete root.dataset.cinematic
    }
  }, [rootRef, sceneRef, cameraRef, videoRef, targetRef, trace, onFinish])
  return <>
    <canvas ref={canvasRef} className="garden-cinematic" aria-hidden="true" />
    <div className="garden-interlude" aria-hidden="true"><span ref={textRef} className="garden-interlude-cn">因你而在的故事</span></div>
  </>
}
