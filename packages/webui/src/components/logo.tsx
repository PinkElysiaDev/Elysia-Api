import { cn } from '@/lib/utils'

/** 品牌 Logo：粉色渐变方块 + 爱莉希雅花纹徽章，呼应整体粉色调。 */
export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2.5', className)}>
      <span className="relative flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-[#ff40ff] to-[#d000d0] shadow-soft">
        <svg viewBox="0 0 32 32" className="h-6 w-6 text-white" fill="currentColor" aria-hidden>
          {/* Outer spiked ring */}
          <path
            d="M16 3.5l1.2 2.8 2.5-1.5-0.3 2.9 2.9 0.3-1.5 2.5 2.8 1.2-2.4 1.8 2.4 1.8-2.8 1.2 1.5 2.5-2.9 0.3 0.3 2.9-2.5-1.5-1.2 2.8-1.2-2.8-2.5 1.5 0.3-2.9-2.9-0.3 1.5-2.5-2.8-1.2 2.4-1.8-2.4-1.8 2.8-1.2-1.5-2.5 2.9-0.3-0.3-2.9 2.5 1.5z"
            fill="none"
            stroke="currentColor"
            strokeWidth="0.6"
            opacity="0.7"
          />
          {/* Cardinal flower petals */}
          <path d="M16 8 L17.5 12.5 L16 11 L14.5 12.5 Z" />
          <path d="M16 24 L17.5 19.5 L16 21 L14.5 19.5 Z" />
          <path d="M8 16 L12.5 14.5 L11 16 L12.5 17.5 Z" />
          <path d="M24 16 L19.5 14.5 L21 16 L19.5 17.5 Z" />
          {/* Inner angular petals */}
          <path d="M16 10.5 L18.5 14 L16 13 L13.5 14 Z" opacity="0.9" />
          <path d="M16 21.5 L18.5 18 L16 19 L13.5 18 Z" opacity="0.9" />
          <path d="M10.5 16 L14 13.5 L13 16 L14 18.5 Z" opacity="0.9" />
          <path d="M21.5 16 L18 13.5 L19 16 L18 18.5 Z" opacity="0.9" />
          {/* Diagonal petals */}
          <path d="M11.5 11.5 L14 14 L12.5 13.5 L13.5 12.5 Z" opacity="0.85" />
          <path d="M20.5 11.5 L18 14 L19.5 13.5 L18.5 12.5 Z" opacity="0.85" />
          <path d="M11.5 20.5 L14 18 L12.5 18.5 L13.5 19.5 Z" opacity="0.85" />
          <path d="M20.5 20.5 L18 18 L19.5 18.5 L18.5 19.5 Z" opacity="0.85" />
          {/* Center core */}
          <circle cx="16" cy="16" r="2" opacity="0.95" />
          <circle cx="16" cy="16" r="1" fill="#e020e0" />
        </svg>
      </span>
      <span className="flex flex-col leading-tight">
        <span className="text-sm font-semibold tracking-tight">Elysia API</span>
        <span className="text-[11px] text-muted-foreground">控制台</span>
      </span>
    </span>
  )
}
