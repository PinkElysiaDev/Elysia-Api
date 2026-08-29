import { Suspense, lazy, useEffect, useState } from 'react'
import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { getToken, subscribeToken, syncCookieFromStorage } from './lib/auth'
import { AppLayout } from './components/app-layout'

// 页面按路由拆包：recharts 等重组件只随用到它的页面下载，
// 登录页与首屏外壳保持轻量。
const LoginPage = lazy(() => import('./pages/login').then((m) => ({ default: m.LoginPage })))
const OverviewPage = lazy(() => import('./pages/overview').then((m) => ({ default: m.OverviewPage })))
const SourcesPage = lazy(() => import('./pages/sources').then((m) => ({ default: m.SourcesPage })))
const GroupsPage = lazy(() => import('./pages/groups').then((m) => ({ default: m.GroupsPage })))
const TokensPage = lazy(() => import('./pages/tokens').then((m) => ({ default: m.TokensPage })))
const UsageStatsPage = lazy(() => import('./pages/usage-stats').then((m) => ({ default: m.UsageStatsPage })))
const UsageLogsPage = lazy(() => import('./pages/usage-logs').then((m) => ({ default: m.UsageLogsPage })))
const SystemLogsPage = lazy(() => import('./pages/system-logs').then((m) => ({ default: m.SystemLogsPage })))
const RuntimeConfigPage = lazy(() => import('./pages/runtime-config').then((m) => ({ default: m.RuntimeConfigPage })))
const DiagnosticsPage = lazy(() => import('./pages/diagnostics').then((m) => ({ default: m.DiagnosticsPage })))

/** 登录前/外壳外加载页面的占位：全屏居中。 */
function BootFallback() {
  return (
    <div className="flex min-h-screen items-center justify-center" aria-busy="true">
      <span className="skeleton h-10 w-10 rounded-full" />
    </div>
  )
}

/** 订阅 token 变化，集中管理登录态。 */
function useAuthState() {
  const [token, setTokenState] = useState<string | null>(() => getToken())
  useEffect(() => {
    // 启动时按 localStorage 重建 cookie，确保 pprof 等浏览器导航请求能携带认证。
    syncCookieFromStorage()
    return subscribeToken(setTokenState)
  }, [])
  return token
}

/**
 * 登录后只预热高概率下一跳（总览之后常见的调用日志 / 模型源），避开图表
 * chunk。空闲时调度，省流或 2g 网络跳过。
 */
function usePreloadRoutes(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    const run = () => {
      if (cancelled) return
      const conn = (navigator as Navigator & { connection?: { saveData?: boolean; effectiveType?: string } }).connection
      if (conn?.saveData || conn?.effectiveType === 'slow-2g' || conn?.effectiveType === '2g') return
      void import('./pages/usage-logs')
      void import('./pages/sources')
    }
    const ric = window.requestIdleCallback
    if (typeof ric === 'function') {
      const id = ric(run, { timeout: 4000 })
      return () => {
        cancelled = true
        window.cancelIdleCallback(id)
      }
    }
    const id = window.setTimeout(run, 2000)
    return () => {
      cancelled = true
      window.clearTimeout(id)
    }
  }, [enabled])
}

export function App() {
  const token = useAuthState()
  usePreloadRoutes(!!token)

  return (
    <HashRouter>
      <Suspense fallback={<BootFallback />}>
        {token ? (
          <Routes>
            <Route element={<AppLayout />}>
              <Route path="/overview" element={<OverviewPage />} />
              <Route path="/sources" element={<SourcesPage />} />
              <Route path="/groups" element={<GroupsPage />} />
              <Route path="/tokens" element={<TokensPage />} />
              <Route path="/usage" element={<UsageStatsPage />} />
              <Route path="/usage-logs" element={<UsageLogsPage />} />
              <Route path="/logs" element={<SystemLogsPage />} />
              <Route path="/runtime" element={<RuntimeConfigPage />} />
              <Route path="/diagnostics" element={<DiagnosticsPage />} />
            </Route>
            <Route path="*" element={<Navigate to="/overview" replace />} />
          </Routes>
        ) : (
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="*" element={<Navigate to="/login" replace />} />
          </Routes>
        )}
      </Suspense>
    </HashRouter>
  )
}
