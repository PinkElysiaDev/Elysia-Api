import { useState, type FormEvent } from 'react'
import { Loader2, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { SecretInput } from '@/components/secret-input'
import { ThemeToggle } from '@/components/theme-toggle'
import { Logo } from '@/components/logo'
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
    <div className="relative min-h-screen">
      <div className="app-aurora" />
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>
      <div className="grid min-h-screen place-items-center px-4">
        <Card className="w-full max-w-md p-8">
          <div className="mb-6 flex flex-col items-center gap-4 text-center">
            <Logo className="scale-110" />
            <div className="space-y-1">
              <h1 className="text-xl font-semibold tracking-tight">登录控制台</h1>
              <p className="text-sm text-muted-foreground">
                使用后端 config.json 配置的 Panel Access Token 登录
              </p>
            </div>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
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
              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>
            <Button type="submit" className="w-full" size="lg" disabled={loading}>
              {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <ShieldCheck className="h-4 w-4" />}
              {loading ? '验证中…' : '登录'}
            </Button>
          </form>

          <p className="mt-6 text-center text-xs text-muted-foreground">
            Token 仅保存在本地浏览器，所有请求通过 Bearer 认证。
          </p>
        </Card>
      </div>
    </div>
  )
}
