// 图片灯箱：缩略图导航、缩放/拖拽与键盘操作。
import { useEffect, useMemo, useRef, useState } from 'react'
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
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { cachedAssetUrl } from '@/lib/asset-blob-cache'
import { cn } from '@/lib/utils'
import { KIND_META, assetKindOf, useAssetObjectUrl, type AssetKind, type AssetRef } from './asset-refs'
import { AssetMediaCard, AssetImageCard } from './asset-cards'

/* 灯箱缩放参数：滚轮/键盘按指数步进，跨度体感均匀（1 → 1.4 → 2 → 2.7 …）。 */
export const ZOOM_MIN = 1
export const ZOOM_MAX = 8
export const ZOOM_STEP = 1.4

/** 灯箱工具钮：黑幕上的圆形幽灵钮，与浅色主题的 Button 体系解耦。 */
export const LIGHTBOX_TOOL_CLS =
  'inline-flex items-center justify-center rounded-full p-2 text-white/85 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60 disabled:pointer-events-none disabled:opacity-30'

/** 灯箱底部缩略图条的单格：blob 与网格/舞台共用缓存（image 类已在网格加载）。 */
export function LightboxThumb({
  asset,
  active,
  onSelect,
  innerRef,
}: {
  asset: AssetRef
  active: boolean
  onSelect: () => void
  innerRef?: React.Ref<HTMLButtonElement>
}) {
  const { url } = useAssetObjectUrl(asset)
  return (
    <button
      type="button"
      ref={innerRef}
      onClick={onSelect}
      aria-current={active}
      aria-label={`查看 ${asset.file}`}
      className={cn(
        'relative h-14 w-20 shrink-0 overflow-hidden rounded-md border bg-black/30 transition-all duration-200',
        active
          ? 'border-transparent opacity-100 ring-2 ring-[var(--rose)]'
          : 'border-white/10 opacity-50 hover:opacity-90',
      )}
    >
      {url ? (
        <img src={url} alt="" draggable={false} className="h-full w-full object-cover" />
      ) : (
        <span className="skeleton block h-full w-full" />
      )}
    </button>
  )
}

/** 沉浸式大图灯箱：黑幕全屏替代白卡弹窗，图片 object-contain 常驻视野；
 * 顶部悬浮文件名/计数、左右悬浮箭头翻页、底部缩略图条 + 工具栏
 * （缩放 / 下载 / 新标签打开）。支持滚轮缩放、双击缩放、拖拽平移，
 * 键盘 ←/→ 翻页、Home/End 跳转、+/- 缩放、0 复位。 */
export function AssetLightbox({
  assets,
  index,
  onNavigate,
  onClose,
}: {
  assets: AssetRef[]
  index: number | null
  onNavigate: (next: number) => void
  onClose: () => void
}) {
  const asset = index != null ? assets[index] : null
  const { url, failed } = useAssetObjectUrl(asset)

  const stageRef = useRef<HTMLDivElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)
  const activeThumbRef = useRef<HTMLButtonElement>(null)
  const [zoom, setZoom] = useState({ scale: 1, x: 0, y: 0 })
  const [dragging, setDragging] = useState(false)
  const dragStart = useRef<{ px: number; py: number; x: number; y: number } | null>(null)

  const hasPrev = index != null && index > 0
  const hasNext = index != null && index < assets.length - 1
  const go = (next: number) => {
    if (next >= 0 && next < assets.length) onNavigate(next)
  }

  // 切换图片/关闭即复位缩放与拖拽（含 Radix 关闭动画期间 index 置 null 的路径）。
  useEffect(() => {
    setZoom({ scale: 1, x: 0, y: 0 })
    dragStart.current = null
    setDragging(false)
  }, [index])

  // 相邻图片预加载：翻页瞬间目标图已命中 blob 缓存，不再白屏等待。
  useEffect(() => {
    if (index == null) return
    for (const neighbor of [assets[index - 1], assets[index + 1]]) {
      if (neighbor) cachedAssetUrl(neighbor).catch(() => {})
    }
  }, [index, assets])

  // 当前缩略图滚到条带可视区中央（block:'nearest' 防止牵动页面滚动）。
  useEffect(() => {
    activeThumbRef.current?.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' })
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
    if (!url) return
    const anchor = stageAnchor(e)
    if (anchor) zoomTo((cur) => cur * Math.exp(-e.deltaY * 0.0016), anchor.x, anchor.y)
  }
  const onStagePointerDown = (e: React.PointerEvent) => {
    if (!url || zoom.scale <= ZOOM_MIN || e.button !== 0) return
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
    if (!url) return
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
        go(assets.length - 1)
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
    <Dialog open={index != null} onOpenChange={(open) => !open && onClose()}>
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
            <DialogTitle className="truncate font-mono text-xs font-medium text-white/90" title={asset?.file}>
              {asset?.file ?? '—'}
            </DialogTitle>
            <DialogDescription className="tnum mt-0.5 text-2xs text-white/55">
              {index != null ? `${index + 1} / ${assets.length}` : ''}
              {assets.length > 1 && (
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
            style={{ cursor: url && zoom.scale > ZOOM_MIN ? (dragging ? 'grabbing' : 'grab') : 'default' }}
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
            ) : url ? (
              <img
                ref={imgRef}
                src={url}
                alt={asset?.file}
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
          {assets.length > 1 && (
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

        {/* 底部：缩略图条（>1 张时）+ 工具栏（缩放 / 下载 / 新标签打开） */}
        <div className="pointer-events-none absolute inset-x-0 bottom-0 z-10 flex flex-col items-center gap-2.5 bg-gradient-to-t from-black/70 via-black/30 to-transparent pb-4 pt-10">
          {assets.length > 1 && (
            <div className="hide-scrollbar pointer-events-auto flex max-w-full items-center gap-1.5 overflow-x-auto px-5 py-1">
              {assets.map((thumb, i) => (
                <LightboxThumb
                  key={`${thumb.requestId}/${thumb.file}`}
                  asset={thumb}
                  active={i === index}
                  onSelect={() => go(i)}
                  innerRef={i === index ? activeThumbRef : undefined}
                />
              ))}
            </div>
          )}
          <div className="pointer-events-auto flex items-center gap-0.5 rounded-full border border-white/15 bg-black/50 px-1 py-1 shadow-lg backdrop-blur">
            {url && asset ? (
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
                <a href={url} download={asset.file} title={`下载 ${asset.file}`} aria-label="下载" className={LIGHTBOX_TOOL_CLS}>
                  <Download className="h-4 w-4" aria-hidden />
                </a>
                <a href={url} target="_blank" rel="noreferrer" title="在新标签页打开" aria-label="在新标签页打开" className={LIGHTBOX_TOOL_CLS}>
                  <ExternalLink className="h-4 w-4" aria-hidden />
                </a>
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

/** 外置媒体区：图片进缩略图网格（点击进灯箱），音频/视频/文件用行卡内联
 * 加载播放；首行汇总各类数量与占位符说明。灯箱承接本段全部图片序列。 */

export function AssetGallery({ assets }: { assets: AssetRef[] }) {
  const imageAssets = useMemo(() => assets.filter((a) => assetKindOf(a) === 'image'), [assets])
  const plainAssets = useMemo(() => assets.filter((a) => assetKindOf(a) !== 'image'), [assets])
  const kindCounts = useMemo(() => {
    const counts: Partial<Record<AssetKind, number>> = {}
    for (const asset of assets) {
      const kind = assetKindOf(asset)
      counts[kind] = (counts[kind] ?? 0) + 1
    }
    return counts
  }, [assets])
  const summary = [
    `${assets.length} 个外置媒体`,
    ...(Object.keys(KIND_META) as AssetKind[])
      .filter((kind) => kindCounts[kind])
      .map((kind) => `${KIND_META[kind].label} ${kindCounts[kind]}`),
  ].join(' · ')

  const [previewIndex, setPreviewIndex] = useState<number | null>(null)
  const openPreview = (asset: AssetRef) => {
    const idx = imageAssets.findIndex((a) => a.file === asset.file && a.requestId === asset.requestId)
    if (idx >= 0) setPreviewIndex(idx)
  }
  return (
    <div className="mb-3.5 space-y-2">
      <p className="text-2xs text-muted-foreground">
        {summary}
        <span className="text-muted-foreground/65">（base64 已抽出为独立文件，正文内保留占位符）</span>
      </p>
      {imageAssets.length > 0 && (
        // 列数随图量走：单图全宽，2-4 张两列（单格够大看得清），≥5 张三列
        // 控制纵向篇幅——抽屉宽度有限，三列以上单格会小到失去预览意义。
        <div
          className={cn(
            'grid gap-2',
            imageAssets.length >= 5 ? 'grid-cols-3' : imageAssets.length > 1 && 'grid-cols-2',
          )}
        >
          {imageAssets.map((asset) => (
            <AssetImageCard
              key={`${asset.requestId}/${asset.file}`}
              asset={asset}
              featured={imageAssets.length === 1}
              onOpenPreview={() => openPreview(asset)}
            />
          ))}
        </div>
      )}
      {plainAssets.length > 0 && (
        <div className="space-y-2">
          {plainAssets.map((asset) => (
            <AssetMediaCard key={`${asset.requestId}/${asset.file}`} asset={asset} />
          ))}
        </div>
      )}
      <AssetLightbox
        assets={imageAssets}
        index={previewIndex}
        onNavigate={setPreviewIndex}
        onClose={() => setPreviewIndex(null)}
      />
    </div>
  )
}
