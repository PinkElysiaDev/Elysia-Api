
import { useEffect, useState } from 'react'
import {
  AlertTriangle,
  Download,
  MoveRight,
} from 'lucide-react'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetSectionTitle, SheetTitle } from '@/components/ui/sheet'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { protocolLabel } from '@/lib/protocol'
import type { UsageLogDetail } from '@/lib/types'
import { cn, downloadJSON, formatDateTime, formatDuration, formatNumber } from '@/lib/utils'
import { ChainBodies } from './log-detail/body-sections'
import { buildExportPayload, errorKindLabel } from './log-detail/body-helpers'

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
                  <span className="max-w-full break-all rounded-full border border-input bg-card px-2.5 py-[3px] font-mono text-2xs text-muted-foreground">
                    {protocolLabel(detail.sourceFormat || detail.inputFormat || '', 'long') || '—'}
                  </span>
                  <MoveRight className="h-3 w-3 text-muted-foreground" aria-hidden />
                  <span
                    className={cn(
                      'max-w-full break-all rounded-full border px-2.5 py-[3px] font-mono text-2xs',
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
