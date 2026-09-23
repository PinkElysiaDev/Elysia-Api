import { useState } from "react";
import { ArrowUp, ShieldCheck, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toolArgsPreview } from "@/lib/agent/mask";
import {
  agentToolLabel,
  type AgentAskQuestion,
  type AgentPlanStep,
} from "@/lib/agent/types";

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
