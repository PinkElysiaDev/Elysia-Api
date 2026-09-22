// 按 key 独立拉取的模型勾选面板与权限徽标（多 key 源编辑用）。
import { useState } from 'react'
import { Check, Search } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { SourceAPIKey } from '@/lib/types'
import { cn } from '@/lib/utils'

// KeyPermissionBadge 显示 key 的模型权限状态：
// 已拉取 → 「已启用 x/y」；未做过按 key 拉取 → 「未拉取 · 不限制」。
export function KeyPermissionBadge({ apiKeyEntry }: { apiKeyEntry: SourceAPIKey }) {
  const fetched = apiKeyEntry.fetchedModels ?? []
  if (fetched.length === 0) {
    return <span className="rounded bg-muted px-1.5 py-0.5 text-2xs">未拉取 · 不限制</span>
  }
  const enabled = apiKeyEntry.allowedModels ?? fetched
  return (
    <span className="rounded bg-muted px-1.5 py-0.5 text-2xs">
      已启用 {enabled.length}/{fetched.length}
    </span>
  )
}

// KeyModelsPanel 是按 key 独立拉取的模型勾选面板：展示该 key 拉取到的模型
// （权限自动发现结果），勾选 = allowedModels；搜索 + 全选/反选。勾选变动即写入
// 显式 allowedModels（undefined → 显式列表），空数组 = 全部停用。
export function KeyModelsPanel({
  apiKeyEntry,
  onChange,
}: {
  apiKeyEntry: SourceAPIKey
  onChange: (allowed: string[]) => void
}) {
  const [search, setSearch] = useState('')
  const fetched = apiKeyEntry.fetchedModels ?? []
  const enabled = apiKeyEntry.allowedModels ?? fetched

  const keyword = search.trim().toLowerCase()
  const visible = keyword ? fetched.filter((id) => id.toLowerCase().includes(keyword)) : fetched

  function toggle(id: string) {
    onChange(enabled.includes(id) ? enabled.filter((x) => x !== id) : [...enabled, id])
  }

  return (
    <div className="space-y-2 border-t border-border/60 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-44">
          <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="h-7 pl-8 text-xs"
            placeholder="搜索模型…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={() => onChange(fetched.slice())}
        >
          全选
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={() => onChange(fetched.filter((id) => !enabled.includes(id)))}
        >
          反选
        </Button>
        <span className="ml-auto text-2xs text-muted-foreground">
          该 key 拉取到的模型即其分组权限；取消勾选后此 key 不再服务对应模型
        </span>
      </div>
      {visible.length === 0 ? (
        <p className="py-2 text-center text-xs text-muted-foreground">没有匹配的模型</p>
      ) : (
        <div className="grid gap-1 sm:grid-cols-2 lg:grid-cols-3">
          {visible.map((id) => {
            const checked = enabled.includes(id)
            return (
              <button
                key={id}
                type="button"
                onClick={() => toggle(id)}
                className={cn(
                  'flex items-center gap-2 rounded-md border px-2 py-1 text-left text-xs transition-colors',
                  checked ? 'border-primary/50 bg-primary/10 text-primary' : 'border-border/60 hover:bg-accent',
                )}
              >
                <span
                  className={cn(
                    'flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded border',
                    checked ? 'border-primary bg-primary text-primary-foreground' : 'border-border',
                  )}
                >
                  {checked && <Check className="h-2.5 w-2.5" />}
                </span>
                <span className="truncate font-mono">{id}</span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
