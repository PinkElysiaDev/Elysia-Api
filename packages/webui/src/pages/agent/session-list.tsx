import { MessageSquarePlus, Trash2 } from 'lucide-react'
import { Dot } from '@/components/badges'
import { Button } from '@/components/ui/button'
import type { AgentSession, AgentSessionStatus } from '@/lib/agent/types'
import { cn, formatRelative } from '@/lib/utils'

/** 左侧会话列表：新会话、删除（走页面级确认弹窗）、状态点。 */
export function SessionList({
  sessions,
  activeId,
  onSelect,
  onCreate,
  onDelete,
}: {
  sessions: AgentSession[]
  activeId?: string
  onSelect: (id: string) => void
  onCreate: () => void
  onDelete: (id: string) => void
}) {
  return (
    <div className="flex h-full w-64 shrink-0 flex-col border-r border-border bg-card/40 max-rail:hidden">
      <div className="flex items-center gap-2 px-3 py-2.5">
        <span className="text-sm font-semibold">会话</span>
        <Button size="sm" variant="outline" className="ml-auto h-7 gap-1 px-2 text-2xs" onClick={onCreate}>
          <MessageSquarePlus className="h-3.5 w-3.5" /> 新会话
        </Button>
      </div>
      <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
        {sessions.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground">还没有会话，点「新会话」开始</p>
        ) : null}
        {sessions.map((session) => (
          <div
            key={session.id}
            role="button"
            tabIndex={0}
            className={cn(
              'group flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-xs transition-colors hover:bg-wash',
              session.id === activeId && 'bg-muted font-medium',
            )}
            onClick={() => onSelect(session.id)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' || event.key === ' ') onSelect(session.id)
            }}
          >
            <SessionStatusDot status={session.status} />
            <div className="min-w-0 flex-1">
              <p className="truncate">{session.title || '未命名会话'}</p>
              <p className="truncate text-2xs text-muted-foreground">
                {session.mode === 'edit' ? `编辑协议 ${session.protocolId ?? ''}` : '通用会话'}
                <span className="mx-1" aria-hidden>·</span>
                {formatRelative(session.updatedAt)}
              </p>
            </div>
            <Button
              variant="danger"
              size="iconSm"
              aria-label="删除会话"
              className="opacity-0 transition-opacity group-hover:opacity-100"
              onClick={(event) => {
                event.stopPropagation()
                onDelete(session.id)
              }}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}
      </div>
    </div>
  )
}

function SessionStatusDot({ status }: { status: AgentSessionStatus }) {
  if (status === 'running') return <Dot state="ok" />
  if (status === 'waiting_approval') return <Dot state="warn" />
  return <Dot state="off" />
}
