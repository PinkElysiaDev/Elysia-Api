import { useEffect, useState } from 'react'
import { Check, Copy, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { copyText } from '@/lib/clipboard'

export function MCPConfigCopyButton({
  name,
  baseUrl,
  disabledReason,
}: {
  name: string
  baseUrl: string
  disabledReason?: string
}) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const timer = window.setTimeout(() => setCopied(false), 1500)
    return () => window.clearTimeout(timer)
  }, [copied])

  async function handleCopy() {
    setBusy(true)
    setCopied(false)
    try {
      let endpoint: URL
      try {
        endpoint = new URL(`${baseUrl.replace(/\/+$/, '')}/mcp`)
        if (!['http:', 'https:'].includes(endpoint.protocol) || endpoint.search || endpoint.hash) {
          throw new Error('invalid MCP base URL')
        }
      } catch {
        throw new Error('请填写完整的 HTTP 或 HTTPS 对外基础地址，不包含查询参数或 # 片段')
      }
      // 列表中的 token 已脱敏；仅在复制时按需取回真实 Key，不渲染到页面。
      const { token } = await api.revealToken(name)
      if (!token) throw new Error('未能获取此 Key，请刷新后重试')
      await copyText(JSON.stringify({
        mcpServers: {
          elysia: {
            type: 'http',
            url: endpoint.toString(),
            headers: { Authorization: `Bearer ${token}` },
          },
        },
      }, null, 2))
      setCopied(true)
    } catch (err) {
      toast.error('复制失败', (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Button
      variant="ghost"
      size="sm"
      className="min-w-[132px] shrink-0"
      title={disabledReason || '复制 MCP 配置 JSON，包含此 Key'}
      aria-label={`复制 ${name} 的 MCP JSON`}
      disabled={busy || Boolean(disabledReason)}
      onClick={handleCopy}
    >
      {busy ? <Loader2 className="animate-spin" /> : copied ? <Check className="text-success" /> : <Copy />}
      {busy ? '正在复制…' : copied ? '已复制' : '复制 MCP JSON'}
    </Button>
  )
}
