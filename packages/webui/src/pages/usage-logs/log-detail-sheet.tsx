
import { Fragment, useEffect, useState } from 'react'
import {
  AlertTriangle,
  Info,
  Download,
  MoveRight,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetSectionTitle, SheetTitle } from '@/components/ui/sheet'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { useEnabledProtocols, useSources } from '@/lib/hooks'
import { protocolID } from '@/lib/protocol'
import type { UsageLogDetail } from '@/lib/types'
import { downloadJSON, formatDateTime, formatDuration } from '@/lib/utils'
import { ChainBodies } from './log-detail/body-sections'
import { buildExportPayload, errorKindLabel } from './log-detail/body-helpers'
import { UsageTokens } from './log-detail/usage-tokens'

export function LogDetailSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const toast = useToast()
  const { data: sources } = useSources()
  const { data: registeredProtocols = [] } = useEnabledProtocols(!!id)
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
  const targetProtocol = protocolID(detail?.targetFormat || detail?.platform || '', registeredProtocols)
  const protocols = [...new Set([
    internal ? '' : protocolID(detail?.sourceFormat || detail?.inputFormat || '', registeredProtocols),
    targetProtocol,
  ].filter(Boolean))]
  const sourceName = sources?.find((source) => source.id === detail?.sourceId)?.name || detail?.sourceId
  const usage = detail?.usage
  const output = usage?.outputTokens
  const creationBucketOmitted = (detail?.conversionIssues ?? []).some((issue) =>
    issue.code === 'conversion_degraded' && issue.fidelity === 'lossy_compatible'
      && (issue.ruleId === 'response-usage-projection' || issue.ruleId === 'event-usage-projection')
      && /\/usage\/details\/ephemeral_(5m|1h)_input_tokens$/.test(issue.path),
  )
  const conversionIssues = detail?.conversionIssues ?? []
  const outputRate =
    detail && output != null && output > 0 && detail.durationMs > 0 ? (output / (detail.durationMs / 1000)).toFixed(1) : null

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
                      <span title={protocol} className="max-w-full break-all rounded-full border border-input bg-card px-2.5 py-[3px] font-mono text-2xs text-muted-foreground">
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
                <UsageTokens usage={detail.usage} />
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

              {conversionIssues.length > 0 && (
                <details className="mb-5 rounded-md border p-3 text-xs" aria-label="转换诊断">
                  <summary className="cursor-pointer">
                    转换诊断 · {conversionIssues.length} 条
                    <span className="ml-2 text-muted-foreground">
                      {([
                        ['error', '错误', 'text-destructive'],
                        ['warning', '警告', 'text-amber'],
                        ['info', '信息', ''],
                      ] as const)
                        .filter(([level]) => conversionIssues.some((issue) => issue.severity === level))
                        .map(([level, label, tone]) => (
                          <span key={level} className={`ml-1.5 ${tone}`}>
                            {conversionIssues.filter((issue) => issue.severity === level).length} {label}
                          </span>
                        ))}
                    </span>
                  </summary>
                  <ul className="mt-3 space-y-3">
                    {conversionIssues.map((issue, index) => (
                      <li key={index} className="break-all rounded-md border p-3">
                        <p className={`flex items-center gap-1.5 ${issue.severity === 'error' ? 'text-destructive' : issue.severity === 'warning' ? 'text-amber' : 'text-muted-foreground'}`}>
                          {issue.severity === 'info' ? <Info className="h-3 w-3 shrink-0" aria-hidden /> : <AlertTriangle className="h-3 w-3 shrink-0" aria-hidden />}
                          {issue.severity === 'info' ? '信息' : issue.severity === 'error' ? '错误' : '警告'} · {issue.code} · {issue.path}
                        </p>
                        <p className="mt-1">{issue.reason}</p>
                        <p className="mt-1 text-muted-foreground">阶段：{issue.stage ?? '—'} · 规则：{issue.ruleId ?? '—'} · 结果：{issue.fidelity ?? '—'}</p>
                        {issue.protocol?.definitionId && <p className="mt-1 text-muted-foreground">协议：{issue.protocol.definitionId} · 修订：{issue.protocol.revision}</p>}
                        {issue.policyHash && <p className="mt-1 text-muted-foreground">策略：{issue.policyHash}</p>}
                      </li>
                    ))}
                  </ul>
                </details>
              )}

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
