import { useEffect, useRef, useState } from 'react'
import { Moon, Sun } from 'lucide-react'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

/** 主题切换胶囊（侧栏底部 / 登录页右上角复用）：图标 morph + 一次品牌色涟漪。 */
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
        'theme-toggle relative inline-flex h-[34px] items-center gap-2 overflow-hidden rounded-md border border-input bg-card/80 px-3 text-xs text-muted-foreground',
        // hover 语言与 Button default 同族：玫红洗色，而非独立的灰阶提亮
        'transition-colors duration-300 hover:border-rose hover:bg-wash hover:text-rose',
        switching && 'is-switching',
      )}
    >
      <span className="theme-icon" aria-hidden>
        <Sun className="icon-sun" strokeWidth={1.8} />
        <Moon className="icon-moon" strokeWidth={1.8} />
      </span>
      <span>{dark ? '深色模式' : '浅色模式'}</span>
    </button>
  )
}
