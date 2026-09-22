// 详情抽屉的正文区组件与导出/展示辅助。
import { useState } from 'react'
import {
  ChevronRight,
} from 'lucide-react'
import { colorize } from '@/lib/json-highlight'
import type { UsageBody, UsageLogDetail } from '@/lib/types'
import { cn } from '@/lib/utils'
import { AssetGallery } from './asset-lightbox'
import { extractAssetRefs } from './asset-refs'
import { prettyPrintBody } from './body-helpers'

export function ChainBodies({ detail }: { detail: UsageLogDetail }) {
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
