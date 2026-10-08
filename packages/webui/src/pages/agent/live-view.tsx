import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { closeUnbalancedFences } from "@/lib/agent/fences";
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
          <div className="min-w-0 max-w-[80ch] space-y-2">
            <ReasoningBlock text={live.reasoning} />
            {live.text ? <Markdown text={closeUnbalancedFences(live.text)} /> : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
