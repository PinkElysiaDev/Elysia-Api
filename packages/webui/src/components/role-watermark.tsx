import { cn } from '@/lib/utils'

/** 登录 / 总览共用的立绘水印：作为低饱和度品牌暗纹，不抢夺前景信息。 */
export function RoleWatermark({ className }: { className?: string }) {
  const src = `${import.meta.env.BASE_URL}role-mask.png`
  return (
    <div
      aria-hidden
      className={cn(
        'pointer-events-none absolute z-0 w-[240px] select-none opacity-[0.05] transition-opacity dark:opacity-[0.07] rail:w-[480px]',
        className,
      )}
    >
      <div
        className="role-fig aspect-[760/808] w-full [mask-image:radial-gradient(ellipse_at_center,black_40%,transparent_80%)]"
        style={{
          WebkitMaskImage: `url(${src})`,
          maskImage: `url(${src})`,
        }}
      />
    </div>
  )
}
