import type { AgentAssistantContent, AgentMessage, AgentToolResultContent } from "@/lib/agent/types";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import type { ReactNode } from "react";
import { withToolInputs } from "@/lib/agent/tool-inputs";
import { RefreshCw } from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { MessageCard, type MessageActions } from "./message-card";
import { LiveAssistantView } from "./live-view";
import { ToolCallGroup } from "./tool-call-group";
import type { ToolCallEntry } from "./tool-call-row";

type StreamItem =
  | { kind: "message"; key: string; message: AgentMessage }
  | { kind: "live"; key: string }
  | { kind: "turn-actions"; key: string; user: AgentMessage; text: string }
  | { kind: "tools"; key: string; calls: ToolCallEntry[] };

/** 历史与现场共用一条消息流：只跨过不可见的空助手消息合并工具。 */
export function MessageStream({
  messages,
  live,
  actions,
  suppressedErrorSeq,
  turnPending = live.running || Boolean(live.approvalPending),
  children,
}: {
  messages: AgentMessage[];
  live: AgentLiveState;
  actions?: MessageActions;
  suppressedErrorSeq?: number;
  /** 含远程执行与等待审批：这些都属于尚未结束的当前轮。 */
  turnPending?: boolean;
  children?: ReactNode;
}) {
  const items: StreamItem[] = [];
  const persistedCalls = new Set<string>();
  let turnSeq = 0;
  let turnUser: AgentMessage | undefined;
  let turnText: string[] = [];
  let hasResponse = false;
  const appendTurnActions = () => {
    if (!turnUser || !hasResponse) return;
    items.push({ kind: "turn-actions", key: `turn-actions:${turnUser.seq}`, user: turnUser, text: turnText.join("\n\n") });
  };
  const appendTool = (call: ToolCallEntry) => {
    const last = items[items.length - 1];
    if (last?.kind === "tools") last.calls.push(call);
    else items.push({ kind: "tools", key: `tools:${call.id}`, calls: [call] });
  };
  for (const message of withToolInputs(messages)) {
    if (message.role === "user") {
      appendTurnActions();
      turnUser = message;
      turnText = [];
      hasResponse = false;
    } else {
      hasResponse = true;
    }
    if (message.seq === suppressedErrorSeq && message.role === "system") continue;
    if (message.role === "user") turnSeq = message.seq;
    if (message.role === "assistant") {
      const content = message.content as AgentAssistantContent;
      if (content.text?.trim()) turnText.push(content.text.trim());
      if (!content.text?.trim() && !content.reasoning?.trim()) continue;
    }
    if (message.role === "tool_result") {
      const content = message.content as AgentToolResultContent;
      const id = content.callId ? `${turnSeq}:${content.callId}` : `seq:${message.seq}`;
      persistedCalls.add(id);
      const denied = !content.ok && (content.data as { error?: string } | null)?.error === "denied";
      appendTool({
        id,
        seq: message.seq,
        name: content.name,
        status: denied ? "denied" : content.ok ? "done" : "failed",
        input: content.input,
        result: content.data,
        summary: content.summary,
        durationMs: content.durationMs,
      });
    } else {
      items.push({ kind: "message", key: `message:${message.seq}`, message });
    }
  }
  if (live.text.trim() || live.reasoning.trim()) items.push({ kind: "live", key: "live-content" });
  for (const call of live.toolCards) {
    const id = `${turnSeq}:${call.callId}`;
    if (!persistedCalls.has(id)) appendTool({ ...call, id });
  }
  if (live.text.trim()) turnText.push(live.text.trim());
  if (live.text || live.reasoning || live.toolCards.length > 0) hasResponse = true;

  return (
    <>
      {items.map((item) => (
        <div key={item.key} data-seq={item.kind === "message" ? item.message.seq : undefined}>
          {item.kind === "tools" ? <ToolCallGroup calls={item.calls} />
            : item.kind === "live" ? <LiveAssistantView live={live} />
              : item.kind === "turn-actions" ? (
                <TurnActions user={item.user} text={item.text} actions={actions} disabled={turnPending} />
              )
              : <MessageCard message={item.message} actions={actions} />}
        </div>
      ))}
      {live.running && live.statusText && !live.text && !live.toolCards.some((call) => call.status === "running") ? (
        <div role="status" className="pl-1 text-2xs text-muted-foreground">{live.statusText}</div>
      ) : null}
      {children}
      {!turnPending && turnUser && hasResponse ? (
        <TurnActions user={turnUser} text={turnText.join("\n\n")} actions={actions} />
      ) : null}
    </>
  );
}

function TurnActions({ user, text, actions, disabled }: {
  user: AgentMessage;
  text: string;
  actions?: MessageActions;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-center gap-0.5 pb-1" aria-label="本轮操作">
      {text ? <CopyButton value={text} aria-label="复制本轮正文" title="复制本轮正文" /> : null}
      {actions ? (
        <Button variant="ghost" size="iconSm" aria-label="重试本轮" title="重试本轮" disabled={disabled} onClick={() => actions.onRetry(user)}>
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      ) : null}
    </div>
  );
}
