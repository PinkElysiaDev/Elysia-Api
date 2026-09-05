import { useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Download,
  ExternalLink,
  FileText,
  Film,
  Image as ImageIcon,
  Loader2,
  Maximize,
  MoveRight,
  Music2,
  X,
  ZoomIn,
  ZoomOut,
  type LucideIcon,
} from 'lucide-react'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetHeader,
  SheetSectionTitle,
  SheetTitle,
} from '@/components/ui/sheet'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { cachedAssetBytes, cachedAssetUrl } from '@/lib/asset-blob-cache'
import { colorize } from '@/lib/json-highlight'
import { protocolLabel } from '@/lib/protocol'
import type { UsageBody, UsageLogDetail } from '@/lib/types'
import { cn, downloadJSON, formatBytes, formatDateTime, formatDuration, formatNumber, tryParseJSON } from '@/lib/utils'

/* ---------------- 详情抽屉 ---------------- */

export function LogDetailSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const toast = useToast()
  const [detail, setDetail] = useState<UsageLogDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (!id) {
      setDetail(null)
      setErr(null)
      return
    }
    let active = true
    setLoading(true)
    setErr(null)
    api
      .usageLogDetail(id)
      .then((data) => {
        if (active) setDetail(data)
      })
      .catch((e) => {
        if (active) setErr((e as Error).message)
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [id])

  function handleExport() {
    if (!detail) return
    const filename = `usage-log-${detail.requestId}.json`
    downloadJSON(filename, buildExportPayload(detail))
    toast.success('已导出完整日志', filename)
  }

  const usage = detail?.usage
  // tokbar 三段：缓存命中 rose-soft / 未命中输入 rose / 输出 jade
  const cacheHit = usage?.cacheHitTokens ?? 0
  const inputTotal = usage?.inputTokens ?? 0
  const inputMiss = Math.max(inputTotal - cacheHit, 0)
  const output = usage?.outputTokens ?? 0
  const tokTotal = cacheHit + inputMiss + output
  const outputRate =
    detail && output > 0 && detail.durationMs > 0 ? (output / (detail.durationMs / 1000)).toFixed(1) : null

  return (
    <Sheet open={!!id} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-[min(640px,94vw)]">
        <SheetHeader>
          <div className="min-w-0 pr-8">
            <SheetTitle>调用详情</SheetTitle>
            <p className="mt-0.5 break-all font-mono text-xs text-muted-foreground">
              {detail?.requestId ?? id}
              {detail && (
                <span className="ml-2">
                  · {formatDateTime(detail.startedAt)}
                </span>
              )}
            </p>
          </div>
        </SheetHeader>
        <SheetBody>
          {loading && <div className="skeleton h-64 rounded-md" />}
          {err && <p className="text-sm text-ember">{err}</p>}
          {!loading && !err && detail && (
            <>
              {detail.error && (
                <div className="mb-5 flex items-start gap-2 rounded-[7px] border border-[color-mix(in_srgb,var(--ember)_35%,transparent)] bg-[color-mix(in_srgb,var(--ember)_7%,transparent)] p-3 text-sm text-ember">
                  <AlertTriangle className="mt-0.5 h-[15px] w-[15px] shrink-0" />
                  <span className="min-w-0 break-all">
                    HTTP {detail.statusCode}
                    {detail.errorKind && ` · ${errorKindLabel(detail.errorKind)}`} · {detail.error}
                  </span>
                </div>
              )}

              <section className="mb-5">
                <SheetSectionTitle>协议链路</SheetSectionTitle>
                <div className="flex flex-wrap items-center gap-[7px]">
                  <span className="max-w-full break-all rounded-[5px] border border-input bg-card px-2 py-[3px] font-mono text-2xs text-muted-foreground">
                    {protocolLabel(detail.sourceFormat || detail.inputFormat || '', 'long') || '—'}
                  </span>
                  <MoveRight className="h-3 w-3 text-muted-foreground" aria-hidden />
                  <span
                    className={cn(
                      'max-w-full break-all rounded-[5px] border px-2 py-[3px] font-mono text-2xs',
                      detail.sourceFormat && detail.targetFormat && detail.sourceFormat !== detail.targetFormat
                        ? 'border-rose bg-wash text-rose'
                        : 'border-input bg-card text-muted-foreground',
                    )}
                  >
                    {protocolLabel(detail.targetFormat || detail.platform, 'long') || '—'}
                  </span>
                  <span className="ml-2 inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
                    <CopyButton value={detail.requestId} />
                  </span>
                </div>
                <dl className="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-[7px] text-xs">
                  <dt className="whitespace-nowrap text-muted-foreground">调用方</dt>
                  <dd className="tnum min-w-0 break-all">{detail.keyName || '—'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">模型组 → 命中</dt>
                  <dd className="tnum min-w-0 break-all">
                    {detail.groupName || '—'} → <span className="font-mono">{detail.modelName || '—'}</span>
                  </dd>
                  <dt className="whitespace-nowrap text-muted-foreground">平台协议</dt>
                  <dd className="min-w-0 break-all font-mono text-xs">
                    {protocolLabel(detail.platform || '', 'long') || '—'}
                  </dd>
                  <dt className="whitespace-nowrap text-muted-foreground">传输方式</dt>
                  <dd className="tnum">{detail.stream ? '流式' : '缓冲'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">用量来源</dt>
                  <dd className="min-w-0 break-all font-mono text-xs">{detail.usageSource || '—'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">重试次数</dt>
                  <dd className="tnum">{detail.retryCount > 0 ? `${detail.retryCount} 次` : '无'}</dd>
                </dl>
              </section>

              <section className="mb-5">
                <SheetSectionTitle>耗时与用量</SheetSectionTitle>
                <p className="tnum mb-2 flex flex-wrap gap-x-4 text-xs text-muted-foreground">
                  <span>
                    首字 <b className="font-semibold text-foreground">{formatDuration(detail.firstByteMs)}</b>
                  </span>
                  <span>
                    总耗时 <b className="font-semibold text-foreground">{formatDuration(detail.durationMs)}</b>
                  </span>
                  {outputRate && (
                    <span>
                      输出速率 <b className="font-semibold text-foreground">{outputRate} tok/s</b>
                    </span>
                  )}
                </p>
                {tokTotal > 0 ? (
                  <>
                    <div className="my-2 flex h-2 overflow-hidden rounded-[5px] bg-border">
                      {cacheHit > 0 && <i className="h-full" style={{ width: `${(cacheHit / tokTotal) * 100}%`, background: 'var(--rose-soft)' }} />}
                      {inputMiss > 0 && <i className="h-full" style={{ width: `${(inputMiss / tokTotal) * 100}%`, background: 'var(--rose)' }} />}
                      {output > 0 && <i className="h-full" style={{ width: `${(output / tokTotal) * 100}%`, background: 'var(--jade)' }} />}
                    </div>
                    <p className="tnum flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--rose-soft)' }} />
                        缓存命中 {formatNumber(cacheHit)}
                        {inputTotal > 0 && (
                          <span className="text-muted-foreground">（输入的 {Math.round((cacheHit / inputTotal) * 100)}%）</span>
                        )}
                      </span>
                      <span>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--rose)' }} />
                        未命中输入 {formatNumber(inputMiss)}
                      </span>
                      <span>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--jade)' }} />
                        输出 {formatNumber(output)}
                      </span>
                      <span>
                        合计 <b className="font-semibold text-foreground">{formatNumber(tokTotal)}</b>
                      </span>
                      {usage?.estimated && <span className="text-amber">（估算）</span>}
                    </p>
                  </>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    无 token 用量（embedding / 请求未完成或上游未返回 usage）。
                  </p>
                )}
              </section>

              {detail.retryCount > 0 && detail.retryEvents && detail.retryEvents.length > 0 && (
                <section className="mb-5">
                  <SheetSectionTitle>重试事件</SheetSectionTitle>
                  <div className="flex flex-col gap-[7px] text-xs">
                    {detail.retryEvents.map((ev, i) => (
                      <div key={i} className="flex items-start gap-2.5">
                        <span className="tnum mt-px flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-full border border-ember font-mono text-2xs leading-none text-ember">
                          {ev.attempt}
                        </span>
                        <span className="min-w-0 break-all leading-[18px]">
                          <span className="font-mono text-xs">{ev.model}</span>
                          {ev.error && <span className="text-ember"> — {ev.error}</span>}
                        </span>
                      </div>
                    ))}
                  </div>
                </section>
              )}

              <section className="mb-5">
                <SheetSectionTitle>链路原文</SheetSectionTitle>
                <ChainBodies key={detail.requestId} detail={detail} />
                <p className="mt-2 flex items-center gap-1.5 text-2xs text-muted-foreground">
                  <AlertTriangle className="h-3 w-3" aria-hidden />
                  请求 / 响应体可能被截断，并非完整内容
                </p>
              </section>

              <div className="flex justify-end gap-2.5 border-t border-border pt-4">
                <Button variant="outline" onClick={onClose}>
                  关闭
                </Button>
                <Button onClick={handleExport}>
                  <Download /> 导出完整日志
                </Button>
              </div>
            </>
          )}
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}

/* ---------------- 外置媒体（占位符渲染） ---------------- */

/** 占位符形态：__ELYSIA_ASSET__:<requestId>/<hash16>.<ext> */
interface AssetRef {
  requestId: string
  file: string
}

const ASSET_REF_PATTERN = /__ELYSIA_ASSET__:([\w-]+)\/([0-9a-f]{16}\.[a-z0-9]{1,5})/g

type AssetKind = 'image' | 'audio' | 'video' | 'file'

const KIND_META: Record<AssetKind, { icon: LucideIcon; label: string }> = {
  image: { icon: ImageIcon, label: '图片' },
  audio: { icon: Music2, label: '音频' },
  video: { icon: Film, label: '视频' },
  file: { icon: FileText, label: '文件' },
}

function assetKind(ext: string): AssetKind {
  if (['png', 'jpg', 'gif', 'webp'].includes(ext)) return 'image'
  if (['mp3', 'wav', 'ogg'].includes(ext)) return 'audio'
  if (['mp4', 'webm'].includes(ext)) return 'video'
  return 'file'
}

function assetKindOf(asset: AssetRef): AssetKind {
  return assetKind(asset.file.split('.').pop() ?? '')
}

/** 从一段链路原文中提取外置媒体引用（去重，保持出现顺序）。 */
function extractAssetRefs(content: string): AssetRef[] {
  const seen = new Set<string>()
  const refs: AssetRef[] = []
  for (const match of content.matchAll(ASSET_REF_PATTERN)) {
    const asset = { requestId: match[1], file: match[2] }
    const key = `${asset.requestId}/${asset.file}`
    if (!seen.has(key)) {
      seen.add(key)
      refs.push(asset)
    }
  }
  return refs
}

/** 外置媒体 blob hook：经模块级 LRU 缓存取 objectURL（同一资产在缩略图、
 * 弹窗与重开的链路段之间复用一份 blob，<img> 无法附带 Bearer 头）。
 * asset 切换/置空时立即清空旧 url——否则加载下一张期间会短暂显示上一张
 * （且下载按钮会把上一张的字节存成新文件名）。URL 生命周期归缓存所有，
 * 组件不做 revoke。reloadNonce 递增时对同键重取（失败重试用）。 */
function useAssetObjectUrl(asset: AssetRef | null, reloadNonce = 0) {
  const [url, setUrl] = useState<string | null>(null)
  const [bytes, setBytes] = useState<number | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (!asset) {
      setUrl(null)
      setBytes(null)
      return
    }
    let cancelled = false
    setFailed(false)
    setUrl(null)
    setBytes(null)
    cachedAssetUrl(asset)
      .then((cached) => {
        if (cancelled) return
        setUrl(cached)
        setBytes(cachedAssetBytes(asset))
      })
      .catch(() => {
        if (!cancelled) setFailed(true)
      })
    return () => {
      cancelled = true
    }
    // 仅依赖身份字段与重试计数：asset 为 null 时立即清空，对象身份变化不触发重取。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [asset?.requestId, asset?.file, reloadNonce])
  return { url, bytes, failed }
}

/** 图片卡：上图下名的两段结构。图区是固定高度画布，object-contain 完整
 * 显示（截图/文档类图片裁切后只剩无意义局部，contain 才保得住信息量）；
 * 文件名与体积放底栏常显——hex 文件名是唯一的可读标识，叠在图片内容上的
 * hover 遮罩既难读又挡内容。点击图区进灯箱，下载钮在底栏右侧。 */
function AssetImageCard({
  asset,
  featured,
  onOpenPreview,
}: {
  asset: AssetRef
  featured: boolean
  onOpenPreview: () => void
}) {
  const [reloadNonce, setReloadNonce] = useState(0)
  const { url, bytes, failed } = useAssetObjectUrl(asset, reloadNonce)
  const canvasCls = featured ? 'min-h-44 max-h-72' : 'h-40'
  return (
    <figure className="group overflow-hidden rounded-md border border-border bg-card transition-colors duration-200 hover:border-[color-mix(in_srgb,var(--rose)_55%,transparent)]">
      {url ? (
        <button type="button" className="block w-full cursor-zoom-in bg-code" onClick={onOpenPreview} aria-label={`查看大图：${asset.file}`} title="点击查看大图">
          <img
            src={url}
            alt={asset.file}
            draggable={false}
            loading="lazy"
            className={cn('mx-auto w-full object-contain', canvasCls)}
          />
        </button>
      ) : failed ? (
        <div className={cn('flex w-full flex-col items-center justify-center gap-2 bg-code', canvasCls)}>
          <AlertTriangle className="h-4 w-4 text-ember" aria-hidden />
          <span className="text-2xs text-muted-foreground">缩略图获取失败</span>
          <Button variant="outline" size="sm" onClick={() => setReloadNonce((n) => n + 1)}>
            重试
          </Button>
        </div>
      ) : (
        <div className={cn('skeleton w-full', canvasCls)} aria-label="缩略图加载中" />
      )}
      <figcaption className="flex min-w-0 items-center gap-1.5 border-t border-border px-2.5 py-1.5">
        <ImageIcon className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden />
        <span className="min-w-0 flex-1 truncate font-mono text-2xs text-foreground" title={asset.file}>
          {asset.file}
        </span>
        {bytes != null && (
          <span className="tnum shrink-0 text-2xs text-muted-foreground">{formatBytes(bytes)}</span>
        )}
        {url && (
          <a
            href={url}
            download={asset.file}
            title={`下载 ${asset.file}`}
            aria-label={`下载 ${asset.file}`}
            className="shrink-0 rounded p-0.5 text-muted-foreground transition-colors hover:text-rose"
          >
            <Download className="h-3.5 w-3.5" aria-hidden />
          </a>
        )}
      </figcaption>
    </figure>
  )
}

/** 音频/视频/文件行卡：点击「加载」后内联播放（自动拉取大媒体不划算），
 * 失败可重试；文件类加载后提供下载。 */
function AssetMediaCard({ asset }: { asset: AssetRef }) {
  const kind = assetKindOf(asset)
  const [requested, setRequested] = useState(false)
  const [reloadNonce, setReloadNonce] = useState(0)
  const { url, bytes, failed } = useAssetObjectUrl(requested ? asset : null, reloadNonce)
  const { icon: Icon, label } = KIND_META[kind]

  let stateCtl: React.ReactNode
  if (!requested) {
    stateCtl = (
      <Button variant="outline" size="sm" className="shrink-0" onClick={() => setRequested(true)}>
        加载
      </Button>
    )
  } else if (failed) {
    stateCtl = (
      <Button variant="outline" size="sm" className="shrink-0" onClick={() => setReloadNonce((n) => n + 1)}>
        重试
      </Button>
    )
  } else if (!url) {
    stateCtl = (
      <span className="inline-flex shrink-0 items-center gap-1.5 text-2xs text-muted-foreground">
        <Loader2 className="h-3 w-3 animate-spin" aria-hidden /> 加载中…
      </span>
    )
  } else if (kind === 'file') {
    stateCtl = (
      <Button variant="outline" size="sm" asChild className="shrink-0">
        <a href={url} download={asset.file}>
          <Download /> 下载
        </a>
      </Button>
    )
  } else {
    stateCtl = (
      <a
        href={url}
        download={asset.file}
        title={`下载 ${asset.file}`}
        aria-label={`下载 ${asset.file}`}
        className="inline-flex h-[29px] w-[29px] shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-wash hover:text-rose"
      >
        <Download className="h-3.5 w-3.5" aria-hidden />
      </a>
    )
  }

  return (
    <div className="rounded-md border border-border bg-card p-2.5">
      <div className="flex items-center gap-2.5">
        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-wash text-rose">
          <Icon className="h-3.5 w-3.5" aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-2xs text-foreground" title={asset.file}>
            {asset.file}
          </p>
          <p className="tnum text-2xs text-muted-foreground">
            {label}
            {bytes != null && ` · ${formatBytes(bytes)}`}
          </p>
        </div>
        {stateCtl}
      </div>
      {url && kind === 'audio' && <audio controls src={url} preload="metadata" className="mt-2.5 w-full" />}
      {url && kind === 'video' && (
        <video controls src={url} preload="metadata" playsInline className="mt-2.5 max-h-64 w-full rounded-md border border-border bg-code" />
      )}
    </div>
  )
}

/* 灯箱缩放参数：滚轮/键盘按指数步进，跨度体感均匀（1 → 1.4 → 2 → 2.7 …）。 */
const ZOOM_MIN = 1
const ZOOM_MAX = 8
const ZOOM_STEP = 1.4

/** 灯箱工具钮：黑幕上的圆形幽灵钮，与浅色主题的 Button 体系解耦。 */
const LIGHTBOX_TOOL_CLS =
  'inline-flex items-center justify-center rounded-full p-2 text-white/85 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60 disabled:pointer-events-none disabled:opacity-30'

/** 灯箱底部缩略图条的单格：blob 与网格/舞台共用缓存（image 类已在网格加载）。 */
function LightboxThumb({
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
function AssetLightbox({
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
function AssetGallery({ assets }: { assets: AssetRef[] }) {
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

function ChainBodies({ detail }: { detail: UsageLogDetail }) {
  const [openSegments, setOpenSegments] = useState<Set<string>>(() => new Set(['incoming']))
  const segments: { key: string; title: string; body: UsageBody | undefined }[] = [
    { key: 'incoming', title: '① 下游请求', body: detail.incomingBody },
    { key: 'outgoing', title: '② 后端转发', body: detail.outgoingBody },
    { key: 'provider', title: '③ 上游回传', body: detail.providerResponse },
    { key: 'downstream', title: '④ 返回下游', body: detail.downstreamResponse },
  ]
  return (
    <>
      {segments.map((seg) => {
        const content = seg.body?.content ?? ''
        const pretty = prettyPrintBody(content)
        const assets = extractAssetRefs(content)
        const open = content.length > 0 && openSegments.has(seg.key)
        const panelId = `chain-body-${seg.key}`
        const triggerId = `chain-trigger-${seg.key}`
        return (
          <div key={seg.key} className="border-t border-border first:border-t-0">
            <button
              type="button"
              id={triggerId}
              disabled={!content}
              aria-expanded={open}
              aria-controls={panelId}
              className="flex w-full items-center gap-2 px-0.5 py-2.5 text-left text-sm font-medium transition-colors hover:text-rose disabled:cursor-default disabled:hover:text-inherit"
              onClick={() => {
                setOpenSegments((current) => {
                  const next = new Set(current)
                  if (next.has(seg.key)) next.delete(seg.key)
                  else next.add(seg.key)
                  return next
                })
              }}
            >
              <ChevronRight
                className={cn(
                  'h-3 w-3 shrink-0 transition-transform duration-300 ease-smooth motion-reduce:transition-none',
                  open && 'rotate-90',
                  !content && 'opacity-35',
                )}
                aria-hidden
              />
              {seg.title}
              <span className="ml-auto inline-flex items-center gap-1.5 font-normal text-muted-foreground">
                <span className="tnum font-mono text-2xs">
                  {content ? `${(new Blob([content]).size / 1024).toFixed(1)} KB` : '空'}
                </span>
                {seg.body?.truncated && (
                  <span className="rounded border border-[color-mix(in_srgb,var(--amber)_35%,transparent)] px-[5px] font-mono text-2xs text-amber">
                    已截断
                  </span>
                )}
              </span>
            </button>
            <div
              id={panelId}
              role="region"
              aria-labelledby={triggerId}
              aria-hidden={!open}
              className={cn(
                'grid transition-[grid-template-rows,opacity] duration-300 ease-smooth motion-reduce:transition-none',
                open ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0',
              )}
            >
              <div className="min-h-0 overflow-hidden">
                {content && (
                  <pre
                    className="mb-3.5 max-h-[clamp(300px,42vh,560px)] overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3.5 py-3 font-mono text-xs leading-[1.7]"
                    dangerouslySetInnerHTML={{ __html: colorize(pretty) }}
                  />
                )}
                {open && assets.length > 0 && <AssetGallery assets={assets} />}
              </div>
            </div>
          </div>
        )
      })}
    </>
  )
}

/**
 * 把链路内容格式化为带换行的可读文本：
 * - 整体是 JSON → 缩进美化；
 * - SSE 流（多行 data: 事件）→ 逐事件美化其 JSON，事件间空行分隔；
 * - 其它 → 原文返回。
 */
function prettyPrintBody(content: string): string {
  if (!content) return ''
  const parsed = tryParseJSON(content)
  if (typeof parsed !== 'string') {
    return JSON.stringify(parsed, null, 2)
  }
  if (content.includes('data:')) {
    const blocks: string[] = []
    for (const rawLine of content.split('\n')) {
      const line = rawLine.trim()
      if (!line.startsWith('data:')) continue
      const payload = line.slice('data:'.length).trim()
      if (!payload || payload === '[DONE]') {
        blocks.push(payload || '')
        continue
      }
      const ev = tryParseJSON(payload)
      blocks.push(typeof ev === 'string' ? ev : JSON.stringify(ev, null, 2))
    }
    if (blocks.length > 0) return blocks.join('\n\n')
  }
  return content
}

/** 把详情重组为带标签的导出结构：总览 + 四段链路（请求体尽量解析为对象）+ 原始记录。 */
function buildExportPayload(detail: UsageLogDetail) {
  const seg = (b: UsageBody | undefined) => ({
    content: tryParseJSON(b?.content ?? ''),
    truncated: b?.truncated ?? false,
  })
  // 外置媒体引用清单：正文内是占位符，导出时列出可回查的文件路径。
  const assets = extractAssetRefs(
    [detail.incomingBody, detail.outgoingBody, detail.providerResponse, detail.downstreamResponse]
      .map((b) => b?.content ?? '')
      .join('\n'),
  )
  return {
    overview: {
      requestId: detail.requestId,
      api: detail.platform,
      groupName: detail.groupName,
      modelName: detail.modelName,
      conversion: {
        from: detail.sourceFormat || detail.inputFormat || '',
        to: detail.targetFormat || detail.platform || '',
        chain: detail.conversionChain ?? [],
      },
      stream: detail.stream,
      statusCode: detail.statusCode,
      error: detail.error ?? '',
      errorKind: detail.errorKind ?? '',
      retryCount: detail.retryCount,
      retryEvents: detail.retryEvents ?? [],
      firstByteMs: detail.firstByteMs,
      durationMs: detail.durationMs,
      usage: detail.usage,
      usageDetail: detail.usageDetail,
      startedAt: detail.startedAt,
      endedAt: detail.endedAt,
    },
    chain: {
      downstreamRequest: seg(detail.incomingBody),
      backendForward: seg(detail.outgoingBody),
      upstreamResponse: seg(detail.providerResponse),
      downstreamResponse: seg(detail.downstreamResponse),
    },
    // 媒体文件可经 GET /api/admin/usage/assets/<requestId>/<file> 回查。
    assets: assets.map((a) => `${a.requestId}/${a.file}`),
    raw: detail,
  }
}

function errorKindLabel(kind: string): string {
  switch (kind) {
    case 'conversion':
      return '协议转换失败'
    case 'upstream':
      return '上游失败'
    default:
      return kind
  }
}
