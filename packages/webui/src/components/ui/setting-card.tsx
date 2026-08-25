import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

export interface SettingCardProps {
  title: ReactNode
  description?: ReactNode
  icon?: LucideIcon
  badge?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}

/**
 * 统一设置卡片容器：用于将零散的配置选项聚合为结构清晰的模块。
 */
export function SettingCard({
  title,
  description,
  icon: Icon,
  badge,
  action,
  children,
  className,
}: SettingCardProps) {
  return (
    <Card className={cn('overflow-hidden shadow-soft transition-shadow hover:shadow-md', className)}>
      <CardHeader className="border-b border-border/70 bg-secondary/30 pb-4 pt-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2.5">
            {Icon && (
              <span className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-card text-rose shadow-sm">
                <Icon className="h-4 w-4" />
              </span>
            )}
            <div>
              <div className="flex items-center gap-2">
                <CardTitle className="text-[15px] font-semibold tracking-tight text-foreground">{title}</CardTitle>
                {badge}
              </div>
              {description && (
                <CardDescription className="mt-0.5 text-xs text-muted-foreground">{description}</CardDescription>
              )}
            </div>
          </div>
          {action && <div className="flex items-center gap-2">{action}</div>}
        </div>
      </CardHeader>
      <CardContent className="p-5 sm:p-6">{children}</CardContent>
    </Card>
  )
}

export interface SettingRowProps {
  label: ReactNode
  description?: ReactNode
  children: ReactNode
  /** 左右横排布局 (适合 Switch, Select, 短输入框)；false 为上下纵排 (适合多行或宽表单) */
  inline?: boolean
  required?: boolean
  className?: string
}

/**
 * 统一设置行：规范左侧 Label/说明 与 右侧 Control 的对齐与层级。
 */
export function SettingRow({
  label,
  description,
  children,
  inline = true,
  required,
  className,
}: SettingRowProps) {
  if (inline) {
    return (
      <div
        className={cn(
          'flex flex-col gap-3 py-3.5 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between',
          'border-b border-border/50 last:border-b-0',
          className,
        )}
      >
        <div className="max-w-md space-y-0.5">
          <label className="text-sm font-medium text-foreground">
            {label}
            {required && <span className="ml-1 text-destructive">*</span>}
          </label>
          {description && <p className="text-xs leading-relaxed text-muted-foreground">{description}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-2 sm:justify-end">{children}</div>
      </div>
    )
  }

  return (
    <div
      className={cn(
        'space-y-2 py-3.5 first:pt-0 last:pb-0 border-b border-border/50 last:border-b-0',
        className,
      )}
    >
      <div className="space-y-0.5">
        <label className="text-sm font-medium text-foreground">
          {label}
          {required && <span className="ml-1 text-destructive">*</span>}
        </label>
        {description && <p className="text-xs leading-relaxed text-muted-foreground">{description}</p>}
      </div>
      <div className="pt-1">{children}</div>
    </div>
  )
}
