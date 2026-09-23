import {
  AlertTriangle,
  FileText,
  Pencil,
  RefreshCw,
  ShieldCheck,
} from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { ToolCallRow } from "./tool-call-row";
import { Collapse, JsonBlock } from "./ui-blocks";
import { Markdown, ReasoningBlock } from "./markdown";
import {
  formatUsage,
  type AgentApprovalContent,
  type AgentAssistantContent,
  type AgentMessage,
  type AgentSystemContent,
  type AgentToolResultContent,
  type AgentUserContent,
} from "@/lib/agent/types";
import { cn } from "@/lib/utils";

/** 工具结果行（持久化消息形态）。失败保持展开，成功默认折叠详情。 */
function ToolResultCard({ content }: { content: AgentToolResultContent }) {
  const denied = !content.ok && isDenied(content.data);
  return (
    <ToolCallRow
      name={content.name}
      status={denied ? "denied" : content.ok ? "done" : "failed"}
      summary={content.summary}
      durationMs={content.durationMs}
      detail={
        content.data != null ? (
          <Collapse stopPropagation title="结果详情" defaultOpen={!content.ok}>
            <JsonBlock value={content.data} />
          </Collapse>
        ) : null
      }
    />
  );
}

function isDenied(data: unknown): boolean {
  if (!data || typeof data !== "object") return false;
  return (data as { error?: string }).error === "denied";
}

export interface MessageActions {
  onRetry: (message: AgentMessage) => void;
  onEditResend: (message: AgentMessage) => void;
  onRegenerate: (message: AgentMessage) => void;
}

/** 持久化消息卡片。 */
export function MessageCard({
  message,
  actions,
}: {
  message: AgentMessage;
  actions?: MessageActions;
}) {
  if (message.role === "user") {
    const content = message.content as AgentUserContent;
    const text = content.text ?? "";
    return (
      <div className="group flex flex-col items-end gap-1">
        <div className="max-w-bubble space-y-1.5 rounded-2xl rounded-br-md bg-wash px-4 py-2.5 text-sm">
          {text ? (
            <p className="whitespace-pre-wrap break-words">{text}</p>
          ) : null}
          {(content.documents ?? []).length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {(content.documents ?? []).map((doc, index) => (
                <span
                  key={index}
                  className="inline-flex items-center gap-1 rounded-md bg-card px-1.5 py-0.5 text-2xs text-muted-foreground"
                >
                  <FileText className="h-3 w-3" />
                  {doc.name ?? `材料 ${index + 1}`}
                </span>
              ))}
            </div>
          ) : null}
        </div>
        {actions ? (
          <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
            <CopyButton value={text} aria-label="复制" />
            <Button
              variant="ghost"
              size="iconSm"
              aria-label="编辑后重发"
              title="编辑后重发"
              onClick={() => actions.onEditResend(message)}
            >
              <Pencil className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="iconSm"
              aria-label="原样重试"
              title="原样重试"
              onClick={() => actions.onRetry(message)}
            >
              <RefreshCw className="h-3.5 w-3.5" />
            </Button>
          </div>
        ) : null}
      </div>
    );
  }

  if (message.role === "assistant") {
    const content = message.content as AgentAssistantContent;
    return (
      <div className="group">
        <div className="min-w-0 space-y-2">
          <ReasoningBlock text={content.reasoning ?? ""} />
          {content.text ? <Markdown text={content.text} /> : null}
          {/* 工具调用不在此重复展示：紧随其后的工具结果行（或流式期间的
              live 卡）已完整表达，chips 只会加噪音。 */}
          <div className="flex items-center gap-1.5 pt-0.5 text-2xs text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100">
            {message.model ? <span>{message.model}</span> : null}
            {message.usage ? <span>{formatUsage(message.usage)}</span> : null}
            {content.text ? (
              <CopyButton value={content.text} aria-label="复制" />
            ) : null}
            {actions ? (
              <Button
                variant="ghost"
                size="iconSm"
                aria-label="重新生成本条回复"
                title="重新生成本条回复"
                onClick={() => actions.onRegenerate(message)}
              >
                <RefreshCw className="h-3 w-3" />
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    );
  }

  if (message.role === "tool_result") {
    return (
      <ToolResultCard content={message.content as AgentToolResultContent} />
    );
  }

  if (message.role === "approval") {
    const content = message.content as AgentApprovalContent;
    return (
      <div className="flex items-center gap-1.5 self-center rounded-full border border-border bg-muted/40 px-3 py-1 text-2xs text-muted-foreground">
        <ShieldCheck
          className={cn(
            "h-3.5 w-3.5",
            content.decision === "approved" ? "text-jade" : "text-ember",
          )}
        />
        {content.decision === "approved" ? "已允许" : "已拒绝"}：
        {content.names?.join("、") ?? ""}
      </div>
    );
  }

  const content = message.content as AgentSystemContent;
  return (
    <div
      className={cn(
        "flex items-start gap-1.5 self-center rounded-md px-3 py-1.5 text-2xs",
        content?.kind === "error"
          ? "tone-ember border"
          : "bg-muted/50 text-muted-foreground",
      )}
    >
      {content?.kind === "error" ? (
        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      ) : null}
      <span className="line-clamp-3">{content?.text ?? ""}</span>
    </div>
  );
}
