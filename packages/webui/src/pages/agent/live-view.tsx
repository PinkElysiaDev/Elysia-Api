import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { Markdown, ReasoningBlock } from "./markdown";

/** 进行中的正文与思考；工具由消息流统一分组。 */
export function LiveAssistantView({
  live,
}: {
  live: AgentLiveState;
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
    </div>
  );
}
