import { cn } from '@/lib/utils'

/** 品牌 Logo：粉色渐变方块 + 花蕊图形，呼应整体粉色调。 */
export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2.5', className)}>
      <span className="relative flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-[hsl(330_86%_70%)] to-[hsl(333_71%_51%)] shadow-soft">
        <svg viewBox="0 0 24 24" className="h-5 w-5 text-white" fill="currentColor" aria-hidden>
          <path d="M12 4c-.9 2.6-2.6 4.3-5.2 5.2C9.4 10.1 11.1 11.8 12 14.4c.9-2.6 2.6-4.3 5.2-5.2C14.6 8.3 12.9 6.6 12 4Z" />
          <circle cx="17" cy="17" r="2" opacity="0.85" />
        </svg>
      </span>
      <span className="flex flex-col leading-tight">
        <span className="text-sm font-semibold tracking-tight">Elysia API</span>
        <span className="text-[11px] text-muted-foreground">控制台</span>
      </span>
    </span>
  )
}
