// Panel access token 的本地持久化 + 订阅。token 存于 localStorage 和 Cookie，
// 所有 admin 请求附带 Authorization: Bearer <token>。401 时清除并回到登录页。
// Cookie 用于让浏览器直接访问的链接（如 pprof）也能携带认证信息。

const STORAGE_KEY = 'elysia-webui.panel-token'
const COOKIE_NAME = 'panel_access_token'
const COOKIE_MAX_AGE = 30 * 24 * 60 * 60

type Listener = (token: string | null) => void

const listeners = new Set<Listener>()

/**
 * 把 token 写入 cookie。encodeURIComponent 保证 token 中的特殊字符
 * （如 + / = 空格）不会破坏 Cookie 头；后端读取时会用 url.QueryUnescape 还原。
 * SameSite=Lax：允许同站顶层 GET 导航（点击 pprof 链接、地址栏直开）携带 cookie，
 * 同时仍拦截跨站 POST，CSRF 防护不弱化。Strict 会拒绝部分同站新标签页导航，过严。
 */
function writeCookie(token: string | null): void {
  if (token) {
    document.cookie = `${COOKIE_NAME}=${encodeURIComponent(token)}; path=/; SameSite=Lax; max-age=${COOKIE_MAX_AGE}`
  } else {
    document.cookie = `${COOKIE_NAME}=; path=/; SameSite=Lax; max-age=0`
  }
}

export function getToken(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

export function setToken(token: string): void {
  try {
    localStorage.setItem(STORAGE_KEY, token)
    writeCookie(token)
  } catch {
    /* ignore quota / privacy mode */
  }
  listeners.forEach((fn) => fn(token))
}

export function clearToken(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
    writeCookie(null)
  } catch {
    /* ignore */
  }
  listeners.forEach((fn) => fn(null))
}

/**
 * 用 localStorage 中的 token 重建 cookie。仅在应用启动时调用一次。
 *
 * 必要性：cookie 只在显式登录（setToken）时写入。若用户在 cookie 认证特性上线前
 * 已登录、或 cookie 过期 / 被清理而 localStorage 仍在，则浏览器导航类请求
 * （pprof 等无 Authorization 头）会因拿不到 cookie 而被判 “token 未配置”。
 * 启动时同步一次可消除这类 “已登录但 cookie 缺失” 的状态。
 */
export function syncCookieFromStorage(): void {
  try {
    writeCookie(getToken())
  } catch {
    /* ignore */
  }
}

export function subscribeToken(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function isAuthenticated(): boolean {
  return !!getToken()
}
