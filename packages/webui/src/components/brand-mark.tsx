import { cn } from '@/lib/utils'

/** 侧栏与登录页共用的品牌块：彩色 logo + 名称。 */
export function BrandMark({
  size = 'nav',
  className,
}: {
  size?: 'nav' | 'login'
  className?: string
}) {
  // 登录页变体：仅一行「Elysia API Console」文字（logo 以背景刻印形式
  // 由登录页另行铺陈），与表单卡片同一中轴线。
  if (size === 'login') {
    return (
      <div className={cn('flex justify-center', className)}>
        <div className="flex items-baseline gap-2.5">
          <b className="font-display text-xl font-semibold leading-[1.2] tracking-[0.02em]">
            Elysia API
          </b>
          <span className="text-2xs uppercase tracking-[0.32em] text-muted-foreground">
            Console
          </span>
        </div>
      </div>
    )
  }
  return (
    <div className={cn('flex items-center gap-[11px]', className)}>
      <img
        src={`${import.meta.env.BASE_URL}logo-color.png`}
        alt="Elysia 徽标"
        className="h-[34px] w-auto drop-shadow-[0_2px_6px_var(--halo-a)]"
      />
      <div className="min-w-0">
        <b className="block font-display font-semibold text-lg leading-[1.2] tracking-[0.02em]">
          Elysia API
        </b>
        <span className="block text-2xs uppercase tracking-[0.1em] text-muted-foreground">
          Console
        </span>
      </div>
    </div>
  )
}
