// 外置媒体灯箱：blob 资产的懒解析（useAssetObjectUrl）+ 相邻预加载 + 底部
// 缩略图条，交互核心复用共享的 LightboxStage。
import { useEffect, useMemo, useRef, useState } from 'react'
import { cachedAssetUrl } from '@/lib/asset-blob-cache'
import { cn } from '@/lib/utils'
import { LightboxStage } from '@/components/lightbox-stage'
import { KIND_META, assetKindOf, useAssetObjectUrl, type AssetKind, type AssetRef } from './asset-refs'
import { AssetMediaCard, AssetImageCard } from './asset-cards'

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

/** 日志详情的图片灯箱：解析当前图 + 预加载相邻图，舞台与工具栏走 LightboxStage。 */
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
  const activeThumbRef = useRef<HTMLButtonElement>(null)

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

  return (
    <LightboxStage
      open={index != null}
      src={url}
      failed={failed}
      name={asset?.file ?? ''}
      index={index}
      total={assets.length}
      onNavigate={onNavigate}
      onClose={onClose}
      bottomBar={
        <div className="hide-scrollbar pointer-events-auto flex max-w-full items-center gap-1.5 overflow-x-auto px-5 py-1">
          {assets.map((thumb, i) => (
            <LightboxThumb
              key={`${thumb.requestId}/${thumb.file}`}
              asset={thumb}
              active={i === index}
              onSelect={() => onNavigate(i)}
              innerRef={i === index ? activeThumbRef : undefined}
            />
          ))}
        </div>
      }
    />
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
