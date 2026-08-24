import { cn } from '@/lib/utils'

/** 登录 / 总览共用的立绘水印。定位由调用方 className 决定。 */
export function RoleWatermark({ className }: { className?: string }) {
  const src = `${import.meta.env.BASE_URL}role-mask.png`
  return (
    <div
      aria-hidden
      className={cn(
        'pointer-events-none absolute z-0 w-[240px] select-none rail:w-[520px]',
        className,
      )}
    >
      <div
        className="role-fig aspect-[760/808] w-full opacity-[0.15]"
        style={{
          WebkitMaskImage: `url(${src})`,
          maskImage: `url(${src})`,
        }}
      />
    </div>
  )
}
