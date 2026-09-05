import { NavLink } from 'react-router-dom'
import {
  Activity,
  Database,
  Layers,
  KeyRound,
  BarChart3,
  ScrollText,
  Terminal,
  Settings,
  Stethoscope,
  LogOut,
  type LucideIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { clearToken } from '@/lib/auth'
import { useConfirm } from './ui/confirm-dialog'
import { Button } from './ui/button'
import { ThemeToggle } from './theme-toggle'
import { BrandMark } from './brand-mark'

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  group: string
}

const NAV_ITEMS: NavItem[] = [
  { to: '/overview', label: '总览', icon: Activity, group: '监控' },
  { to: '/usage-logs', label: '调用日志', icon: ScrollText, group: '监控' },
  { to: '/sources', label: '模型源', icon: Database, group: '网关配置' },
  { to: '/groups', label: '模型组', icon: Layers, group: '网关配置' },
  { to: '/tokens', label: '访问令牌', icon: KeyRound, group: '网关配置' },
  { to: '/usage', label: 'Usage 统计', icon: BarChart3, group: '观测' },
  { to: '/logs', label: '系统日志', icon: Terminal, group: '系统' },
  { to: '/runtime', label: '运行配置', icon: Settings, group: '系统' },
  { to: '/diagnostics', label: '诊断', icon: Stethoscope, group: '系统' },
]

const GROUP_ORDER = ['监控', '网关配置', '观测', '系统']

export function Sidebar() {
  const { confirm, dialog } = useConfirm()
  const grouped = GROUP_ORDER.map((group) => ({
    group,
    items: NAV_ITEMS.filter((item) => item.group === group),
  }))

  // 登出需二次确认，防止误触直接清除本地令牌。
  async function handleLogout() {
    const ok = await confirm({
      title: '退出登录？',
      description: '将清除本地保存的 Panel Access Token，重新输入令牌后才能进入控制台。',
      confirmText: '退出',
    })
    if (ok) clearToken()
  }

  return (
    <div className="flex h-full flex-col gap-[22px] bg-rail-fade py-[22px] pb-[18px] text-sidebar-foreground max-rail:bg-background">
      <BrandMark className="px-[22px]" />

      <nav aria-label="主导航" className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-3 pb-2">
        {grouped.map(({ group, items }) => (
          <div key={group} className="flex flex-col gap-0.5">
            <span className="px-3 pb-1.5 pt-3.5 text-2xs uppercase tracking-[0.08em] text-muted-foreground">
              {group}
            </span>
            {items.map((item) => {
              const Icon = item.icon
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    cn(
                      'relative flex w-full items-center gap-[11px] rounded-md px-3 py-[9px] text-sm transition-colors duration-200',
                      isActive
                        ? 'bg-wash font-semibold text-rose'
                        : 'text-muted-foreground hover:bg-wash hover:text-foreground',
                    )
                  }
                >
                  {({ isActive }) => (
                    <>
                      {isActive && (
                        <span
                          aria-hidden
                          className="absolute -left-3 top-1/2 h-1.5 w-1.5 -translate-x-1/2 -translate-y-1/2 rotate-45 bg-brand-grad"
                        />
                      )}
                      <Icon className="h-[17px] w-[17px] shrink-0" strokeWidth={1.8} />
                      <span>{item.label}</span>
                    </>
                  )}
                </NavLink>
              )
            })}
          </div>
        ))}
      </nav>

      {/* 底部：主题切换 · 退出登录 */}
      <div className="mt-auto flex items-center justify-between gap-2 border-t border-border px-[22px] pt-3.5">
        <ThemeToggle />
        <Button
          variant="danger"
          size="iconSm"
          onClick={handleLogout}
          aria-label="退出登录"
          title="退出登录"
        >
          <LogOut className="h-3.5 w-3.5" />
        </Button>
      </div>
      {dialog}
    </div>
  )
}
