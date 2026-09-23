import type { AgentMessage } from "@/lib/agent/types";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { cn } from "@/lib/utils";

/**
 * 对话轮数条：会话工作区左侧的竖向导航——每个圆点对应一轮对话（一条用户
 * 消息），点击滚动定位到该轮位置；当前视口所在轮 rose 高亮，运行中在底部
 * 显示呼吸点。
 */
export function TurnRail({
  messages,
  live,
  activeSeq,
  onJump,
}: {
  messages: AgentMessage[];
  live: AgentLiveState;
  activeSeq: number | null;
  onJump: (seq: number) => void;
}) {
  const turns = messages.filter((message) => message.role === "user");
  if (turns.length === 0 && !live.running) return null;

  return (
    <nav
      aria-label="对话轮次"
      className="no-scrollbar flex w-11 shrink-0 flex-col items-center gap-1 overflow-y-auto overflow-x-hidden py-3"
    >
      {turns.map((turn, index) => {
        const active = activeSeq === turn.seq;
        const snippet = snippetOf(turn);
        return (
          <button
            key={turn.seq}
            type="button"
            title={`第 ${index + 1} 轮${snippet ? `：${snippet}` : ""}`}
            aria-label={`定位到第 ${index + 1} 轮对话`}
            aria-current={active ? "true" : undefined}
            onClick={() => onJump(turn.seq)}
            className={cn(
              "flex h-7 w-7 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-wash",
              active && "bg-wash",
            )}
          >
            <span
              className={cn(
                "tnum text-xs leading-none transition-colors",
                active ? "font-medium text-rose" : "text-muted-foreground/70",
              )}
            >
              {index + 1}
            </span>
          </button>
        );
      })}
      {live.running ? (
        <span className="dot dot-ok mt-1 shrink-0" aria-label="本轮进行中" />
      ) : null}
    </nav>
  );
}

function snippetOf(turn: AgentMessage): string {
  const content = turn.content as { text?: string } | undefined;
  const text = (content?.text ?? "").trim().replace(/\s+/g, " ");
  return text.length > 30 ? `${text.slice(0, 30)}…` : text;
}
