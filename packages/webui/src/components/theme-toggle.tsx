import { Moon, Sun } from 'lucide-react'
import { useTheme } from '@/lib/theme'

/** 侧栏底部的主题切换按钮（login 页右上角也复用）。 */
export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme()
  return (
    <button
      onClick={toggleTheme}
      aria-label={theme === 'dark' ? '切换到日间主题' : '切换到夜间主题'}
      title={theme === 'dark' ? '日间主题' : '夜间主题'}
      className="inline-flex items-center gap-[7px] rounded-md border border-border px-2.5 py-1.5 text-xs text-muted-foreground transition-colors hover:border-rose hover:bg-wash hover:text-rose"
    >
      {theme === 'dark' ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
      <span>{theme === 'dark' ? '日间' : '夜间'}</span>
    </button>
  )
}
