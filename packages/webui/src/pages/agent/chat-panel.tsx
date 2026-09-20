import { AlertTriangle, Eraser, FileText, Paperclip, Send, Square, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/input'
import { useToast } from '@/components/ui/use-toast'
import type { AgentDocument, AgentMessage, AgentSession } from '@/lib/agent/types'
import type { AgentLiveState } from '@/lib/agent/use-agent-stream'
import { cn } from '@/lib/utils'
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
  onClearHistory: () => void
}

export function ChatPanel({
  session,
  messages,
  live,
  onSend,
  onApprove,
  onStop,
  onClearHistory,
}: ChatPanelProps) {
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [documents, setDocuments] = useState<AgentDocument[]>([])
  const [dragOver, setDragOver] = useState(false)
  const [editing, setEditing] = useState<{ seq: number; text: string } | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const busy = live.running
  const approval = live.approvalPending
  const needsModel = !session.settings.modelSourceId || !session.settings.modelName

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [messages.length, live.text, live.toolCards.length, approval])

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
      <div className={cn('relative flex-1 space-y-3 overflow-y-auto px-4 py-4', dragOver && 'bg-wash/40')}>
        {dragOver ? (
          <div className="pointer-events-none absolute inset-3 z-10 flex items-center justify-center rounded-xl border-2 border-dashed border-rose/40 text-sm text-muted-foreground">
            松开以添加附件（文档 / 图片 / PDF）
          </div>
        ) : null}
        {messages.length === 0 && !live.running ? (
          <div className="mx-auto max-w-md space-y-2 px-4 py-10 text-center text-sm text-muted-foreground">
            <p className="text-base font-medium text-foreground">Elysia-API 智能体，网关事务一句话</p>
            <p>
              拖入 API 文档让它接入新协议（草稿 → 离线自检 → 经批准真实测试 → 保存）；让它新增模型源 / 模型组、
              查询用量并生成图表、分析失败请求。写操作与出站请求都会先经你批准。
            </p>
          </div>
        ) : null}
        {messages.map((message) => (
          <MessageCard key={message.seq} message={message} actions={messageActions} />
        ))}
        {live.running && live.statusText ? (
          <div className="flex items-center gap-2 self-center text-2xs text-muted-foreground">
            <span className="dot dot-ok" />
            {live.statusText}
          </div>
        ) : null}
        {live.running || live.text || live.reasoning || live.toolCards.length > 0 ? (
          <LiveAssistantView live={live} />
        ) : null}
        {live.turnUsage ? (
          <p className="tnum self-center text-2xs text-muted-foreground">
            本轮 · ↑{live.turnUsage.inputTokens} ↓{live.turnUsage.outputTokens} · 共 {live.turnUsage.totalTokens} tokens
          </p>
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
          <div className="flex items-center gap-2 self-center rounded-lg border-[color-mix(in_srgb,var(--ember)_35%,transparent)] bg-[color-mix(in_srgb,var(--ember)_7%,transparent)] px-3 py-2 text-xs text-ember">
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

      <div className="border-t border-border bg-card/60 px-4 py-3">
        {editing ? (
          <div className="mb-2 rounded-lg border border-rose/40 bg-wash/30 p-2">
            <div className="mb-1.5 flex items-center gap-2 text-2xs text-muted-foreground">
              编辑历史消息并从这里重发（之后的消息将被替换）
              <button type="button" className="ml-auto rounded p-0.5 hover:text-foreground" onClick={() => setEditing(null)}>
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
            <Textarea
              className="min-h-[60px] text-sm"
              value={editing.text}
              onChange={(event) => setEditing({ ...editing, text: event.target.value })}
            />
            <div className="mt-1.5 flex justify-end">
              <Button
                size="sm"
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
          <div className="mb-2 flex flex-wrap gap-1.5">
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

        <div className="flex items-end gap-2">
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
          <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" title="添加附件" disabled={busy} onClick={() => fileInputRef.current?.click()}>
            <Paperclip className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" title="清空本会话消息（保留草稿与设置）" disabled={busy || messages.length === 0} onClick={onClearHistory}>
            <Eraser className="h-4 w-4" />
          </Button>
          <Textarea
            className="max-h-48 min-h-[40px] flex-1 resize-none text-sm"
            placeholder={needsModel ? '先在上方选择模型源与模型…' : '描述任务：接入协议 / 新增模型源 / 查统计 / 分析报错…；Ctrl+Enter 发送'}
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
          {busy ? (
            <Button variant="destructive" size="sm" className="h-9 shrink-0 gap-1.5" onClick={onStop}>
              <Square className="h-3.5 w-3.5" /> 停止
            </Button>
          ) : (
            <Button size="sm" className="h-9 shrink-0 gap-1.5" disabled={needsModel || (!text.trim() && documents.length === 0)} onClick={submit}>
              <Send className="h-3.5 w-3.5" /> 发送
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
