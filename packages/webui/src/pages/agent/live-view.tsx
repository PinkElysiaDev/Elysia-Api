import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { Markdown, ReasoningBlock } from "./markdown";
import { ToolCallRow } from "./tool-call-row";

/** 进行中的现场气泡（流式增量 + 工具卡片）。 */
export function LiveAssistantView({
  live,
  onOpenActivity,
}: {
  live: AgentLiveState;
  onOpenActivity?: () => void;
}) {
  const hasContent = live.text || live.reasoning;
  return (
    <div className="flex flex-col gap-2">
      {hasContent ? (
        <div>
          <div className="min-w-0 space-y-2">
            <ReasoningBlock text={live.reasoning} />
            {live.text ? <Markdown text={live.text} /> : null}
          </div>
        </div>
      ) : null}
      {live.toolCards.map((card) => (
        <ToolCallRow
          key={card.callId}
          name={card.name}
          status={card.status}
          summary={card.summary}
          command={card.command}
          elapsedMs={card.elapsedMs}
          startedAt={card.startedAt}
          onOpen={onOpenActivity}
        />
      ))}
    </div>
  );
}
