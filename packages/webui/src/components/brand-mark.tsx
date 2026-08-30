import { cn } from '@/lib/utils'

/** 侧栏与登录页共用的品牌块：彩色 logo + 名称。 */
export function BrandMark({
  size = 'nav',
  className,
}: {
  size?: 'nav' | 'login'
  className?: string
}) {
  return (
    <div className={cn('flex items-center gap-[11px]', className)}>
      <img
        src={`${import.meta.env.BASE_URL}logo-color.png`}
        alt="Elysia 徽标"
        className={cn(
          'w-auto drop-shadow-[0_2px_6px_var(--halo-a)]',
          size === 'login' ? 'h-[44px]' : 'h-[34px]',
        )}
      />
      <div className="min-w-0">
        <b
          className={cn(
            'block font-display font-semibold leading-[1.2] tracking-[0.02em]',
            size === 'login' ? 'text-xl' : 'text-lg',
          )}
        >
          Elysia API
        </b>
        <span className="block text-2xs uppercase tracking-[0.1em] text-muted-foreground">
          Console
        </span>
      </div>
    </div>
  )
}
