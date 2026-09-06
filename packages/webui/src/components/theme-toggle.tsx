import { useEffect, useRef, useState } from 'react'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

/**
 * 主题切换钮（侧栏底部 / 登录页右上角复用）。
 * 日/月 morph 移植自 toggles.dev「Classic」；原实现依赖 Tailwind v4 工具类，
 * 项目停在 v3，形变逻辑因此落在 index.css 的 .theme-sun 下。
 */
const SUN_RAYS = [
  'M12 1.4 12 3.8',
  'M20.3 3.7 17.8 6.2',
  'M22.6 12 20.2 12',
  'M12 22.6 12 20.2',
  'M1.4 12 3.8 12',
  'M20.3 20.3 17.8 17.8',
  'M3.7 20.3 6.2 17.8',
  'M3.7 3.7 6.2 6.2',
] as const

export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme()
  const dark = theme === 'dark'
  const [switching, setSwitching] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => () => window.clearTimeout(timer.current), [])

  const handleClick = () => {
    toggleTheme()
    setSwitching(true)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setSwitching(false), 560)
  }

  return (
    <button
      type="button"
      onClick={handleClick}
      aria-label={dark ? '切换到浅色模式' : '切换到深色模式'}
      aria-pressed={dark}
      title={dark ? '浅色模式' : '深色模式'}
      className={cn(
        'theme-toggle relative inline-flex h-8 w-8 items-center justify-center overflow-hidden rounded-full text-muted-foreground',
        'transition-colors duration-300 hover:bg-wash hover:text-rose',
        switching && 'is-switching',
      )}
    >
      <svg viewBox="0 0 24 24" className="theme-sun" aria-hidden="true">
        <defs>
          <clipPath id="theme-sun-clip">
            <path className="sun-clip-path" d="M0 0h25a1 1 0 0010 10v14H0Z" />
          </clipPath>
        </defs>
        <g stroke="currentColor" strokeLinecap="round">
          <circle
            className="sun-disc"
            cx="12"
            cy="12"
            r="5"
            fill="currentColor"
            clipPath="url(#theme-sun-clip)"
          />
          <path className="sun-ray" d={SUN_RAYS.join(' ')} fill="none" strokeWidth={2} />
        </g>
      </svg>
    </button>
  )
}
