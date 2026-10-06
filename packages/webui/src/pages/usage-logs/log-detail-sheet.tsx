
import { Fragment, useEffect, useState } from 'react'
import {
  AlertTriangle,
  Download,
  MoveRight,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetSectionTitle, SheetTitle } from '@/components/ui/sheet'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { useSources } from '@/lib/hooks'
import { protocolLabel } from '@/lib/protocol'
import type { UsageLogDetail } from '@/lib/types'
import { downloadJSON, formatCacheCreationTokens, formatDateTime, formatDuration, formatNumber } from '@/lib/utils'
import { ChainBodies } from './log-detail/body-sections'
import { buildExportPayload, errorKindLabel } from './log-detail/body-helpers'

export function LogDetailSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const toast = useToast()
  const { data: sources } = useSources()
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

  const internal = detail?.relayMode === 'agent-assist'
  const targetProtocol = protocolLabel(detail?.targetFormat || detail?.platform || '', 'long')
  const protocols = [...new Set([
    internal ? '' : protocolLabel(detail?.sourceFormat || detail?.inputFormat || '', 'long'),
    targetProtocol,
  ].filter(Boolean))]
  const sourceName = sources?.find((source) => source.id === detail?.sourceId)?.name || detail?.sourceId
  const usage = detail?.usage
  // tokbar 四段：缓存命中 rose-soft / 未命中输入 rose / 缓存创建 amber / 输出 jade。
  // 缓存创建为上游独有语义，缺省（未上报）不入柱、不显示 0。
  const cacheHit = usage?.cacheHitTokens ?? 0
  const inputTotal = usage?.inputTokens ?? 0
  const inputMiss = Math.max(inputTotal - cacheHit, 0)
  const output = usage?.outputTokens ?? 0
  const creationReported = usage?.cacheCreationTokens !== undefined
  const creation = creationReported ? usage?.cacheCreationTokens ?? 0 : 0
  const tokTotal = cacheHit + inputMiss + creation + output
  // C28 分桶省略但总量保留：目标协议无 TTL 分桶字段时后端记 warning，此处显式说明避免像丢数据。
  const creationBucketOmitted = (detail?.conversionIssues ?? []).some(
    (issue) => issue.reason?.includes('cache creation bucket omitted'),
  )
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
                    {detail.statusCode === 499 ? '客户端取消（499）' : `调用失败（${detail.statusCode}）`}
                    {detail.errorKind && detail.errorKind !== 'client_canceled' && ` · ${errorKindLabel(detail.errorKind)}`} · {detail.error}
                  </span>
                </div>
              )}

              <section className="mb-5">
                <SheetSectionTitle>协议链路</SheetSectionTitle>
                <div className="flex flex-wrap items-center gap-[7px]">
                  {protocols.map((protocol, index) => (
                    <Fragment key={protocol}>
                      {index > 0 && <MoveRight className="h-3 w-3 text-muted-foreground" aria-hidden />}
                      <span className="max-w-full break-all rounded-full border border-input bg-card px-2.5 py-[3px] font-mono text-2xs text-muted-foreground">
                        {protocol}
                      </span>
                    </Fragment>
                  ))}
                  {(internal || (!detail.modelName && !targetProtocol)) && (
                    <span className="text-2xs text-muted-foreground">{internal ? '内部调用' : '未转发'}</span>
                  )}
                </div>
                <dl className="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-[7px] text-xs">
                  <dt className="whitespace-nowrap text-muted-foreground">调用方</dt>
                  <dd className="tnum min-w-0 break-all">{detail.keyName || '—'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">请求模型</dt>
                  <dd className="min-w-0 break-all font-mono">{detail.requestedModelGroup || '—'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">实际模型</dt>
                  <dd className="min-w-0 break-all font-mono">{detail.modelName || '未路由'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">模型源</dt>
                  <dd className="min-w-0 break-all">{sourceName || '—'}</dd>
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
                    首字 <b className="font-semibold text-foreground">{detail.firstByteMs > 0 ? formatDuration(detail.firstByteMs) : '—'}</b>
                  </span>
                  <span>
                    总耗时 <b className="font-semibold text-foreground">{detail.durationMs > 0 ? formatDuration(detail.durationMs) : '—'}</b>
                  </span>
                  {outputRate && (
                    <span>
                      输出速率 <b className="font-semibold text-foreground">{outputRate} tok/s</b>
                    </span>
                  )}
                </p>
                {tokTotal > 0 ? (
                  <>
                    <div className="my-2 flex h-2 overflow-hidden rounded-full bg-border">
                      {cacheHit > 0 && <i className="h-full" style={{ width: `${(cacheHit / tokTotal) * 100}%`, background: 'var(--rose-soft)' }} />}
                      {inputMiss > 0 && <i className="h-full" style={{ width: `${(inputMiss / tokTotal) * 100}%`, background: 'var(--rose)' }} />}
                      {creation > 0 && <i className="h-full" style={{ width: `${(creation / tokTotal) * 100}%`, background: 'var(--amber)' }} />}
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
                      <span className={creationReported ? undefined : 'text-muted-foreground/70'}>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--amber)' }} />
                        缓存创建 {formatCacheCreationTokens(usage?.cacheCreationTokens)}
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
                {creationBucketOmitted && (
                  <p className="mt-2 flex items-start gap-1.5 text-2xs text-amber">
                    <AlertTriangle className="mt-px h-3 w-3 shrink-0" aria-hidden />
                    <span>上游按 TTL 分桶上报的缓存创建明细已省略（目标协议无对应字段）；缓存创建总量仍保留，见上方「缓存创建」。</span>
                  </p>
                )}
              </section>

              {detail.retryCount > 0 && !!detail.retryEvents?.length && (
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

              {detail.systemStructure && <details className="mb-5 rounded-md border p-3 text-xs">
                <summary className="cursor-pointer">System 结构诊断 · 缓存合成{detail.cacheSynthesis ? '开启' : '关闭'}</summary>
                <p className="mt-2 break-all">目标协议：{detail.targetFormat} · 修订：{detail.upstreamRevision}</p>
                <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap">{JSON.stringify(detail.systemStructure, null, 2)}</pre>
              </details>}
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
                <Button variant="primary" onClick={handleExport}>
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
