// 灯箱舞台：与图片来源无关的沉浸式大图查看核心（缩放/拖拽/翻页/键盘）。
// usage 日志详情（blob 懒解析）与 agent 消息图片（dataUrl 现成）共用；
// 图片解析由调用方完成，底部缩略图条等场景差异经 bottomBar 插槽注入。
import { useEffect, useRef, useState } from 'react'
import {
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Download,
  ExternalLink,
  Loader2,
  Maximize,
  X,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'

/* 灯箱缩放参数：滚轮/键盘按指数步进，跨度体感均匀（1 → 1.4 → 2 → 2.7 …）。 */
export const ZOOM_MIN = 1
export const ZOOM_MAX = 8
export const ZOOM_STEP = 1.4

/** 灯箱工具钮：黑幕上的圆形幽灵钮，与浅色主题的 Button 体系解耦。 */
export const LIGHTBOX_TOOL_CLS =
  'inline-flex items-center justify-center rounded-full p-2 text-white/85 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60 disabled:pointer-events-none disabled:opacity-30'

/** 沉浸式大图舞台：黑幕全屏，图片 object-contain 常驻视野；顶部悬浮文件名/
 * 计数、左右悬浮箭头翻页、底部工具栏（缩放 / 下载 / 新标签打开）。支持滚轮
 * 缩放、双击缩放、拖拽平移，键盘 ←/→ 翻页、Home/End 跳转、+/- 缩放、0 复位。 */
export function LightboxStage({
  open,
  src,
  failed = false,
  name,
  index,
  total,
  onNavigate,
  onClose,
  bottomBar,
}: {
  open: boolean
  src: string | null
  failed?: boolean
  name: string
  index: number | null
  total: number
  onNavigate: (next: number) => void
  onClose: () => void
  /** 底部工具栏上方的插槽（如缩略图条）；不传则只有工具栏。 */
  bottomBar?: React.ReactNode
}) {
  const stageRef = useRef<HTMLDivElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)
  const [zoom, setZoom] = useState({ scale: 1, x: 0, y: 0 })
  const [dragging, setDragging] = useState(false)
  const dragStart = useRef<{ px: number; py: number; x: number; y: number } | null>(null)

  const hasPrev = index != null && index > 0
  const hasNext = index != null && index < total - 1
  const go = (next: number) => {
    if (next >= 0 && next < total) onNavigate(next)
  }

  // 切换图片/关闭即复位缩放与拖拽（含 Radix 关闭动画期间 index 置 null 的路径）。
  useEffect(() => {
    setZoom({ scale: 1, x: 0, y: 0 })
    dragStart.current = null
    setDragging(false)
  }, [index])

  /** 放大后的位移按图片实际渲染尺寸收边：图片边缘不得脱出舞台。 */
  const clampOffset = (x: number, y: number, scale: number) => {
    const stage = stageRef.current
    const img = imgRef.current
    if (!stage || !img || scale <= ZOOM_MIN) return { x: 0, y: 0 }
    const maxX = Math.max((img.clientWidth * scale - stage.clientWidth) / 2, 0)
    const maxY = Math.max((img.clientHeight * scale - stage.clientHeight) / 2, 0)
    return { x: Math.min(Math.max(x, -maxX), maxX), y: Math.min(Math.max(y, -maxY), maxY) }
  }

  /** 缩放到目标倍率；anchor 为光标相对舞台中心的像素，缺省时绕中心缩放。
   * 用函数式目标而非外部快照，连续滚轮事件批处理时不会丢步。 */
  const zoomTo = (target: number | ((cur: number) => number), anchorX?: number, anchorY?: number) => {
    setZoom((cur) => {
      const scale = Math.min(
        Math.max(typeof target === 'function' ? target(cur.scale) : target, ZOOM_MIN),
        ZOOM_MAX,
      )
      if (scale <= ZOOM_MIN) return { scale: 1, x: 0, y: 0 }
      const k = scale / cur.scale
      const next =
        anchorX == null || anchorY == null
          ? { x: cur.x * k, y: cur.y * k }
          : { x: anchorX - (anchorX - cur.x) * k, y: anchorY - (anchorY - cur.y) * k }
      return { scale, ...clampOffset(next.x, next.y, scale) }
    })
  }

  /** 光标坐标 → 舞台中心系（缩放锚点）。 */
  const stageAnchor = (e: { clientX: number; clientY: number }) => {
    const rect = stageRef.current?.getBoundingClientRect()
    if (!rect) return null
    return { x: e.clientX - rect.left - rect.width / 2, y: e.clientY - rect.top - rect.height / 2 }
  }

  const onStageWheel = (e: React.WheelEvent) => {
    if (!src) return
    const anchor = stageAnchor(e)
    if (anchor) zoomTo((cur) => cur * Math.exp(-e.deltaY * 0.0016), anchor.x, anchor.y)
  }
  const onStagePointerDown = (e: React.PointerEvent) => {
    if (!src || zoom.scale <= ZOOM_MIN || e.button !== 0) return
    dragStart.current = { px: e.clientX, py: e.clientY, x: zoom.x, y: zoom.y }
    setDragging(true)
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onStagePointerMove = (e: React.PointerEvent) => {
    const start = dragStart.current
    if (!start) return
    setZoom((cur) => ({
      ...cur,
      ...clampOffset(start.x + e.clientX - start.px, start.y + e.clientY - start.py, cur.scale),
    }))
  }
  const endDrag = () => {
    dragStart.current = null
    setDragging(false)
  }
  const onStageDoubleClick = (e: React.MouseEvent) => {
    if (!src) return
    if (zoom.scale > ZOOM_MIN) {
      zoomTo(ZOOM_MIN)
      return
    }
    const anchor = stageAnchor(e)
    if (anchor) zoomTo(2.5, anchor.x, anchor.y)
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    // Radix 关闭动画期间 Content 仍挂载而 index 已置 null：直接忽略，
    // 否则 ArrowRight 会把刚关闭的弹窗重开到下一张。
    if (index == null) return
    switch (e.key) {
      case 'ArrowLeft':
        go(index - 1)
        break
      case 'ArrowRight':
        go(index + 1)
        break
      case 'Home':
        go(0)
        break
      case 'End':
        go(total - 1)
        break
      case '+':
      case '=':
        zoomTo((cur) => cur * ZOOM_STEP)
        break
      case '-':
      case '_':
        zoomTo((cur) => cur / ZOOM_STEP)
        break
      case '0':
        zoomTo(ZOOM_MIN)
        break
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        hideClose
        onKeyDown={onKeyDown}
        // 全屏黑幕灯箱：覆盖 DialogContent 的居中卡片形态（inset-0 + 清空
        // max-w/圆角/内边距），保留 Radix 的焦点圈闭、Esc 关闭与进出场动画。
        className="fixed inset-0 left-0 top-0 z-[75] flex h-full max-h-none w-full max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none border-0 bg-black/80 p-0 backdrop-blur-sm"
      >
        {/* 顶部悬浮信息栏：文件名 + 序号计数 + 操作提示 + 关闭 */}
        <div className="pointer-events-none absolute inset-x-0 top-0 z-10 flex items-start justify-between gap-3 bg-gradient-to-b from-black/70 via-black/30 to-transparent pb-10 pl-5 pr-4 pt-4">
          <div className="min-w-0 flex-1">
            <DialogTitle className="truncate font-mono text-xs font-medium text-white/90" title={name}>
              {name || '—'}
            </DialogTitle>
            <DialogDescription className="tnum mt-0.5 text-2xs text-white/55">
              {index != null ? `${index + 1} / ${total}` : ''}
              {total > 1 && (
                <span className="ml-2 hidden sm:inline">←/→ 切换 · 滚轮缩放 · 双击放大</span>
              )}
            </DialogDescription>
          </div>
          <DialogClose
            aria-label="关闭"
            className="pointer-events-auto inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-full border border-white/15 bg-black/40 text-white/85 backdrop-blur transition-colors hover:bg-black/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60"
          >
            <X className="h-4 w-4" aria-hidden />
          </DialogClose>
        </div>

        {/* 舞台：图片居中 contain；翻页箭头挂在舞台外层，拖拽/双击不会误触 */}
        <div className="relative flex min-h-0 min-w-0 flex-1">
          <div
            ref={stageRef}
            className="flex min-w-0 flex-1 select-none items-center justify-center overflow-hidden"
            style={{ cursor: src && zoom.scale > ZOOM_MIN ? (dragging ? 'grabbing' : 'grab') : 'default' }}
            onWheel={onStageWheel}
            onPointerDown={onStagePointerDown}
            onPointerMove={onStagePointerMove}
            onPointerUp={endDrag}
            onPointerCancel={endDrag}
            onDoubleClick={onStageDoubleClick}
          >
            {failed ? (
              <div className="flex flex-col items-center gap-2 text-sm text-white/70">
                <AlertTriangle className="h-5 w-5" aria-hidden />
                图片获取失败
              </div>
            ) : src ? (
              <img
                ref={imgRef}
                src={src}
                alt={name}
                draggable={false}
                className="max-h-full max-w-full object-contain transition-transform duration-200 ease-smooth motion-reduce:transition-none"
                style={{
                  transform: `translate3d(${zoom.x}px, ${zoom.y}px, 0) scale(${zoom.scale})`,
                  transitionDuration: dragging ? '0ms' : undefined,
                }}
              />
            ) : (
              <div className="flex flex-col items-center gap-2 text-sm text-white/70">
                <Loader2 className="h-5 w-5 animate-spin" aria-hidden />
                加载中…
              </div>
            )}
          </div>
          {total > 1 && (
            <>
              <button
                type="button"
                disabled={!hasPrev}
                aria-label="上一张"
                onClick={() => go(index! - 1)}
                className="absolute left-3 top-1/2 -translate-y-1/2 rounded-full border border-white/15 bg-black/40 p-2.5 text-white/85 backdrop-blur transition-colors hover:bg-black/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60 disabled:pointer-events-none disabled:opacity-30"
              >
                <ChevronLeft className="h-5 w-5" aria-hidden />
              </button>
              <button
                type="button"
                disabled={!hasNext}
                aria-label="下一张"
                onClick={() => go(index! + 1)}
                className="absolute right-3 top-1/2 -translate-y-1/2 rounded-full border border-white/15 bg-black/40 p-2.5 text-white/85 backdrop-blur transition-colors hover:bg-black/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60 disabled:pointer-events-none disabled:opacity-30"
              >
                <ChevronRight className="h-5 w-5" aria-hidden />
              </button>
            </>
          )}
        </div>

        {/* 底部：场景插槽（如缩略图条，>1 张时）+ 工具栏（缩放 / 下载 / 新标签） */}
        <div className="pointer-events-none absolute inset-x-0 bottom-0 z-10 flex flex-col items-center gap-2.5 bg-gradient-to-t from-black/70 via-black/30 to-transparent pb-4 pt-10">
          {total > 1 && bottomBar}
          <div className="pointer-events-auto flex items-center gap-0.5 rounded-full border border-white/15 bg-black/50 px-1 py-1 shadow-lg backdrop-blur">
            {src && index != null ? (
              <>
                <button
                  type="button"
                  aria-label="缩小"
                  title="缩小（-）"
                  disabled={zoom.scale <= ZOOM_MIN}
                  onClick={() => zoomTo((cur) => cur / ZOOM_STEP)}
                  className={LIGHTBOX_TOOL_CLS}
                >
                  <ZoomOut className="h-4 w-4" aria-hidden />
                </button>
                <span className="tnum w-11 text-center font-mono text-2xs text-white/70">
                  {Math.round(zoom.scale * 100)}%
                </span>
                <button
                  type="button"
                  aria-label="放大"
                  title="放大（+）"
                  disabled={zoom.scale >= ZOOM_MAX}
                  onClick={() => zoomTo((cur) => cur * ZOOM_STEP)}
                  className={LIGHTBOX_TOOL_CLS}
                >
                  <ZoomIn className="h-4 w-4" aria-hidden />
                </button>
                {zoom.scale > ZOOM_MIN && (
                  <button
                    type="button"
                    aria-label="适应窗口"
                    title="适应窗口（0）"
                    onClick={() => zoomTo(ZOOM_MIN)}
                    className={LIGHTBOX_TOOL_CLS}
                  >
                    <Maximize className="h-4 w-4" aria-hidden />
                  </button>
                )}
                <i className="mx-1 h-4 w-px bg-white/15" aria-hidden />
                <a href={src} download={name} title={`下载 ${name}`} aria-label="下载" className={LIGHTBOX_TOOL_CLS}>
                  <Download className="h-4 w-4" aria-hidden />
                </a>
                {/* Chromium 禁止顶层导航到 data: URL——agent 消息图片全是
                    dataUrl，该入口只在 http(s)/blob 源下展示。 */}
                {!src.startsWith("data:") && (
                  <a href={src} target="_blank" rel="noreferrer" title="在新标签页打开" aria-label="在新标签页打开" className={LIGHTBOX_TOOL_CLS}>
                    <ExternalLink className="h-4 w-4" aria-hidden />
                  </a>
                )}
              </>
            ) : (
              <span className="px-3.5 py-1.5 text-2xs text-white/60">{failed ? '图片获取失败' : '加载中…'}</span>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
