import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  AlertTriangle,
  ArrowUp,
  FileText,
  Pencil,
  RefreshCw,
  ShieldCheck,
  X,
} from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { colorize } from "@/lib/json-highlight";
import { ChartBlock } from "./chart-block";
import { ToolCallRow } from "./tool-call-row";
import { Collapse, JsonBlock } from "./ui-blocks";
import { parseChartSpec } from "@/lib/agent/chart";
import { toolArgsPreview } from "@/lib/agent/mask";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import {
  agentToolLabel,
  formatUsage,
  type AgentApprovalContent,
  type AgentAskQuestion,
  type AgentAssistantContent,
  type AgentMessage,
  type AgentPlanStep,
  type AgentSystemContent,
  type AgentToolResultContent,
  type AgentUserContent,
} from "@/lib/agent/types";
import { cn } from "@/lib/utils";

/** Markdown 渲染（代码块复用 JSON 高亮，chart 围栏渲染为图表）。 */
function Markdown({ text }: { text: string }) {
  return (
    <div className="agent-markdown space-y-2 break-words text-sm leading-relaxed">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => (
            <div className="overflow-x-auto text-xs">{children}</div>
          ),
          code: ({ className, children, ...props }) => {
            const raw = String(children ?? "");
            const isBlock = /language-/.test(className ?? "");
            if (isBlock) {
              const language = /language-([A-Za-z0-9_-]+)/.exec(
                className ?? "",
              )?.[1];
              if (language === "chart") {
                const spec = parseChartSpec(raw);
                if (spec) return <ChartBlock spec={spec} />;
              }
              return (
                <pre
                  className="max-h-80 overflow-auto rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]"
                  dangerouslySetInnerHTML={{ __html: colorize(raw) }}
                />
              );
            }
            return (
              <code className="rounded bg-muted px-1 py-0.5 text-xs" {...props}>
                {children}
              </code>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

/** 思维链折叠块。 */
function ReasoningBlock({ text }: { text: string }) {
  if (!text.trim()) return null;
  return (
    <Collapse
      stopPropagation
      title="思考过程"
      tone="amber"
      icon={<span className="text-amber">💭</span>}
    >
      <p className="whitespace-pre-wrap text-2xs leading-relaxed text-muted-foreground">
        {text}
      </p>
    </Collapse>
  );
}

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
          elapsedMs={card.elapsedMs}
          startedAt={card.startedAt}
          onOpen={onOpenActivity}
        />
      ))}
    </div>
  );
}

export function ApprovalCard({
  approval,
  onApprove,
  onDeny,
  busy,
}: {
  approval: {
    calls: { name?: string; arguments?: unknown }[];
    reason?: string;
  };
  onApprove: (extra?: {
    baseUrl?: string;
    apiKey?: string;
    note?: string;
  }) => void;
  onDeny: (note?: string) => void;
  busy?: boolean;
}) {
  const argsPreview = approval.calls
    .map((call) => toolArgsPreview(call.arguments))
    .filter(Boolean)
    .join("\n");
  const [credentialsOpen, setCredentialsOpen] = useState(false);
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [note, setNote] = useState("");
  const extra = {
    baseUrl: baseUrl.trim() || undefined,
    apiKey: apiKey.trim() || undefined,
    note: note.trim() || undefined,
  };
  return (
    <div className="tone-amber w-full space-y-2.5 rounded-xl border px-3.5 py-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <ShieldCheck className="h-4 w-4 text-amber" />
        请求批准：
        {approval.calls
          .map((call) => agentToolLabel(call.name ?? ""))
          .join("、")}
      </div>
      {approval.reason ? (
        <p className="line-clamp-4 text-xs text-muted-foreground">
          助手说明：{approval.reason}
        </p>
      ) : null}
      {argsPreview ? (
        <div className="rounded-lg border border-border/70 bg-card px-3 py-2">
          <p className="mb-1.5 text-2xs font-medium text-muted-foreground">
            执行参数
          </p>
          <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-2xs leading-relaxed">
            {argsPreview}
          </pre>
        </div>
      ) : null}
      <div className="flex items-center gap-2">
        <Button size="sm" disabled={busy} onClick={() => onApprove(extra)}>
          允许
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          onClick={() => onDeny(note.trim() || undefined)}
        >
          <X className="h-3.5 w-3.5" /> 拒绝
        </Button>
        <button
          type="button"
          className="text-2xs text-muted-foreground hover:text-foreground"
          onClick={() => setCredentialsOpen((value) => !value)}
        >
          {credentialsOpen ? "收起凭证" : "补充测试凭证"}
        </button>
      </div>
      {credentialsOpen ? (
        <div className="grid gap-1.5">
          <input
            className="rounded-md border border-border bg-card px-2 py-1 text-xs"
            placeholder="上游 Base URL（可选）"
            value={baseUrl}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
          <input
            className="rounded-md border border-border bg-card px-2 py-1 text-xs"
            placeholder="API Key（可选，仅本次会话记住）"
            type="password"
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
          />
          <input
            className="rounded-md border border-border bg-card px-2 py-1 text-xs"
            placeholder="给助手的备注（可选）"
            value={note}
            onChange={(event) => setNote(event.target.value)}
          />
        </div>
      ) : null}
    </div>
  );
}

/** ask_user 的作答卡：点选项或输入自定义答案。 */
export function QuestionCard({
  question,
  busy,
  onAnswer,
}: {
  question: AgentAskQuestion;
  busy?: boolean;
  onAnswer: (answer: string) => void;
}) {
  const [custom, setCustom] = useState("");
  return (
    <div className="tone-amber w-full space-y-2 rounded-xl border px-3.5 py-3">
      <p className="text-sm font-medium">{question.question}</p>
      <div className="flex flex-wrap gap-1.5">
        {(question.options ?? []).map((option) => (
          <button
            key={option.label}
            type="button"
            disabled={busy}
            title={option.description}
            className="rounded-full border border-border bg-card px-3 py-1 text-xs hover:bg-wash disabled:opacity-50"
            onClick={() => onAnswer(option.label)}
          >
            {option.label}
          </button>
        ))}
      </div>
      {question.allowCustom !== false ? (
        <form
          className="flex gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (custom.trim()) onAnswer(custom.trim());
          }}
        >
          <input
            className="min-w-0 flex-1 rounded-md border border-border bg-card px-2 py-1 text-xs"
            placeholder="或者输入你的答案"
            value={custom}
            onChange={(event) => setCustom(event.target.value)}
          />
          <Button size="sm" type="submit" disabled={busy || !custom.trim()}>
            回答
          </Button>
        </form>
      ) : null}
    </div>
  );
}

/** 方案定稿确认：确认后后端关闭计划模式并继续执行。 */
export function PlanConfirmCard({
  plan,
  busy,
  onConfirm,
  onRevise,
}: {
  plan: AgentPlanStep[];
  busy?: boolean;
  onConfirm: () => void;
  onRevise: (note: string) => void;
}) {
  const [note, setNote] = useState("");
  return (
    <div className="tone-amber w-full space-y-2 rounded-xl border px-3.5 py-3">
      <p className="text-sm font-medium">方案已定稿，确认后开始执行</p>
      <ol className="space-y-1 text-xs text-muted-foreground">
        {plan.map((step, index) => (
          <li key={step.title}>
            {index + 1}. {step.title}
          </li>
        ))}
      </ol>
      <div className="flex items-center gap-2">
        <Button size="sm" disabled={busy} onClick={onConfirm}>
          执行方案
        </Button>
      </div>
      {/* 有内容就一定是修改意见：空内容不发送，Ctrl/Cmd+Enter 与按钮等效。 */}
      <form
        className="flex items-center gap-1.5"
        onSubmit={(event) => {
          event.preventDefault();
          const trimmed = note.trim();
          if (!trimmed || busy) return;
          onRevise(trimmed);
        }}
      >
        <input
          className="min-w-0 flex-1 rounded-md border border-border bg-card px-2 py-1 text-xs"
          placeholder="不同意见（Ctrl+Enter 发送）"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== "Enter") return;
            // 裸 Enter 是换行习惯，只有 Ctrl/Cmd+Enter 才发送（与主输入框一致）；
            // 不拦的话表单会隐式提交，与提示语矛盾。
            event.preventDefault();
            if (event.ctrlKey || event.metaKey) {
              event.currentTarget.form?.requestSubmit();
            }
          }}
        />
        <Button
          type="submit"
          variant="ghost"
          size="icon"
          className="h-7 w-7 shrink-0 rounded-full text-muted-foreground hover:bg-primary hover:text-primary-foreground"
          title="发送修改意见（Ctrl+Enter）"
          disabled={busy || !note.trim()}
        >
          <ArrowUp className="h-3.5 w-3.5" />
        </Button>
      </form>
    </div>
  );
}
