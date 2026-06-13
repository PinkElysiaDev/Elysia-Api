import type { ReactNode } from 'react'
import { Card } from './ui/card'
import { cn } from '@/lib/utils'

export function StatCard({
  label,
  value,
  hint,
  icon,
  accent,
  className,
}: {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: boolean
  className?: string
}) {
  return (
    <Card className={cn('relative overflow-hidden p-5', className)}>
      {accent && (
        <span className="pointer-events-none absolute -right-8 -top-8 h-24 w-24 rounded-full bg-primary/10 blur-2xl" />
      )}
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1.5">
          <p className="text-sm font-medium text-muted-foreground">{label}</p>
          <p className="text-2xl font-semibold tracking-tight">{value}</p>
          {hint && <div className="text-xs text-muted-foreground">{hint}</div>}
        </div>
        {icon && (
          <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
            {icon}
          </span>
        )}
      </div>
    </Card>
  )
}
