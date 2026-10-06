import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * 弹窗内无界小节：标题行（名称 / 补充说明 / 右侧动作）+ 发丝下划线，
 * 内容开放排布。与设置页 SettingSection 同一取向——不做描边卡盒，
 * 避免弹窗里「弹窗→小节→行→输入框」层层套盒。
 */
export function FormSection({
  title,
  hint,
  action,
  children,
  className,
}: {
  title: ReactNode
  /** 标题右侧的弱化补充（空态说明、计数等） */
  hint?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn('space-y-2.5', className)}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border/50 pb-2">
        <h4 className="text-sm font-medium text-foreground">{title}</h4>
        {hint && <span className="text-2xs text-muted-foreground">{hint}</span>}
        {action && <div className="ml-auto flex items-center gap-2">{action}</div>}
      </div>
      {children}
    </section>
  )
}
