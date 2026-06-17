import { useEffect, useState } from 'react'
import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { getToken, subscribeToken, syncCookieFromStorage } from './lib/auth'
import { AppLayout } from './components/app-layout'
import { LoginPage } from './pages/login'
import { OverviewPage } from './pages/overview'
import { SourcesPage } from './pages/sources'
import { GroupsPage } from './pages/groups'
import { TokensPage } from './pages/tokens'
import { UsageStatsPage } from './pages/usage-stats'
import { UsageLogsPage } from './pages/usage-logs'
import { SystemLogsPage } from './pages/system-logs'
import { RuntimeConfigPage } from './pages/runtime-config'
import { DiagnosticsPage } from './pages/diagnostics'

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

export function App() {
  const token = useAuthState()

  return (
    <HashRouter>
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
    </HashRouter>
  )
}
