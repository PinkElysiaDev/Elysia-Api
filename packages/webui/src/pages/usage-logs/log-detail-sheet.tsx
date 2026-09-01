import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, ChevronRight, Download, FileText, Image, MoveRight } from 'lucide-react'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
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
import { colorize } from '@/lib/json-highlight'
import { protocolLabel } from '@/lib/protocol'
import type { UsageBody, UsageLogDetail } from '@/lib/types'
import { cn, downloadJSON, formatDateTime, formatDuration, formatNumber, tryParseJSON } from '@/lib/utils'

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
                      <div key={i} className="flex items-baseline gap-2.5">
                        <span className="tnum flex h-[18px] w-[18px] shrink-0 translate-y-[3px] items-center justify-center rounded-full border border-ember font-mono text-2xs text-ember">
                          {ev.attempt}
                        </span>
                        <span className="min-w-0 break-all">
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

function assetKind(ext: string): 'image' | 'audio' | 'video' | 'file' {
  if (['png', 'jpg', 'gif', 'webp'].includes(ext)) return 'image'
  if (['mp3', 'wav', 'ogg'].includes(ext)) return 'audio'
  if (['mp4', 'webm'].includes(ext)) return 'video'
  return 'file'
}

/** 单个外置媒体：点击后经管理 API 取 blob 渲染（<img> 无法带 Bearer 头）。 */
function AssetChip({ asset }: { asset: AssetRef }) {
  const ext = asset.file.split('.').pop() ?? ''
  const kind = assetKind(ext)
  const [url, setUrl] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)
  // 卸载标记：详情面板在 blob 请求途中关闭时，resolve 后不得再 setUrl——
  // 那个 objectURL 会钉住整个 blob 且无人 revoke（长会话累积泄漏）。
  const cancelledRef = useRef(false)

  useEffect(() => () => { cancelledRef.current = true }, [])

  // url 变化/卸载时回收上一个 objectURL，避免长会话内存泄漏。
  useEffect(
    () => () => {
      if (url) URL.revokeObjectURL(url)
    },
    [url],
  )

  async function load() {
    if (url || loading || failed) return
    setLoading(true)
    try {
      const blob = await api.usageAssetBlob(asset.requestId, asset.file)
      const objectUrl = URL.createObjectURL(blob)
      if (cancelledRef.current) {
        URL.revokeObjectURL(objectUrl)
        return
      }
      setUrl(objectUrl)
    } catch {
      if (!cancelledRef.current) setFailed(true)
    } finally {
      setLoading(false)
    }
  }

  const Icon = kind === 'image' ? Image : FileText
  return (
    <div className="rounded-[7px] border border-border bg-card p-2">
      <div className="flex items-center gap-2">
        <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <span className="min-w-0 truncate font-mono text-2xs text-muted-foreground" title={asset.file}>
          {asset.file}
        </span>
        {!url && (
          <Button variant="outline" size="sm" className="ml-auto shrink-0" onClick={load} disabled={loading || failed}>
            {failed ? '获取失败' : loading ? '加载中…' : '预览'}
          </Button>
        )}
        {url && kind === 'file' && (
          <a href={url} download={asset.file} className="ml-auto shrink-0 text-2xs underline text-muted-foreground">
            下载
          </a>
        )}
      </div>
      {url && kind === 'image' && (
        <a href={url} target="_blank" rel="noreferrer" className="mt-2 block" title="点击在新标签页查看原图">
          <img src={url} alt={asset.file} className="max-h-40 rounded border border-border" loading="lazy" />
        </a>
      )}
      {url && kind === 'audio' && <audio controls src={url} className="mt-2 w-full" />}
      {url && kind === 'video' && <video controls src={url} className="mt-2 max-h-56 w-full rounded border border-border" />}
    </div>
  )
}

function AssetGallery({ assets }: { assets: AssetRef[] }) {
  return (
    <div className="mb-3.5 space-y-2">
      <p className="text-2xs text-muted-foreground">
        {assets.length} 个外置媒体（base64 已抽出为独立文件，正文内保留占位符）
      </p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {assets.map((asset) => (
          <AssetChip key={`${asset.requestId}/${asset.file}`} asset={asset} />
        ))}
      </div>
    </div>
  )
}

/** 四段链路原文：受控折叠动画 + JSON/SSE 着色，头部字节数 + 截断标记。 */
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
