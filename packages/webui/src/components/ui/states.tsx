import type { ReactNode } from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { Button } from './button'
import { TableSkeleton } from './skeleton'
import { cn } from '@/lib/utils'

interface StateProps {
  className?: string
}

/** 空状态：无图标、无说明长文，display 衬线大字间距文案；胶囊操作随鼠标浮现。 */
export function EmptyState({
  title,
  action,
  className,
}: StateProps & {
  title: string
  action?: ReactNode
}) {
  return (
    <div className={cn('group flex min-h-[55vh] flex-col items-center justify-center gap-8 px-6 py-16 text-center', className)}>
      {/* pl 补偿末字符字间距，保证视觉居中 */}
      <p className="pl-[0.24em] font-display text-lg font-medium tracking-[0.24em] text-muted-foreground/80">{title}</p>
      {action && (
        <div className="translate-y-1 opacity-0 transition-[opacity,transform] duration-200 group-hover:translate-y-0 group-hover:opacity-100 group-focus-within:translate-y-0 group-focus-within:opacity-100">
          {action}
        </div>
      )}
    </div>
  )
}

/** 卡片/表格内的紧凑空文案：与页面级 EmptyState 同语言（宽字间距、无图标）。 */
export function EmptyText({ children, className }: { children: ReactNode; className?: string }) {
  return <p className={cn('pl-[0.18em] text-xs tracking-[0.18em] text-muted-foreground/70', className)}>{children}</p>
}

/** 错误状态，附带重试。 */
export function ErrorState({
  title = '加载失败',
  message,
  onRetry,
  className,
}: StateProps & {
  title?: string
  message?: string
  onRetry?: () => void
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-3 px-6 py-16 text-center', className)}>
      <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-destructive/10 text-destructive">
        <AlertCircle className="h-7 w-7" />
      </span>
      <div className="space-y-1">
        <p className="text-base font-semibold">{title}</p>
        {message && <p className="max-w-md text-sm text-muted-foreground">{message}</p>}
      </div>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw className="h-4 w-4" />
          重试
        </Button>
      )}
    </div>
  )
}

/** loading 状态。 */
export function LoadingState({ rows, columns }: { rows?: number; columns?: number }) {
  return <TableSkeleton rows={rows} columns={columns} />
}

/**
 * 统一的列表三态封装：loading / error / empty / content。
 * 不包卡片壳——三态都以居中信息直接呈现，与无卡片的内容态表格一致。
 */
export function AsyncState<T>({
  isLoading,
  error,
  data,
  onRetry,
  emptyTitle,
  emptyAction,
  loadingRows,
  loadingColumns,
  children,
}: {
  isLoading: boolean
  error: unknown
  data: T[] | undefined
  onRetry?: () => void
  emptyTitle: string
  emptyAction?: ReactNode
  loadingRows?: number
  loadingColumns?: number
  children: (data: T[]) => ReactNode
}) {
  if (isLoading && !data) return <LoadingState rows={loadingRows} columns={loadingColumns} />
  if (error) return <ErrorState message={(error as Error)?.message} onRetry={onRetry} />
  if (!data || data.length === 0) return <EmptyState title={emptyTitle} action={emptyAction} />
  return <>{children(data)}</>
}
