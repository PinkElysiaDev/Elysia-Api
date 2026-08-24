import { useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { Menu, X } from 'lucide-react'
import { Sidebar } from './sidebar'
import { cn } from '@/lib/utils'

/**
 * 外壳：≥761px 时 228px 侧栏常驻、无顶栏；
 * ≤760px 侧栏完全隐藏，左上角悬浮汉堡键以遮罩滑出
 * （点叉 / 点遮罩 / Esc / 跳转导航均收起）。
 */
export function AppLayout() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()

  useEffect(() => {
    setMobileOpen(false)
  }, [location.pathname])

  useEffect(() => {
    if (!mobileOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMobileOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [mobileOpen])

  return (
    <div className="grid min-h-screen grid-cols-[228px_minmax(0,1fr)] max-rail:grid-cols-1">
      <aside
        className={cn(
          'sticky top-0 h-screen w-[228px] border-r border-border',
          'max-rail:fixed max-rail:inset-y-0 max-rail:left-0 max-rail:z-[80] max-rail:h-full max-rail:bg-background max-rail:shadow-lg max-rail:transition-transform max-rail:duration-300',
          mobileOpen ? 'max-rail:translate-x-0' : 'max-rail:-translate-x-[103%]',
        )}
      >
        <button
          onClick={() => setMobileOpen(false)}
          aria-label="收起侧栏"
          className="absolute right-2.5 top-4 hidden h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-wash hover:text-rose max-rail:inline-flex"
        >
          <X className="h-4 w-4" />
        </button>
        <Sidebar />
      </aside>

      {/* ≤760px 悬浮汉堡键（桌面不显示，不与 Mac app 红绿灯区域冲突） */}
      <button
        onClick={() => setMobileOpen(true)}
        aria-label="打开导航菜单"
        className={cn(
          'fixed left-[14px] top-[14px] z-40 hidden h-9 w-9 items-center justify-center rounded-md border border-border bg-card/90 text-muted-foreground backdrop-blur-[10px] transition-colors hover:border-rose hover:text-rose max-rail:inline-flex',
          mobileOpen && 'max-rail:pointer-events-none max-rail:opacity-0',
        )}
      >
        <Menu className="h-[18px] w-[18px]" strokeWidth={1.8} />
      </button>

      {/* ≤760px 抽屉遮罩：其余内容压暗 + 模糊 */}
      <div
        className={cn(
          'fixed inset-0 z-[70] hidden bg-[var(--scrim)] backdrop-blur-sm transition-opacity duration-300 max-rail:block',
          mobileOpen ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
        onClick={() => setMobileOpen(false)}
      />

      <div className="flex min-w-0 flex-col">
        {/* 所有后台页共用主栏：吃满可用宽度，软顶 1600px，超出居中。 */}
        <main className="w-full flex-1 space-y-6 px-10 pb-[72px] pt-[30px] max-rail:px-[22px] max-rail:pt-16">
          <div key={location.pathname} className="relative mx-auto w-full max-w-[1600px] animate-rise">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
