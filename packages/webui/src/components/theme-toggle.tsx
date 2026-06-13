import { Moon, Sun } from 'lucide-react'
import { Button } from './ui/button'
import { useTheme } from '@/lib/theme'

export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme()
  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={toggleTheme}
      aria-label={theme === 'dark' ? '切换到日间主题' : '切换到夜间主题'}
      title={theme === 'dark' ? '日间主题' : '夜间主题'}
    >
      {theme === 'dark' ? <Sun className="h-[18px] w-[18px]" /> : <Moon className="h-[18px] w-[18px]" />}
    </Button>
  )
}
