import {
  AlertTriangle,
  FileText,
  Pencil,
  ShieldCheck,
} from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { MessageImages } from "./message-images";
import { Markdown, ReasoningBlock } from "./markdown";
import {
  agentToolLabel,
  isImageDocument,
  type AgentApprovalContent,
  type AgentAssistantContent,
  type AgentMessage,
  type AgentSystemContent,
  type AgentUserContent,
} from "@/lib/agent/types";
import { cn } from "@/lib/utils";

export interface MessageActions {
  onRetry: (message: AgentMessage) => void;
  onEditResend: (message: AgentMessage) => void;
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
    const documents = content.documents ?? [];
    // chip 回退编号沿用原始 documents 下标（与图片附件共存的场景编号不断层）。
    const files = documents
      .map((doc, index) => ({ doc, index }))
      .filter((item) => !isImageDocument(item.doc));
    return (
      <div className="group flex flex-col items-end gap-1">
        <div className="max-w-bubble space-y-1.5 rounded-2xl rounded-br-md bg-wash px-4 py-2.5 text-sm">
          {text ? (
            <p className="whitespace-pre-wrap break-words">{text}</p>
          ) : null}
          <MessageImages documents={documents} />
          {files.length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {files.map(({ doc, index }) => (
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
          </div>
        ) : null}
      </div>
    );
  }

  if (message.role === "assistant") {
    const content = message.content as AgentAssistantContent;
    if (!content.text?.trim() && !content.reasoning?.trim()) return null;
    return (
      <div className="min-w-0 space-y-2 max-w-[80ch]">
        <ReasoningBlock text={content.reasoning ?? ""} />
        {content.text ? <Markdown text={content.text} /> : null}
        {/* 工具调用不在此重复展示：紧随其后的工具结果行（或流式期间的
            live 卡）已完整表达，chips 只会加噪音。 */}
      </div>
    );
  }

  // 工具消息由 MessageStream 合并为 ToolCallGroup。
  if (message.role === "tool_result") return null;

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
        {(content.names ?? []).map((name) => agentToolLabel(name)).join("、")}
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
