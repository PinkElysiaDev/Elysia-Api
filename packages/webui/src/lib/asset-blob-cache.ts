import { api } from '@/lib/api'

/**
 * 外置媒体 blob 的模块级 LRU 缓存。
 *
 * 背景：详情页的媒体（图片缩略图、大图弹窗、音视频播放器）都需经管理
 * API 取 blob（<img> 无法附带 Bearer 头）。无缓存时同一条记录里同一资
 * 产会被缩略图与弹窗各取一次，且反复开合链路段/详情抽屉会整段重新拉
 * 取——20 张 5MB 的图片一次就是 ~100MB 的重复流量与内存。
 *
 * 语义：
 * - 以 `requestId/file` 为键，命中返回同一 objectURL（多个 <img>/<a>
 *   可共享同一 URL，浏览器只持有一份 blob）；
 * - 并发去重：同键在途请求只发一次；
 * - LRU 淘汰：总字节超上限或条目超上限时按 lastUsed 逐出并 revoke；
 * - URL 生命周期归缓存所有——组件不得自行 revoke（被淘汰的 URL 若仍
 *   被挂载中的 <img> 引用会显示为破图，属可接受的极端边界）。
 */

interface AssetCacheEntry {
  url: string
  bytes: number
  lastUsed: number
}

/** 缓存上限：总字节 128MB / 条目 96（LRU 双限先到先淘汰）。 */
const MAX_TOTAL_BYTES = 128 * 1024 * 1024
const MAX_ENTRIES = 96

const entries = new Map<string, AssetCacheEntry>() // Map 迭代序 = 插入序，触达时 delete+set 移到末尾
const inflight = new Map<string, Promise<string>>()
let totalBytes = 0

export function cachedAssetUrl(asset: { requestId: string; file: string }): Promise<string> {
  const key = `${asset.requestId}/${asset.file}`
  const hit = entries.get(key)
  if (hit) {
    touch(key, hit)
    return Promise.resolve(hit.url)
  }
  const pending = inflight.get(key)
  if (pending) return pending

  const fetch = api
    .usageAssetBlob(asset.requestId, asset.file)
    .then((blob) => {
      const url = URL.createObjectURL(blob)
      // 同键可能已在等待中被写入（理论上不会，防御重复写）。
      const existing = entries.get(key)
      if (existing) {
        URL.revokeObjectURL(url)
        touch(key, existing)
        return existing.url
      }
      entries.set(key, { url, bytes: blob.size, lastUsed: Date.now() })
      totalBytes += blob.size
      evictIfNeeded()
      return url
    })
    .finally(() => {
      inflight.delete(key)
    })
  inflight.set(key, fetch)
  return fetch
}

/** 已缓存条目的字节大小；未命中返回 null。仅供 UI 展示（媒体卡片上的
 * 文件体积），不会触发加载。 */
export function cachedAssetBytes(asset: { requestId: string; file: string }): number | null {
  const hit = entries.get(`${asset.requestId}/${asset.file}`)
  return hit ? hit.bytes : null
}

function touch(key: string, entry: AssetCacheEntry) {
  entry.lastUsed = Date.now()
  entries.delete(key)
  entries.set(key, entry)
}

function evictIfNeeded() {
  while ((totalBytes > MAX_TOTAL_BYTES || entries.size > MAX_ENTRIES) && entries.size > 1) {
    // Map 首位即最久未触达（每次触达都重插到末尾）。
    const [oldestKey, oldest] = entries.entries().next().value as [string, AssetCacheEntry]
    entries.delete(oldestKey)
    totalBytes -= oldest.bytes
    URL.revokeObjectURL(oldest.url)
  }
}
