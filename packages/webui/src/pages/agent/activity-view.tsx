import { Loader2, Wrench } from "lucide-react";
import { useState } from "react";
import { JsonBlock } from "./ui-blocks";
import { toolArgsPreview } from "@/lib/agent/mask";
import {
  agentToolLabel,
  type AgentMessage,
  type AgentToolResultContent,
} from "@/lib/agent/types";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { cn } from "@/lib/utils";

/** 动态页：运行中卡片 + 可选中的执行详情（参数/结果）+ 近期执行列表。 */
export function ActivityView({
  messages,
  live,
}: {
  messages: AgentMessage[];
  live: AgentLiveState;
}) {
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null);
  const history = messages
    .filter((message) => message.role === "tool_result")
    .map((message) => ({
      seq: message.seq,
      content: message.content as AgentToolResultContent,
    }))
    .filter((entry) => entry.content);

  const runningCard = live.toolCards.find((card) => card.status === "running");
  const selected =
    history.find((entry) => entry.seq === selectedSeq) ??
    history[history.length - 1];

  if (!selected && !runningCard && live.toolCards.length === 0) {
    return (
      <EmptyHint
        icon={<Wrench className="h-4 w-4" />}
        text="点击对话中的工具执行行，可在这里查看参数与结果详情。"
      />
    );
  }

  const recent = [...history].reverse().slice(0, 12);

  return (
    <div className="space-y-3 px-1 py-1">
      {runningCard ? (
        <div className="rounded-lg bg-wash px-3 py-2.5">
          <div className="flex items-center gap-1.5 text-xs font-medium">
            <Loader2 className="h-3.5 w-3.5 animate-spin text-jade" />
            {agentToolLabel(runningCard.name)}
            <span className="ml-auto text-2xs font-normal text-muted-foreground">
              执行中…
            </span>
          </div>
        </div>
      ) : null}

      {selected ? (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs">
            <Wrench className="h-3.5 w-3.5 text-muted-foreground/60" />
            <span className="font-medium">{selected.content.name}</span>
            <span
              className={cn(
                "text-2xs",
                selected.content.ok ? "text-jade" : "text-ember",
              )}
            >
              {selected.content.ok ? "成功" : "失败"}
            </span>
            {selected.content.durationMs ? (
              <span className="tnum text-2xs text-muted-foreground">
                {selected.content.durationMs}ms
              </span>
            ) : null}
          </div>
          {selected.content.summary ? (
            <p className="text-2xs leading-relaxed text-muted-foreground">
              {selected.content.summary}
            </p>
          ) : null}
          {selected.content.input != null &&
          toolArgsPreview(selected.content.input) ? (
            <div>
              <p className="mb-1 text-2xs font-medium text-muted-foreground">
                参数
              </p>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap rounded-[7px] border border-border bg-code px-2.5 py-2 font-mono text-2xs leading-relaxed">
                {toolArgsPreview(selected.content.input)}
              </pre>
            </div>
          ) : null}
          {selected.content.data != null ? (
            <div>
              <p className="mb-1 text-2xs font-medium text-muted-foreground">
                结果
              </p>
              <JsonBlock value={selected.content.data} maxHeight="max-h-64" />
            </div>
          ) : null}
        </div>
      ) : null}

      {recent.length > 0 ? (
        <div className="space-y-1 border-t border-border/50 pt-2">
          <p className="px-0.5 text-2xs font-medium text-muted-foreground">
            近期执行
          </p>
          {recent.map((entry) => (
            <button
              key={entry.seq}
              type="button"
              className={cn(
                "flex w-full items-center gap-1.5 rounded-md px-0.5 py-1 text-left text-2xs transition-colors hover:text-foreground",
                selected?.seq === entry.seq
                  ? "text-rose"
                  : "text-muted-foreground",
              )}
              onClick={() => setSelectedSeq(entry.seq)}
            >
              <span
                className={cn(
                  "h-1.5 w-1.5 shrink-0 rounded-full",
                  entry.content.ok ? "bg-jade" : "bg-ember",
                )}
              />
              <span className="min-w-0 flex-1 truncate">
                {agentToolLabel(entry.content.name)}
              </span>
              {entry.content.durationMs ? (
                <span className="tnum shrink-0 text-muted-foreground">
                  {entry.content.durationMs}ms
                </span>
              ) : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/** 三个视图共用的空态提示。 */
export function EmptyHint({ icon, text }: { icon: React.ReactNode; text: string }) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-12 text-center text-2xs text-muted-foreground">
      <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary">
        {icon}
      </span>
      <p className="leading-relaxed">{text}</p>
    </div>
  );
}
