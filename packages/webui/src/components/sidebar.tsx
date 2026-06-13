import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  Database,
  Layers,
  KeyRound,
  BarChart3,
  ScrollText,
  Terminal,
  Settings,
  Activity,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { Logo } from './logo'

export interface NavItem {
  to: string
  label: string
  icon: React.ComponentType<{ className?: string }>
  group: string
}

export const NAV_ITEMS: NavItem[] = [
  { to: '/overview', label: '概览', icon: LayoutDashboard, group: '总览' },
  { to: '/sources', label: '模型源', icon: Database, group: '模型配置' },
  { to: '/groups', label: '模型组', icon: Layers, group: '模型配置' },
  { to: '/tokens', label: 'API Keys', icon: KeyRound, group: '访问控制' },
  { to: '/usage', label: 'Usage 统计', icon: BarChart3, group: '观测' },
  { to: '/usage-logs', label: 'Usage 日志', icon: ScrollText, group: '观测' },
  { to: '/logs', label: '系统日志', icon: Terminal, group: '观测' },
  { to: '/runtime', label: '运行配置', icon: Settings, group: '系统' },
  { to: '/diagnostics', label: '诊断', icon: Activity, group: '系统' },
]

const GROUP_ORDER = ['总览', '模型配置', '访问控制', '观测', '系统']

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const grouped = GROUP_ORDER.map((group) => ({
    group,
    items: NAV_ITEMS.filter((item) => item.group === group),
  }))

  return (
    <div className="flex h-full flex-col gap-6 bg-sidebar text-sidebar-foreground">
      <div className="px-5 pt-6">
        <Logo />
      </div>
      <nav className="flex-1 space-y-5 overflow-y-auto px-3 pb-6">
        {grouped.map(({ group, items }) => (
          <div key={group} className="space-y-1">
            <p className="px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70">
              {group}
            </p>
            {items.map((item) => {
              const Icon = item.icon
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={onNavigate}
                  className={({ isActive }) =>
                    cn(
                      'group relative flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-all duration-200',
                      isActive
                        ? 'bg-primary/12 text-primary'
                        : 'text-muted-foreground hover:bg-accent hover:text-foreground',
                    )
                  }
                >
                  {({ isActive }) => (
                    <>
                      <span
                        className={cn(
                          'absolute left-0 top-1/2 h-5 w-1 -translate-y-1/2 rounded-r-full bg-primary transition-all duration-200',
                          isActive ? 'opacity-100' : 'opacity-0',
                        )}
                      />
                      <Icon className="h-[18px] w-[18px] shrink-0" />
                      <span>{item.label}</span>
                    </>
                  )}
                </NavLink>
              )
            })}
          </div>
        ))}
      </nav>
    </div>
  )
}
