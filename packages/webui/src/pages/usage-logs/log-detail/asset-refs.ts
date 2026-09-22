// 资产引用的纯逻辑层：引用解析、类型判定与对象 URL 钩子。
import { useEffect, useState } from 'react'
import { FileText, Film, Image as ImageIcon, Music2, type LucideIcon } from 'lucide-react'
import { cachedAssetBytes, cachedAssetUrl } from '@/lib/asset-blob-cache'

/** 占位符形态：__ELYSIA_ASSET__:<requestId>/<hash16>.<ext> */
export interface AssetRef {
  requestId: string
  file: string
}

export const ASSET_REF_PATTERN = /__ELYSIA_ASSET__:([\w-]+)\/([0-9a-f]{16}\.[a-z0-9]{1,5})/g

export type AssetKind = 'image' | 'audio' | 'video' | 'file'

export const KIND_META: Record<AssetKind, { icon: LucideIcon; label: string }> = {
  image: { icon: ImageIcon, label: '图片' },
  audio: { icon: Music2, label: '音频' },
  video: { icon: Film, label: '视频' },
  file: { icon: FileText, label: '文件' },
}

export function assetKind(ext: string): AssetKind {
  if (['png', 'jpg', 'gif', 'webp'].includes(ext)) return 'image'
  if (['mp3', 'wav', 'ogg'].includes(ext)) return 'audio'
  if (['mp4', 'webm'].includes(ext)) return 'video'
  return 'file'
}

export function assetKindOf(asset: AssetRef): AssetKind {
  return assetKind(asset.file.split('.').pop() ?? '')
}

/** 从一段链路原文中提取外置媒体引用（去重，保持出现顺序）。 */
export function extractAssetRefs(content: string): AssetRef[] {
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

export function useAssetObjectUrl(asset: AssetRef | null, reloadNonce = 0) {
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
      .then((cached: string | null) => {
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
