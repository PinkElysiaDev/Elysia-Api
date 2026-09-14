import { ChevronLeft, ChevronRight, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { formatNumber } from '@/lib/utils'

/** 统一分页栏：记录数 + 页码 + 上/下页。onNavigate 供调用方挂滚动复位等副作用。 */
export function PaginationBar({
  total,
  page,
  totalPages,
  onNavigate,
  unitLabel = '条记录',
  loading = false,
}: {
  total: number
  page: number
  totalPages: number
  onNavigate?: (next: number) => void
  unitLabel?: string
  loading?: boolean
}) {
  const go = (next: number) => onNavigate?.(next)
  return (
    <nav aria-label="分页" className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 pt-3 text-xs text-muted-foreground border-t border-border/40">
      <span className="tnum font-mono" aria-live="polite" aria-atomic="true">
        共 <b className="font-semibold text-foreground">{formatNumber(total)}</b> {unitLabel} · 第 {page + 1}/{totalPages} 页
      </span>
      <div className="flex items-center gap-2">
        {loading && <span role="status" className="inline-flex items-center gap-1.5"><Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" />正在加载…</span>}
        <Button variant="outline" size="sm" disabled={loading || page === 0} onClick={() => go(page - 1)}>
          <ChevronLeft className="h-3.5 w-3.5" /> 上一页
        </Button>
        <Button variant="outline" size="sm" disabled={loading || page >= totalPages - 1} onClick={() => go(page + 1)}>
          下一页 <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </nav>
  )
}
