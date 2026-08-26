import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface SettingSectionProps {
  title: ReactNode
  description?: ReactNode
  icon?: LucideIcon
  badge?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}

export type SettingCardProps = SettingSectionProps

/**
 * 无界配置章节：开放式排版、极细晶辉发丝下划线、彻底摒弃灰色表头与四周封闭卡片盒。
 */
export function SettingSection({
  title,
  description,
  icon: Icon,
  badge,
  action,
  children,
  className,
}: SettingSectionProps) {
  return (
    <section className={cn('space-y-4 pt-1', className)}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border/50 pb-3.5">
        <div className="flex items-center gap-2.5">
          {Icon && <Icon className="h-4 w-4 text-primary shrink-0" />}
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold tracking-tight text-foreground">{title}</h3>
              {badge}
            </div>
            {description && (
              <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
            )}
          </div>
        </div>
        {action && <div className="flex items-center gap-2">{action}</div>}
      </div>
      <div className="pt-1">{children}</div>
    </section>
  )
}

/** 兼容旧名称 */
export const SettingCard = SettingSection

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
 * 统一设置行：规范左侧 Label/说明 与 右侧 Control 的对齐与层级，使用发丝细线分隔。
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
          'border-b border-border/30 last:border-b-0',
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
        'space-y-2 py-3.5 first:pt-0 last:pb-0 border-b border-border/30 last:border-b-0',
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
