// Panel access token 的本地持久化 + 订阅。token 仅存于 localStorage，
// 所有 admin 请求附带 Authorization: Bearer <token>。401 时清除并回到登录页。

const STORAGE_KEY = 'elysia-webui.panel-token'

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
  } catch {
    /* ignore quota / privacy mode */
  }
  listeners.forEach((fn) => fn(token))
}

export function clearToken(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
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
