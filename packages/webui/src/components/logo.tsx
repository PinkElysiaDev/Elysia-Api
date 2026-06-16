import { cn } from '@/lib/utils'

/** 品牌 Logo：爱莉希雅白色玫瑰花徽章（基于 Elysian 图案）。 */
export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2.5', className)}>
      <span className="relative flex h-12 w-12 items-center justify-center">
        <img
          src={`${import.meta.env.BASE_URL}logo.png`}
          alt="Elysia Logo"
          className="h-12 w-12 object-contain drop-shadow-[0_0_8px_rgba(245,190,221,0.6)]"
        />
      </span>
      <span className="flex flex-col leading-tight">
        <span className="text-sm font-semibold tracking-tight">Elysia API</span>
        <span className="text-[11px] text-muted-foreground">控制台</span>
      </span>
    </span>
  )
}
