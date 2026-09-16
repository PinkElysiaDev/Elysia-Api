import { useEffect, useState } from 'react'
import { Sparkles } from 'lucide-react'
import { cn } from '@/lib/utils'
import { ARRIVED_FROM_LOGIN_KEY } from '@/lib/auth'
import { ROLE_ANCHOR_CLASS, roleMaskStyle } from '@/lib/role-presentation'

interface ElysiaStageProps {
  className?: string
  statusState?: 'ok' | 'err' | 'off' | 'active'
  showAura?: boolean
}

/**
 * 爱莉希雅视觉中枢舞台 (Elysia Visual Stage)
 * 将高精度立绘作为界面的核心视觉主角与氛围发生源，配合灵动微光、状态光环与水晶粒子。
 *
 * 登录到达时收尾：登录页以完整浓度预印的线稿原地交接，此处
 * 从 0.85 浓度缓缓淡入水印常驻浓度（watermark-settle，见 index.css）。
 * 标记由登录页写入 sessionStorage，此处读后即删——刷新与普通路由跳转不重播。
 */
export function ElysiaStage({ className, statusState = 'ok', showAura = true }: ElysiaStageProps) {
  // 初始化器先于 effect 消费标记读值，StrictMode 双挂载也只播一次。
  const [arriving, setArriving] = useState(() => {
    try {
      return sessionStorage.getItem(ARRIVED_FROM_LOGIN_KEY) === '1'
    } catch {
      return false
    }
  })

  useEffect(() => {
    try {
      sessionStorage.removeItem(ARRIVED_FROM_LOGIN_KEY)
    } catch {
      /* ignore */
    }
  }, [])

  const maskStyle = roleMaskStyle()

  return (
    <div
      aria-hidden
      className={cn(
        ROLE_ANCHOR_CLASS,
        'z-0 select-none overflow-visible',
        'transition-all duration-700 ease-out',
        arriving && 'elysia-arrive',
        className,
      )}
    >
      {/* 氛围呼吸光晕 */}
      {showAura && (
        <div className="elysia-arrive-aura absolute -top-12 right-12 h-72 w-72 rounded-full bg-gradient-to-br from-primary/15 to-transparent blur-3xl sm:h-96 sm:w-96" />
      )}

      {/* 晶化粒子微光装饰 */}
      <div className="absolute right-1/4 top-16 hidden animate-pulse opacity-40 duration-1000 md:block">
        <Sparkles className="h-4 w-4 text-primary" />
      </div>
      <div className="absolute right-1/3 top-48 hidden animate-pulse opacity-30 delay-300 md:block">
        <Sparkles className="h-3 w-3 text-orchid" />
      </div>

      {/* 立绘主体容器：高清晰度硅光色粉渐变 + 柔化羽化 Mask */}
      <div className="relative aspect-[760/808] w-full">
        <div
          className={cn(
            'role-fig absolute inset-0',
            'opacity-[0.09] transition-opacity duration-500 dark:opacity-[0.14]',
            statusState === 'active' && 'opacity-[0.13] dark:opacity-[0.18]',
          )}
          style={maskStyle}
          onAnimationEnd={(event) => {
            // watermark-settle 走完（forwards 填充值与类定义一致）后摘除动画，
            // 让 statusState 等后续透明度变化恢复生效。
            if (event.animationName === 'watermark-settle') setArriving(false)
          }}
        />
      </div>
    </div>
  )
}

/** 页面共用的立绘水印：作为低饱和度品牌暗纹，不抢夺前景信息。 */
export function RoleWatermark({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      className={cn(
        'pointer-events-none absolute z-0 w-[240px] select-none opacity-[0.06] transition-opacity dark:opacity-[0.09] rail:w-[480px]',
        className,
      )}
    >
      <div className="role-fig aspect-[760/808] w-full" style={roleMaskStyle()} />
    </div>
  )
}
