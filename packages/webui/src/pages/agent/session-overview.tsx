import { Plus, Sparkles, Trash2 } from "lucide-react";
import { Dot } from "@/components/badges";
import type { AgentSession, AgentSessionStatus } from "@/lib/agent/types";
import { compactNumber, formatRelative } from "@/lib/utils";

/**
 * AI 助手总览页：会话卡片网格。不直接进入交互窗口——点击卡片或新建任务
 * 才过渡进入具体会话的工作区。
 */
export function SessionOverview({
  sessions,
  draftSessions,
  onOpen,
  onCreate,
  onDelete,
}: {
  sessions: AgentSession[];
  /** 本机留有未发送文本或附件的会话。 */
  draftSessions?: Set<string>;
  onOpen: (id: string) => void;
  onCreate: () => void;
  onDelete: (id: string) => void;
}) {
  return (
    <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto pb-8 pt-1">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
        <button
          type="button"
          onClick={onCreate}
          className="group flex min-h-[118px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border text-muted-foreground transition-colors hover:border-rose/50 hover:text-rose"
        >
          <span className="flex h-9 w-9 items-center justify-center rounded-full border border-border transition-colors group-hover:border-rose/50">
            <Plus className="h-4 w-4" />
          </span>
          <span className="text-xs">新建任务</span>
        </button>

        {sessions.map((session) => (
          <div
            key={session.id}
            role="button"
            tabIndex={0}
            onClick={() => onOpen(session.id)}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ")
                onOpen(session.id);
            }}
            className="group relative flex min-h-[118px] cursor-pointer flex-col justify-between rounded-xl border border-border bg-card p-4 text-left transition-colors hover:border-rose/40"
          >
            <div className="flex items-start gap-2 pr-7">
              <SessionStatusDot status={session.status} />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {session.title || "未命名会话"}
                </p>
                <p className="mt-0.5 truncate text-2xs text-muted-foreground">
                  <SessionSummary session={session} hasDraft={draftSessions?.has(session.id) ?? false} />
                </p>
              </div>
            </div>
            <div className="flex items-center gap-2 text-2xs text-muted-foreground">
              {session.plan && session.plan.length > 0 ? (
                <span className="tnum">
                  方案{" "}
                  {session.plan.filter((step) => step.status === "done").length}
                  /{session.plan.length}
                </span>
              ) : null}
              {session.settings.planMode ? (
                <span className="text-amber">计划模式</span>
              ) : null}
              <span className="tnum ml-auto">
                {formatRelative(session.updatedAt)}
              </span>
            </div>
            <button
              type="button"
              aria-label="删除会话"
              title="删除会话"
              className="absolute right-2.5 top-2.5 rounded-md p-1 text-muted-foreground/60 opacity-0 transition-opacity hover:text-ember focus-visible:opacity-100 group-hover:opacity-100"
              onClick={(event) => {
                event.stopPropagation();
                onDelete(session.id);
              }}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
      </div>

      {sessions.length === 0 ? (
        <div className="mt-10 flex items-center justify-center gap-2 text-2xs text-muted-foreground">
          <Sparkles className="h-3.5 w-3.5" />
          还没有会话——点上面的卡片开始第一个任务
        </div>
      ) : null}
    </div>
  );
}

/** 卡片副文案：状态 · 轮数 · 用量，替代原先的固定能力说明。 */
function SessionSummary({ session, hasDraft }: { session: AgentSession; hasDraft: boolean }) {
  const parts = [sessionStateLabel(session, hasDraft)]
  if ((session.userTurns ?? 0) > 0) parts.push(`${session.userTurns} 轮`)
  if ((session.totalTokens ?? 0) > 0) parts.push(`↑${compactNumber(session.totalTokens ?? 0)}`)
  return <>{parts.join(' · ')}</>
}

function sessionStateLabel(session: AgentSession, hasDraft: boolean): string {
  // 审批、提问、方案确认都停在 waiting_approval，统一称待确认。
  if (session.status === 'waiting_approval') return '待确认'
  if (session.status === 'running') return '进行中'
  if (hasDraft) return '有未发送内容'
  if ((session.userTurns ?? 0) > 0) return '已结束'
  return '空对话'
}

function SessionStatusDot({ status }: { status: AgentSessionStatus }) {
  if (status === "running") return <Dot state="ok" />;
  if (status === "waiting_approval") return <Dot state="warn" />;
  return <Dot state="off" />;
}
