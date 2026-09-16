import { Suspense, useEffect, useRef, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Outlet, useLocation } from 'react-router-dom'
import { Menu, X } from 'lucide-react'
import { createPortal } from 'react-dom'
import { Sidebar } from './sidebar'
import { useUsageLive } from '@/lib/hooks'
import { ARRIVED_FROM_LOGIN_KEY, readArrivedFromLogin } from '@/lib/auth'
import { ROLE_ANCHOR_CLASS, roleMaskStyle } from '@/lib/role-presentation'
import { cn } from '@/lib/utils'
import { Z_INDEX } from '@/lib/z-index'

/**
 * 交接残影：登录页的线稿以 0.45 浓度原地交接的瞬间，控制台外壳正从透明
 * 渐显，内部的水印会被带着一起「消失再出现」。这枚与控制台水印同位置、
 * 同浓度的残影悬浮在最上层顶住这段渐显期（1s），再自行淡出卸载。
 */
function ArrivalEcho() {
  const [gone, setGone] = useState(false)
  if (gone) return null
  return createPortal(
    <div
      aria-hidden
      className={cn('arrival-echo', Z_INDEX.arrivalEcho, ROLE_ANCHOR_CLASS)}
      style={roleMaskStyle()}
      onAnimationEnd={() => setGone(true)}
    />,
    document.body,
  )
}

/** 桌面常驻侧栏；移动端复用 Radix 的焦点管理、滚动锁定与关闭后焦点恢复。 */
export function AppLayout() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()
  const mainRef = useRef<HTMLElement>(null)
  useUsageLive()

  // 仅登录到达时外壳渐显（app-fade）；刷新与普通路由跳转保持无动画。
  // 初始化器读取标记，移除由 ElysiaStage 的 effect 负责。
  const [arriving] = useState(readArrivedFromLogin)

  useEffect(() => {
    setMobileOpen(false)
    // 'auto' 而非 'instant'：Safari < 15.4 的 ScrollBehavior 枚举里没有后者，会抛 TypeError。
    window.scrollTo({ top: 0, behavior: 'auto' })
  }, [location.pathname])

  // arrival 标记的删除通常由 overview 内的 ElysiaStage 读后即删完成；若 overview
  // 懒加载 chunk 完成前用户已导航离开，标记会滞留 sessionStorage，导致同会话稍后
  // 首次进入 overview 意外重播入场动画。离开 overview 时在此兜底删除。
  useEffect(() => {
    if (location.pathname !== '/overview') {
      try {
        sessionStorage.removeItem(ARRIVED_FROM_LOGIN_KEY)
      } catch {
        /* ignore */
      }
    }
  }, [location.pathname])

  useEffect(() => {
    const media = window.matchMedia('(min-width: 761px)')
    const closeOnDesktop = () => {
      if (media.matches) setMobileOpen(false)
    }
    media.addEventListener('change', closeOnDesktop)
    return () => media.removeEventListener('change', closeOnDesktop)
  }, [])

  return (
    <div className={cn(arriving && 'app-fade', 'grid min-h-dvh grid-cols-[228px_minmax(0,1fr)] max-rail:grid-cols-1')}>
      {arriving && <ArrivalEcho />}
      <a
        href="#main-content"
        onClick={(event) => {
          // HashRouter 使用 URL hash 路由，跳转正文只移动焦点，不改写路由。
          event.preventDefault()
          mainRef.current?.focus()
        }}
        className={cn("sr-only rounded-md bg-primary px-4 py-3 text-primary-foreground focus:not-sr-only focus:fixed focus:left-4 focus:top-4", Z_INDEX.skipLink)}
      >
        跳转到主要内容
      </a>

      <aside className="sticky top-0 h-dvh w-[228px] border-r border-border max-rail:hidden">
        <Sidebar />
      </aside>

      <Dialog.Root open={mobileOpen} onOpenChange={setMobileOpen}>
        <Dialog.Trigger asChild>
          <button
            type="button"
            aria-label="打开导航菜单"
            className="fixed left-[max(14px,env(safe-area-inset-left))] top-[max(10px,env(safe-area-inset-top))] z-40 hidden h-11 w-11 items-center justify-center rounded-lg border border-border bg-card/95 text-muted-foreground shadow-soft backdrop-blur-[10px] transition-colors hover:border-rose hover:text-rose max-rail:inline-flex"
          >
            <Menu className="h-5 w-5" strokeWidth={1.8} />
          </button>
        </Dialog.Trigger>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-40 bg-[var(--scrim)] backdrop-blur-sm data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=open]:fade-in-0 data-[state=closed]:fade-out-0" />
          <Dialog.Content
            aria-describedby={undefined}
            className="fixed inset-y-0 left-0 z-50 h-dvh w-[min(288px,calc(100vw-3rem))] border-r border-border bg-background shadow-lg outline-none data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=open]:slide-in-from-left data-[state=closed]:slide-out-to-left duration-200"
          >
            <Dialog.Title className="sr-only">导航菜单</Dialog.Title>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="收起侧栏"
                className="absolute right-1 top-2 z-10 inline-flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-wash hover:text-rose"
              >
                <X className="h-5 w-5" />
              </button>
            </Dialog.Close>
            <Sidebar onNavigate={() => setMobileOpen(false)} />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>

      <main
        id="main-content"
        ref={mainRef}
        tabIndex={-1}
        className="min-w-0 w-full space-y-6 px-6 pb-[max(72px,env(safe-area-inset-bottom))] pt-[30px] outline-none max-rail:px-4 max-rail:pt-[72px]"
      >
        <div key={location.pathname} className="page-enter relative w-full">
          <Suspense
            fallback={
              <div role="status" aria-label="正在加载页面" className="space-y-6" aria-busy="true">
                <div className="skeleton h-9 w-40 rounded-md" />
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                  {[0, 1, 2].map((key) => <div key={key} className="skeleton h-28 rounded-xl" />)}
                </div>
                <div className="skeleton h-64 rounded-xl" />
                <span className="sr-only">正在加载页面…</span>
              </div>
            }
          >
            <Outlet />
          </Suspense>
        </div>
      </main>
    </div>
  )
}
