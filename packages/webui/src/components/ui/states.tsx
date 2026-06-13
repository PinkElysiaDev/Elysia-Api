import type { ReactNode } from 'react'
import { AlertCircle, Inbox, RefreshCw } from 'lucide-react'
import { Button } from './button'
import { TableSkeleton } from './skeleton'
import { cn } from '@/lib/utils'

interface StateProps {
  className?: string
}

/** 空状态。 */
export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: StateProps & {
  icon?: ReactNode
  title: string
  description?: string
  action?: ReactNode
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-3 px-6 py-16 text-center', className)}>
      <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary">
        {icon ?? <Inbox className="h-7 w-7" />}
      </span>
      <div className="space-y-1">
        <p className="text-base font-semibold">{title}</p>
        {description && <p className="max-w-sm text-sm text-muted-foreground">{description}</p>}
      </div>
      {action}
    </div>
  )
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
      <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-destructive/12 text-destructive">
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
 */
export function AsyncState<T>({
  isLoading,
  error,
  data,
  onRetry,
  emptyTitle,
  emptyDescription,
  emptyAction,
  emptyIcon,
  loadingRows,
  loadingColumns,
  children,
}: {
  isLoading: boolean
  error: unknown
  data: T[] | undefined
  onRetry?: () => void
  emptyTitle: string
  emptyDescription?: string
  emptyAction?: ReactNode
  emptyIcon?: ReactNode
  loadingRows?: number
  loadingColumns?: number
  children: (data: T[]) => ReactNode
}) {
  if (isLoading && !data) return <LoadingState rows={loadingRows} columns={loadingColumns} />
  if (error) return <ErrorState message={(error as Error)?.message} onRetry={onRetry} />
  if (!data || data.length === 0)
    return <EmptyState title={emptyTitle} description={emptyDescription} action={emptyAction} icon={emptyIcon} />
  return <>{children(data)}</>
}
