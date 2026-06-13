import { cn } from '@/lib/utils'

export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('skeleton rounded-md', className)} {...props} />
}

/** 表格加载骨架。 */
export function TableSkeleton({ rows = 5, columns = 4 }: { rows?: number; columns?: number }) {
  return (
    <div className="space-y-3 p-1">
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="flex items-center gap-4">
          {Array.from({ length: columns }).map((_, c) => (
            <Skeleton key={c} className={cn('h-5', c === 0 ? 'w-1/4' : 'flex-1')} />
          ))}
        </div>
      ))}
    </div>
  )
}
