// 资产卡片：图片卡（重试/放大入口）与非图片媒体的按需加载卡。
import { useState } from 'react'
import {
  AlertTriangle,
  Download,
  Image as ImageIcon,
  Loader2,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn, formatBytes } from '@/lib/utils'
import { KIND_META, assetKindOf, useAssetObjectUrl, type AssetRef } from './asset-refs'

export function AssetImageCard({
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
export function AssetMediaCard({ asset }: { asset: AssetRef }) {
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
