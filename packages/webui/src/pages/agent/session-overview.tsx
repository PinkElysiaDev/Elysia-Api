import { useState } from "react";
import { Check, Pencil, Plus, Trash2 } from "lucide-react";
import type { AgentSession } from "@/lib/agent/types";
import { cn, compactNumber, formatRelative } from "@/lib/utils";
import { EmptyText } from "@/components/ui/states";

/**
 * AI 助手总览页：会话卡片网格。不直接进入交互窗口——点击卡片或新建任务
 * 才过渡进入具体会话的工作区。卡片沿用输入容器的"有线无底"语言——
 * 默认透明仅留边框，hover 时淡填充浮现；边框色承担状态信号
 * （待确认=琥珀、进行中=玉色），待处理的会话置顶。
 * 卡片标题点击即重命名（区分重复命名的会话），与输入工具链一致。
 */
export function SessionOverview({
  sessions,
  draftSessions,
  onOpen,
  onCreate,
  onDelete,
  onEditTitle,
}: {
  sessions: AgentSession[];
  /** 本机留有未发送文本或附件的会话。 */
  draftSessions?: Set<string>;
  onOpen: (id: string) => void;
  onCreate: () => void;
  onDelete: (id: string) => void;
  onEditTitle: (id: string, title: string) => void;
}) {
  // 待处理优先：待确认（需要你来决定）> 进行中 > 有草稿 > 其余按更新时间；
  // 稳定排序，同优先级保持列表原序（后端已按 updatedAt 倒序）。
  const priorityOf = (session: AgentSession): number => {
    if (session.status === "waiting_approval") return 0;
    if (session.status === "running") return 1;
    if (draftSessions?.has(session.id)) return 2;
    return 3;
  };
  const ordered = sessions
    .map((session, index) => ({ session, index }))
    .sort((a, b) => priorityOf(a.session) - priorityOf(b.session) || a.index - b.index)
    .map(({ session }) => session);

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

        {ordered.map((session) => (
          <SessionCard
            key={session.id}
            session={session}
            hasDraft={draftSessions?.has(session.id) ?? false}
            onOpen={() => onOpen(session.id)}
            onDelete={() => onDelete(session.id)}
            onEditTitle={(title) => onEditTitle(session.id, title)}
          />
        ))}
      </div>

      {sessions.length === 0 ? (
        <EmptyText className="mt-10 text-center">还没有会话</EmptyText>
      ) : null}
    </div>
  );
}

function SessionCard({
  session,
  hasDraft,
  onOpen,
  onDelete,
  onEditTitle,
}: {
  session: AgentSession;
  hasDraft: boolean;
  onOpen: () => void;
  onDelete: () => void;
  onEditTitle: (title: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draftTitle, setDraftTitle] = useState("");
  const startEditing = () => {
    setDraftTitle(session.title ?? "");
    setEditing(true);
  };
  const commitEditing = () => {
    setEditing(false);
    const title = draftTitle.trim();
    if (title && title !== session.title) onEditTitle(title);
  };
  const plan = session.plan;
  const planDone = plan?.filter((step) => step.status === "done").length ?? 0;

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => {
        if (!editing) onOpen();
      }}
      onKeyDown={(event) => {
        if (!editing && (event.key === "Enter" || event.key === " "))
          onOpen();
      }}
      className={cn(
        "group relative flex cursor-pointer flex-col rounded-xl border bg-card p-4 text-left transition-colors hover:border-rose/40",
        session.status === "waiting_approval"
          ? "border-amber/40 hover:border-amber/60"
          : session.status === "running"
            ? "border-jade/40 hover:border-jade/60"
            : hasDraft
              ? "border-rose/30 hover:border-rose/50"
              : "border-border/70",
      )}
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1 pr-6">
          {editing ? (
            <input
              autoFocus
              value={draftTitle}
              maxLength={80}
              onChange={(event) => setDraftTitle(event.target.value)}
              onBlur={commitEditing}
              onKeyDown={(event) => {
                event.stopPropagation();
                if (event.key === "Enter") {
                  event.preventDefault();
                  commitEditing();
                } else if (event.key === "Escape") {
                  setEditing(false);
                }
              }}
              onClick={(event) => event.stopPropagation()}
              className="w-full rounded-md border border-border/70 bg-background px-1.5 py-[5px] text-sm font-medium leading-5 outline-none focus:border-foreground/30 focus:outline-none focus-visible:outline-none"
            />
          ) : (
            <p
              className="cursor-text truncate rounded-md border border-transparent px-1.5 py-[5px] text-sm font-medium leading-5 transition-colors hover:border-border/40"
              onClick={(event) => {
                event.stopPropagation();
                startEditing();
              }}
              title="点击重命名"
            >
              {session.title || "未命名会话"}
            </p>
          )}
          <p className="mt-0.5 truncate text-2xs text-muted-foreground">
            <SessionSummary session={session} hasDraft={hasDraft} />
          </p>
        </div>
        <div className="ml-auto flex shrink-0 flex-col items-end gap-0.5 pt-0.5">
          <button
            type="button"
            aria-label={editing ? "确认重命名" : "重命名会话"}
            title={editing ? "确认重命名" : "重命名会话"}
            className={cn(
              "rounded-md p-1 transition-opacity",
              editing
                ? "text-jade hover:text-jade/80"
                : "text-muted-foreground/60 opacity-0 hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100",
            )}
            onClick={(event) => {
              event.stopPropagation();
              if (editing) commitEditing();
              else startEditing();
            }}
          >
            {editing ? (
              <Check className="h-3.5 w-3.5" />
            ) : (
              <Pencil className="h-3.5 w-3.5" />
            )}
          </button>
          <button
            type="button"
            aria-label="删除会话"
            title="删除会话"
            className="rounded-md p-1 text-muted-foreground/60 opacity-0 transition-opacity hover:text-ember focus-visible:opacity-100 group-hover:opacity-100"
            onClick={(event) => {
              event.stopPropagation();
              onDelete();
            }}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
      {plan && plan.length > 0 ? (
        <div className="mt-2 flex items-center gap-2 text-2xs text-muted-foreground">
          <span className="tnum">
            方案 {planDone}/{plan.length}
          </span>
          {session.settings.planMode ? (
            <span className="text-amber">计划模式</span>
          ) : null}
          <span className="tnum ml-auto">{formatRelative(session.updatedAt)}</span>
        </div>
      ) : null}
    </div>
  );
}

/** 卡片副文案：状态 · 轮数 · 用量，替代原先的固定能力说明。 */
function SessionSummary({
  session,
  hasDraft,
}: {
  session: AgentSession;
  hasDraft: boolean;
}) {
  const parts = [sessionStateLabel(session, hasDraft)];
  if ((session.userTurns ?? 0) > 0) parts.push(`${session.userTurns} 轮`);
  if ((session.totalTokens ?? 0) > 0)
    parts.push(`↑${compactNumber(session.totalTokens ?? 0)}`);
  parts.push(formatRelative(session.updatedAt));
  return <>{parts.join(" · ")}</>;
}

function sessionStateLabel(session: AgentSession, hasDraft: boolean): string {
  // 审批、提问、方案确认都停在 waiting_approval，统一称待确认。
  if (session.status === "waiting_approval") return "待确认";
  if (session.status === "running") return "进行中";
  if (hasDraft) return "有未发送内容";
  if ((session.userTurns ?? 0) > 0) return "已结束";
  return "空对话";
}
