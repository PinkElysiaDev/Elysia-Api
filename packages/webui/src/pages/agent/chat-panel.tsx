import { AlertTriangle, ArrowUp, FileText, Plus, Square, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/input'
import { useToast } from '@/components/ui/use-toast'
import type { Model, ModelSource } from '@/lib/types'
import type {
  AgentContextTab,
  AgentDocument,
  AgentMessage,
  AgentSession,
  AgentSettings,
} from '@/lib/agent/types'
import type { AgentLiveState } from '@/lib/agent/use-agent-stream'
import { cn, compactNumber, formatHitRate } from '@/lib/utils'
import { PermissionMenu, ThinkingMenu } from './composer-menu'
import { ModelPicker } from './model-picker'
import { ApprovalCard, LiveAssistantView, MessageCard } from './message-card'

/** 单文件 ≤8MiB、最多 20 个（与后端限制对齐）。 */
const MAX_FILE_BYTES = 8 << 20
const MAX_FILES = 20

async function fileToDocument(file: File): Promise<AgentDocument> {
  const isTextLike = file.type.startsWith('text/') || /\.(txt|md|json|yaml|yml|xml|csv|html?)$/i.test(file.name)
  if (isTextLike) {
    return { name: file.name, mime: file.type || 'text/plain', text: await file.text() }
  }
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
  return { name: file.name, mime: file.type || 'application/octet-stream', dataUrl }
}

export interface ChatPanelProps {
  session: AgentSession
  messages: AgentMessage[]
  live: AgentLiveState
  onSend: (input: { content?: string; documents?: AgentDocument[]; afterSeq?: number }) => void
  onApprove: (decision: { approved: boolean }) => void
  onStop: () => void
  onOpenContextTab: (tab: AgentContextTab) => void
  onSettingsChange: (patch: { settings?: Partial<AgentSettings> }) => Promise<void> | void
}

/**
 * 聊天面板：扁平消息流 + ComposerDock（输入与全部会话设置一体）。
 * 输入容器是页面唯一刻意抬升的元素；消息直接浮在背景上。
 * 底部控制条从左到右：附加 / 权限控制 / 会话统计 / 模型 / 思考强度 / 发送。
 */
export function ChatPanel({
  session,
  messages,
  live,
  onSend,
  onApprove,
  onStop,
  onOpenContextTab,
  onSettingsChange,
}: ChatPanelProps) {
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [documents, setDocuments] = useState<AgentDocument[]>([])
  const [dragOver, setDragOver] = useState(false)
  const [editing, setEditing] = useState<{ seq: number; text: string } | null>(null)
  const [saving, setSaving] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const busy = live.running
  const approval = live.approvalPending
  const settings = session.settings
  const needsModel = !settings.modelSourceId || !settings.modelName

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [messages.length, live.text, live.toolCards.length, approval])

  /** 会话累计用量（含缓存命中），随助手消息持久化逐步累加。 */
  const usageStat = useMemo(() => {
    let input = 0
    let output = 0
    let total = 0
    let cached = 0
    for (const message of messages) {
      if (message.role !== 'assistant' || !message.usage) continue
      input += Number(message.usage.input_tokens ?? 0)
      output += Number(message.usage.output_tokens ?? 0)
      total += Number(message.usage.total_tokens ?? 0)
      cached += Number(message.usage.cached_input_tokens ?? 0)
    }
    if (total === 0 && input === 0 && output === 0) return null
    return {
      input,
      output,
      cached,
      total: total || input + output,
      hitRate: input > 0 ? cached / input : null,
    }
  }, [messages])

  const save = async (patch: { settings?: Partial<AgentSettings> }) => {
    setSaving(true)
    try {
      await onSettingsChange(patch)
    } finally {
      setSaving(false)
    }
  }

  const handleModelSelect = (source: ModelSource, model: Model) => {
    void save({ settings: { modelSourceId: source.id, modelName: model.name } })
  }

  const addFiles = async (files: FileList | File[]) => {
    const next: AgentDocument[] = []
    for (const file of Array.from(files)) {
      if (file.size > MAX_FILE_BYTES) {
        toast({ description: `${file.name} 超过 8MiB 上限，已跳过` })
        continue
      }
      next.push(await fileToDocument(file))
    }
    setDocuments((current) => {
      const merged = [...current, ...next]
      if (merged.length > MAX_FILES) {
        toast({ description: `附件最多 ${MAX_FILES} 个` })
        return merged.slice(0, MAX_FILES)
      }
      return merged
    })
  }

  const submit = () => {
    const content = text.trim()
    if ((!content && documents.length === 0) || busy) return
    onSend({ content, documents })
    setText('')
    setDocuments([])
  }

  const messageActions = useMemo(
    () => ({
      onRetry: (message: AgentMessage) => {
        if (busy) return
        const content = message.content as { text?: string; documents?: AgentDocument[] }
        onSend({ content: content.text ?? '', documents: content.documents ?? [], afterSeq: message.seq - 1 })
      },
      onEditResend: (message: AgentMessage) => {
        if (busy) return
        const content = message.content as { text?: string }
        setEditing({ seq: message.seq, text: content.text ?? '' })
      },
      onRegenerate: (message: AgentMessage) => {
        if (busy) return
        // 截断到该助手消息之前，从上一条用户消息重新生成。
        onSend({ afterSeq: message.seq - 1 })
      },
    }),
    [busy, onSend],
  )

  return (
    <div
      className="flex min-w-0 flex-1 flex-col"
      onDragOver={(event) => {
        event.preventDefault()
        setDragOver(true)
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(event) => {
        event.preventDefault()
        setDragOver(false)
        if (event.dataTransfer.files.length > 0) void addFiles(event.dataTransfer.files)
      }}
    >
      {/* 扁平消息流：直接浮在页面背景上。 */}
      <div className={cn('relative min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-5', dragOver && 'bg-wash/40')}>
        {dragOver ? (
          <div className="pointer-events-none absolute inset-3 z-10 flex items-center justify-center rounded-xl border-2 border-dashed border-rose/40 text-sm text-muted-foreground">
            松开以添加附件（文档 / 图片 / PDF）
          </div>
        ) : null}
        {messages.map((message) => (
          <MessageCard key={message.seq} message={message} actions={messageActions} onOpenActivity={() => onOpenContextTab('activity')} />
        ))}
        {live.running && live.statusText ? (
          <div className="flex items-center gap-2 pl-1 text-2xs text-muted-foreground">
            <span className="dot dot-ok" />
            {live.statusText}
          </div>
        ) : null}
        {live.running || live.text || live.reasoning || live.toolCards.length > 0 ? (
          <LiveAssistantView live={live} onOpenActivity={() => onOpenContextTab('activity')} />
        ) : null}
        {approval ? (
          <ApprovalCard
            approval={approval}
            busy={busy}
            onApprove={() => onApprove({ approved: true })}
            onDeny={() => onApprove({ approved: false })}
          />
        ) : null}
        {live.error ? (
          <div className="flex items-center gap-2 rounded-lg border-[color-mix(in_srgb,var(--ember)_35%,transparent)] bg-[color-mix(in_srgb,var(--ember)_7%,transparent)] px-3 py-2 text-xs text-ember">
            <AlertTriangle className="h-4 w-4 shrink-0" />
            <span className="min-w-0 flex-1">{live.error.text}</span>
            {live.error.retryable && !busy ? (
              <Button
                size="sm"
                variant="ghost"
                className="h-6 gap-1 px-2 text-2xs"
                onClick={() => {
                  const lastUser = [...messages].reverse().find((message) => message.role === 'user')
                  if (!lastUser) return
                  messageActions.onRetry(lastUser)
                }}
              >
                重试
              </Button>
            ) : null}
          </div>
        ) : null}
        <div ref={bottomRef} />
      </div>

      {/* ComposerDock：页面唯一抬升的容器——输入 + 全部会话设置。 */}
      <div className="px-4 pb-4">
        <div className="rounded-xl border border-border bg-card transition-shadow focus-within:border-rose focus-within:ring-[3px] focus-within:ring-wash">
          {editing ? (
            <div className="border-b border-border/50 px-3 pb-2 pt-2.5">
              <div className="mb-1.5 flex items-center gap-2 text-2xs text-muted-foreground">
                编辑历史消息并在这里重发（之后的消息将被替换）
                <button type="button" className="ml-auto rounded p-0.5 hover:text-foreground" onClick={() => setEditing(null)}>
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
              <Textarea
                className="min-h-[60px] border-0 bg-transparent px-0 text-sm focus-visible:border-0 focus-visible:ring-0"
                value={editing.text}
                onChange={(event) => setEditing({ ...editing, text: event.target.value })}
              />
              <div className="flex justify-end">
                <Button
                  size="sm"
                  className="h-7"
                  disabled={busy || !editing.text.trim()}
                  onClick={() => {
                    onSend({ content: editing.text, afterSeq: editing.seq - 1 })
                    setEditing(null)
                  }}
                >
                  从这里重发
                </Button>
              </div>
            </div>
          ) : null}

          {documents.length > 0 ? (
            <div className="flex flex-wrap gap-1.5 border-b border-border/50 px-3 py-2">
              {documents.map((doc, index) => (
                <span key={index} className="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-2xs">
                  <FileText className="h-3 w-3 text-muted-foreground" />
                  {doc.name ?? `材料 ${index + 1}`}
                  <button type="button" className="rounded p-0.5 hover:text-ember" onClick={() => setDocuments((current) => current.filter((_, i) => i !== index))}>
                    <X className="h-3 w-3" />
                  </button>
                </span>
              ))}
            </div>
          ) : null}

          <input
            ref={fileInputRef}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              if (event.target.files?.length) void addFiles(event.target.files)
              event.target.value = ''
            }}
          />
          <Textarea
            className="max-h-56 min-h-[44px] w-full resize-none border-0 bg-transparent px-3.5 py-2.5 text-sm focus-visible:border-0 focus-visible:ring-0"
            placeholder={needsModel ? '先在下方选择模型…' : '描述任务，Ctrl+Enter 发送；可直接粘贴或拖入附件'}
            value={text}
            disabled={busy}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
                event.preventDefault()
                submit()
              }
            }}
            onPaste={(event) => {
              const files = Array.from(event.clipboardData.files ?? [])
              if (files.length > 0) {
                event.preventDefault()
                void addFiles(files)
              }
            }}
          />

          {/* 底部控制条：附加 / 权限 / 统计 / 模型 / 思考 / 发送。 */}
          <div className="flex flex-wrap items-center gap-2 border-t border-border/50 px-2.5 py-2">
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7 shrink-0 rounded-full border border-input"
              title="添加附件（文档 / 图片 / PDF）"
              disabled={busy}
              onClick={() => fileInputRef.current?.click()}
            >
              <Plus className="h-4 w-4" />
            </Button>
            <PermissionMenu settings={settings} disabled={busy} onChange={(patch) => void save(patch)} />
            {usageStat ? (
              <span
                className="tnum whitespace-nowrap text-2xs text-muted-foreground"
                title={`本会话累计：↑${usageStat.input} ↓${usageStat.output}${usageStat.cached > 0 ? ` · 缓存命中 ${usageStat.cached}` : ''} tokens`}
              >
                {compactNumber(usageStat.total)} tokens
                {usageStat.hitRate != null ? ` · 命中 ${formatHitRate(usageStat.hitRate)}` : ''}
              </span>
            ) : null}
            <ModelPicker
              sourceId={settings.modelSourceId}
              modelName={settings.modelName}
              disabled={busy}
              onSelect={handleModelSelect}
            />
            <ThinkingMenu settings={settings} disabled={busy} onChange={(patch) => void save(patch)} />
            <span
              className={cn(
                'ml-auto text-2xs text-muted-foreground transition-opacity',
                saving ? 'opacity-100' : 'opacity-0',
              )}
            >
              保存中…
            </span>
            {busy ? (
              <Button
                variant="destructive"
                size="icon"
                className="h-8 w-8 shrink-0 rounded-full"
                title="停止本轮"
                onClick={onStop}
              >
                <Square className="h-3.5 w-3.5" />
              </Button>
            ) : (
              <Button
                size="icon"
                className="h-8 w-8 shrink-0 rounded-full"
                title={needsModel ? '请先选择模型' : '发送（Ctrl+Enter）'}
                disabled={needsModel || (!text.trim() && documents.length === 0)}
                onClick={submit}
              >
                <ArrowUp className="h-4 w-4" />
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
