import { useState, type FormEvent } from 'react'
import { Loader2, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { SecretInput } from '@/components/secret-input'
import { ThemeToggle } from '@/components/theme-toggle'
import { BrandMark } from '@/components/brand-mark'
import { RoleWatermark } from '@/components/role-watermark'
import { setToken } from '@/lib/auth'
import { verifyToken } from '@/lib/api'

export function LoginPage() {
  const [value, setValue] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const token = value.trim()
    if (!token) {
      setError('请输入 Panel Access Token')
      return
    }
    setLoading(true)
    setError(null)
    try {
      const valid = await verifyToken(token)
      if (!valid) {
        setError('Token 无效，请确认与后端 config.json 中的 panelAccessToken 一致')
        return
      }
      setToken(token)
    } catch (err) {
      setError((err as Error).message || '无法连接到后端')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="relative mx-auto grid min-h-screen w-full max-w-[1600px] place-items-center px-4">
      <RoleWatermark className="-right-4 top-1/2 -translate-y-1/2 rail:-right-8" />

      <div className="absolute right-[22px] top-[22px] max-rail:right-[14px] max-rail:top-[14px]">
        <ThemeToggle />
      </div>

      <div className="relative z-[1] w-full max-w-[400px]">
        <BrandMark size="login" className="mb-8 justify-center" />

        <div className="rounded-lg border border-border bg-card p-7 shadow-soft">
          <h1 className="font-display text-xl font-semibold tracking-[0.01em]">登录控制台</h1>
          <p className="mt-1.5 text-sm text-muted-foreground">使用后端 config.json 配置的 Panel Access Token 登录</p>

          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="token" required>
                Panel Access Token
              </Label>
              <SecretInput
                id="token"
                autoFocus
                value={value}
                placeholder="输入访问令牌"
                onChange={(e) => {
                  setValue(e.target.value)
                  setError(null)
                }}
              />
              {error && <p className="text-xs text-ember">{error}</p>}
            </div>
            <Button type="submit" variant="primary" className="w-full" size="lg" disabled={loading}>
              {loading ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
              {loading ? '验证中…' : '登录'}
            </Button>
          </form>
        </div>

        <p className="mt-6 text-center text-2xs text-muted-foreground">
          Token 仅保存在本地浏览器，所有请求通过 Bearer 认证。
        </p>
      </div>
    </div>
  )
}
