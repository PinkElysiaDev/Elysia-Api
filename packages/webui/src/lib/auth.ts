// Panel access token 的本地持久化 + 订阅。token 存于 localStorage 和 Cookie，
// 所有 admin 请求附带 Authorization: Bearer <token>。401 时清除并回到登录页。
// Cookie 用于让浏览器直接访问的链接（如 pprof）也能携带认证信息。

const STORAGE_KEY = 'elysia-webui.panel-token'
const COOKIE_NAME = 'panel_access_token'

type Listener = (token: string | null) => void

const listeners = new Set<Listener>()

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
    // Set cookie for browser navigation (e.g., pprof links)
    // Use SameSite=Strict for CSRF protection, max age 30 days
    document.cookie = `${COOKIE_NAME}=${encodeURIComponent(token)}; path=/; SameSite=Strict; max-age=${30 * 24 * 60 * 60}`
  } catch {
    /* ignore quota / privacy mode */
  }
  listeners.forEach((fn) => fn(token))
}

export function clearToken(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
    // Clear cookie by setting max-age=0
    document.cookie = `${COOKIE_NAME}=; path=/; SameSite=Strict; max-age=0`
  } catch {
    /* ignore */
  }
  listeners.forEach((fn) => fn(null))
}

export function subscribeToken(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function isAuthenticated(): boolean {
  return !!getToken()
}
