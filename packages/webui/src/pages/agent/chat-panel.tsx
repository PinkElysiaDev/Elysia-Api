import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  FileText,
  Plus,
  Square,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useToast } from "@/components/ui/use-toast";
import { useModels } from "@/lib/hooks";
import type { Model, ModelSource } from "@/lib/types";
import {
  agentUsageToTurn,
  AgentContextTab,
  AgentDocument,
  AgentMessage,
  AgentSession,
  AgentSettings,
} from "@/lib/agent/types";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { cn, compactNumber, formatHitRate } from "@/lib/utils";
import { PermissionMenu, ThinkingMenu } from "./composer-menu";
import { useComposerAttachments } from "./use-composer-attachments";
import { ModelPicker } from "./model-picker";
import {
  ApprovalCard,
  LiveAssistantView,
  MessageCard,
  PlanConfirmCard,
  QuestionCard,
} from "./message-card";

export interface ChatPanelProps {
  session: AgentSession;
  messages: AgentMessage[];
  live: AgentLiveState;
  onSend: (input: {
    content?: string;
    documents?: AgentDocument[];
    afterSeq?: number;
  }) => void;
  onApprove: (decision: {
    approved: boolean;
    baseUrl?: string;
    apiKey?: string;
    note?: string;
    answer?: string;
  }) => void;
  onStop: () => void;
  onOpenContextTab: (tab: AgentContextTab) => void;
  onSettingsChange: (patch: {
    settings?: Partial<AgentSettings>;
  }) => Promise<boolean | void> | void;
  /** 关闭现场报错横幅（关闭后被抑制的落库红卡自然回归）。 */
  onDismissError: () => void;
  /** 轮数条跳转目标：变化时滚动定位到对应消息（nonce 保证重复点击也生效）。 */
  jumpTarget?: { seq: number; nonce: number } | null;
  /** 滚动时上报当前视口所在的轮（最近一条用户消息 seq）。 */
  onActiveTurn?: (seq: number | null) => void;
}

/**
 * 聊天面板：扁平消息流 + ComposerDock（输入与全部会话设置一体）。
 * 输入容器是页面唯一刻意抬升的元素；消息直接浮在背景上。
 * 底部控制条从左到右：附加 / 权限控制 / 会话统计 / 模型 / 思考强度 / 发送。
 */
export function ChatPanel({
  session,
  messages,
  live,
  onSend,
  onApprove,
  onStop,
  onOpenContextTab,
  onSettingsChange,
  onDismissError,
  jumpTarget,
  onActiveTurn,
}: ChatPanelProps) {
  const { toast } = useToast();
  const [text, setText] = useState("");
  const notify = (description: string) => toast({ description });
  const { documents, setDocuments, addFiles } = useComposerAttachments(notify);
  const [dragOver, setDragOver] = useState(false);
  const [editing, setEditing] = useState<{ seq: number; text: string } | null>(
    null,
  );
  const [saving, setSaving] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  // 用户上翻离开底部超过这个距离就停止自动跟随，避免流式增量把正在回看的
  // 历史拽回底部。
  const stickToBottomRef = useRef(true);
  const [showJumpBottom, setShowJumpBottom] = useState(false);
  const lastSentRef = useRef<{
    content: string;
    documents: AgentDocument[];
    priorLastUserSeq: number;
  } | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const busy = live.running;
  const approval = live.approvalPending;
  const settings = session.settings;
  const needsModel = !settings.modelSourceId || !settings.modelName;

  const { data: models } = useModels();
  const selectedModel = (models ?? []).find(
    (model) =>
      model.sourceId === settings.modelSourceId &&
      model.name === settings.modelName,
  );
  const contextLimit = selectedModel?.maxTokens ?? 0;

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages.length, live.text, live.toolCards.length, approval]);

  /** 轮数条跳转：滚动到目标消息。 */
  useEffect(() => {
    if (!jumpTarget) return;
    const el = scrollRef.current?.querySelector(
      `[data-seq="${jumpTarget.seq}"]`,
    );
    el?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [jumpTarget]);

  /** 滚动时上报当前所在轮（最近一条未滚出顶部的用户消息）。 */
  useEffect(() => {
    const container = scrollRef.current;
    if (!container || !onActiveTurn) return;
    const userSeqs = new Set(
      messages
        .filter((message) => message.role === "user")
        .map((message) => message.seq),
    );
    const report = () => {
      let active: number | null = null;
      const threshold = container.clientHeight / 2;
      for (const el of Array.from(
        container.querySelectorAll<HTMLElement>("[data-seq]"),
      )) {
        const seq = Number(el.dataset.seq);
        if (
          userSeqs.has(seq) &&
          el.offsetTop - container.scrollTop <= threshold
        )
          active = seq;
      }
      onActiveTurn(active);
      const distance =
        container.scrollHeight - container.scrollTop - container.clientHeight;
      stickToBottomRef.current = distance < 120;
      setShowJumpBottom(distance >= 120);
    };
    container.addEventListener("scroll", report, { passive: true });
    report();
    return () => container.removeEventListener("scroll", report);
  }, [messages, onActiveTurn]);

  /** 会话累计用量（含缓存命中），随助手消息持久化逐步累加。 */
  const usageStat = useMemo(() => {
    let input = 0;
    let output = 0;
    let total = 0;
    let cached = 0;
    for (const message of messages) {
      if (message.role !== "assistant" || !message.usage) continue;
      const turn = agentUsageToTurn(message.usage);
      input += turn.inputTokens;
      output += turn.outputTokens;
      total += turn.totalTokens;
      cached += Number(message.usage.cached_input_tokens ?? 0);
    }
    if (total === 0 && input === 0 && output === 0) return null;
    return {
      input,
      output,
      cached,
      total: total || input + output,
      hitRate: input > 0 ? cached / input : null,
    };
  }, [messages]);

  /** 发送失败恢复草稿：仅当错误来自预检阶段（没有新的用户消息落库）——
   * 轮内模型错误时用户消息已持久化，恢复会造成内容双份。 */
  useEffect(() => {
    if (!live.error) {
      if (!live.running) lastSentRef.current = null;
      return;
    }
    const last = lastSentRef.current;
    if (!last) return;
    const lastUserSeq = messages.reduce(
      (seq, message) => (message.role === "user" ? message.seq : seq),
      0,
    );
    if (lastUserSeq === last.priorLastUserSeq) {
      setText(last.content);
      setDocuments(last.documents);
    }
    lastSentRef.current = null;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live.error, live.running]);

  const save = async (patch: { settings?: Partial<AgentSettings> }) => {
    setSaving(true);
    try {
      await onSettingsChange(patch);
    } finally {
      setSaving(false);
    }
  };

  const handleModelSelect = (source: ModelSource, model: Model) => {
    void save({
      settings: { modelSourceId: source.id, modelName: model.name },
    });
  };

  const submit = () => {
    const content = text.trim();
    if ((!content && documents.length === 0) || busy) return;
    // 记录发送前最后一条用户消息 seq：错误回来时若没有新的用户消息落库，
    // 说明是预检失败（400 超限/409 占用），恢复草稿避免用户重打全稿。
    const priorLastUserSeq = messages.reduce(
      (seq, message) => (message.role === "user" ? message.seq : seq),
      0,
    );
    lastSentRef.current = { content, documents, priorLastUserSeq };
    onSend({ content, documents });
    setText("");
    setDocuments([]);
  };

  const messageActions = useMemo(
    () => ({
      onRetry: (message: AgentMessage) => {
        if (busy) return;
        const content = message.content as {
          text?: string;
          documents?: AgentDocument[];
        };
        onSend({
          content: content.text ?? "",
          documents: content.documents ?? [],
          afterSeq: message.seq - 1,
        });
      },
      onEditResend: (message: AgentMessage) => {
        if (busy) return;
        const content = message.content as { text?: string };
        setEditing({ seq: message.seq, text: content.text ?? "" });
      },
      onRegenerate: (message: AgentMessage) => {
        if (busy) return;
        // 截断到该助手消息之前，从上一条用户消息重新生成。
        onSend({ afterSeq: message.seq - 1 });
      },
    }),
    [busy, onSend],
  );

  // 与 live.error 同文案的最后一条系统错误消息 seq（渲染抑制用，见消息流注释）。
  let suppressedErrorSeq: number | undefined;
  if (live.error) {
    for (let i = messages.length - 1; i >= 0; i -= 1) {
      const message = messages[i];
      if (message.role !== "system") continue;
      const content = message.content as {
        kind?: string;
        text?: string;
      } | null;
      if (content?.kind === "error" && content.text === live.error.text) {
        suppressedErrorSeq = message.seq;
        break;
      }
    }
  }

  return (
    <div
      className="mx-auto flex min-h-0 w-4/5 min-w-0 flex-col"
      onDragOver={(event) => {
        event.preventDefault();
        setDragOver(true);
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(event) => {
        event.preventDefault();
        setDragOver(false);
        if (event.dataTransfer.files.length > 0)
          void addFiles(event.dataTransfer.files);
      }}
    >
      {/* 扁平消息流：直接浮在页面背景上。 */}
      <div
        ref={scrollRef}
        className={cn(
          "relative min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-5",
          dragOver && "bg-wash/40",
        )}
      >
        {dragOver ? (
          <div className="pointer-events-none absolute inset-3 z-10 flex items-center justify-center rounded-xl border-2 border-dashed border-rose/40 text-sm text-muted-foreground">
            松开以添加附件（文档 / 图片 / PDF）
          </div>
        ) : null}
        {/* 报错去重：引擎对失败既落库系统错误消息又发 error 事件，turn_done
            刷新会把落库红卡拉回列表与 live 横幅同文案并排。live 横幅（带重试）
            存在时跳过最后一条同文案的落库红卡；关闭横幅或刷新后红卡自然回归。 */}
        {messages.length === 0 && !live.running ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
            <p className="text-sm text-foreground">
              描述一个任务，助手会调用工具完成
            </p>
            <div className="flex flex-wrap justify-center gap-2">
              {[
                "帮我设计一个 OpenAI 兼容协议",
                "汇总今天的用量趋势",
                "检查出站策略是否放行了内网",
              ].map((example) => (
                <button
                  key={example}
                  type="button"
                  className="rounded-full border border-border px-3 py-1 text-2xs text-muted-foreground transition-colors hover:bg-wash hover:text-foreground"
                  onClick={() => setText(example)}
                >
                  {example}
                </button>
              ))}
            </div>
          </div>
        ) : null}
        {messages.map((message) => {
          if (
            live.error &&
            message.role === "system" &&
            message.seq === suppressedErrorSeq
          ) {
            return null;
          }
          return (
            <div key={message.seq} data-seq={message.seq}>
              <MessageCard
                message={message}
                actions={messageActions}
                onOpenActivity={() => onOpenContextTab("activity")}
              />
            </div>
          );
        })}
        {live.running && live.statusText ? (
          <div className="flex items-center gap-2 pl-1 text-2xs text-muted-foreground">
            <span className="dot dot-ok" />
            {live.statusText}
          </div>
        ) : null}
        {live.running ||
        live.text ||
        live.reasoning ||
        live.toolCards.length > 0 ? (
          <LiveAssistantView
            live={live}
            onOpenActivity={() => onOpenContextTab("activity")}
          />
        ) : null}
        {live.planNotice ? (
          <button
            type="button"
            className="self-start rounded-full border border-border px-3 py-1 text-2xs text-muted-foreground hover:bg-wash hover:text-foreground"
            onClick={() => onOpenContextTab("plan")}
          >
            方案已更新 · {live.planNotice.done}/{live.planNotice.total}
          </button>
        ) : null}
        {live.compaction ? (
          <div className="self-start rounded-full bg-muted/60 px-3 py-1 text-2xs text-muted-foreground">
            {live.compaction.kind === "summary"
              ? "上下文已自动压缩"
              : "较早的工具结果已压缩"}
            {live.compaction.summarized
              ? ` · 处理 ${live.compaction.summarized} 条`
              : ""}
          </div>
        ) : null}
        {approval?.kind === "question" && approval.question ? (
          <QuestionCard
            question={approval.question}
            busy={busy}
            onAnswer={(answer) => onApprove({ approved: true, answer })}
          />
        ) : null}
        {approval?.kind === "plan" ? (
          <PlanConfirmCard
            plan={approval.plan ?? []}
            busy={busy}
            onConfirm={() => onApprove({ approved: true })}
            onRevise={(note) => onApprove({ approved: false, note })}
          />
        ) : null}
        {approval &&
        approval.kind !== "question" &&
        approval.kind !== "plan" ? (
          <ApprovalCard
            approval={approval}
            busy={busy}
            onApprove={(extra) => onApprove({ approved: true, ...extra })}
            onDeny={(note) => onApprove({ approved: false, note })}
          />
        ) : null}
        {live.error ? (
          <div className="tone-ember flex items-center gap-2 rounded-lg border px-3 py-2 text-xs">
            <AlertTriangle className="h-4 w-4 shrink-0" />
            <span className="min-w-0 flex-1">{live.error.text}</span>
            <button
              type="button"
              aria-label="关闭错误提示"
              className="rounded p-0.5 text-ember/70 transition-colors hover:bg-[color-mix(in_srgb,var(--ember)_12%,transparent)] hover:text-ember"
              onClick={onDismissError}
            >
              <X className="h-3.5 w-3.5" />
            </button>
            {live.error.retryable && !busy ? (
              <Button
                size="sm"
                variant="ghost"
                className="h-6 gap-1 px-2 text-2xs"
                onClick={() => {
                  const lastUser = [...messages]
                    .reverse()
                    .find((message) => message.role === "user");
                  if (!lastUser) return;
                  messageActions.onRetry(lastUser);
                }}
              >
                重试
              </Button>
            ) : null}
          </div>
        ) : null}
        <div ref={bottomRef} />
        {showJumpBottom ? (
          <button
            type="button"
            className="absolute bottom-3 left-1/2 inline-flex -translate-x-1/2 items-center gap-1 rounded-full border border-border bg-card px-3 py-1 text-2xs text-muted-foreground shadow-sm hover:text-foreground"
            onClick={() => {
              stickToBottomRef.current = true;
              setShowJumpBottom(false);
              bottomRef.current?.scrollIntoView({
                behavior: "smooth",
                block: "end",
              });
            }}
          >
            <ArrowDown className="h-3 w-3" /> 回到底部
          </button>
        ) : null}
      </div>

      {/* ComposerDock：默认有线无底（边框常显、内部透明），hover / 聚焦时填充浮现。 */}
      <div className="px-4 pb-2">
        <div className="rounded-xl border border-border bg-transparent transition-colors duration-200 hover:bg-card focus-within:border-rose focus-within:bg-card focus-within:ring-[3px] focus-within:ring-wash">
          {editing ? (
            <div className="px-3 pb-2 pt-2.5">
              <div className="mb-1.5 flex items-center gap-2 text-2xs text-muted-foreground">
                编辑历史消息并在这里重发（之后的消息将被替换）
                <button
                  type="button"
                  className="ml-auto rounded p-0.5 hover:text-foreground"
                  onClick={() => setEditing(null)}
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
              <Textarea
                className="min-h-[60px] border-0 bg-transparent px-0 text-sm focus-visible:border-0 focus-visible:ring-0"
                value={editing.text}
                onChange={(event) =>
                  setEditing({ ...editing, text: event.target.value })
                }
              />
              <div className="flex justify-end">
                <Button
                  size="sm"
                  className="h-7"
                  disabled={busy || !editing.text.trim()}
                  onClick={() => {
                    onSend({
                      content: editing.text,
                      afterSeq: editing.seq - 1,
                    });
                    setEditing(null);
                  }}
                >
                  从这里重发
                </Button>
              </div>
            </div>
          ) : null}

          {documents.length > 0 ? (
            <div className="flex flex-wrap gap-1.5 px-3 py-2">
              {documents.map((doc, index) => (
                <span
                  key={index}
                  className="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-2xs"
                >
                  <FileText className="h-3 w-3 text-muted-foreground" />
                  {doc.name ?? `材料 ${index + 1}`}
                  <button
                    type="button"
                    aria-label={`移除附件 ${doc.name ?? index + 1}`}
                    className="rounded p-0.5 hover:text-ember"
                    onClick={() =>
                      setDocuments((current) =>
                        current.filter((_, i) => i !== index),
                      )
                    }
                  >
                    <X className="h-3 w-3" />
                  </button>
                </span>
              ))}
            </div>
          ) : null}

          <input
            ref={fileInputRef}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              if (event.target.files?.length) void addFiles(event.target.files);
              event.target.value = "";
            }}
          />
          <Textarea
            className="max-h-56 min-h-[44px] w-full resize-none border-0 bg-transparent px-3.5 py-2.5 text-sm focus-visible:border-0 focus-visible:ring-0"
            placeholder={needsModel ? "先在下方选择模型…" : "请描述您的任务"}
            value={text}
            disabled={busy}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
                event.preventDefault();
                submit();
              }
            }}
            onPaste={(event) => {
              const files = Array.from(event.clipboardData.files ?? []);
              if (files.length > 0) {
                event.preventDefault();
                void addFiles(files);
              }
            }}
          />

          {/* 底部控制条：左侧 附加/权限；右侧 模型/思考/上下文占用/发送。 */}
          <div className="flex flex-wrap items-center gap-2 px-2.5 py-2">
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7 shrink-0 rounded-full border border-input"
              title="添加附件（文档 / 图片 / PDF）"
              disabled={busy}
              onClick={() => fileInputRef.current?.click()}
            >
              <Plus className="h-4 w-4" />
            </Button>
            <PermissionMenu
              settings={settings}
              disabled={busy}
              onChange={(patch) => void save(patch)}
            />
            <span
              className={cn(
                "text-2xs text-muted-foreground transition-opacity",
                saving ? "opacity-100" : "opacity-0",
              )}
            >
              保存中…
            </span>
            <div className="ml-auto flex items-center gap-2">
              <ContextGauge usage={usageStat} contextLimit={contextLimit} />
              <ModelPicker
                sourceId={settings.modelSourceId}
                modelName={settings.modelName}
                disabled={busy}
                onSelect={handleModelSelect}
              />
              <ThinkingMenu
                settings={settings}
                disabled={busy}
                onChange={(patch) => void save(patch)}
              />
              {busy ? (
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 shrink-0 rounded-full text-muted-foreground transition-colors hover:bg-destructive hover:text-white"
                  title="停止本轮"
                  onClick={onStop}
                >
                  <Square className="h-3.5 w-3.5" />
                </Button>
              ) : (
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 shrink-0 rounded-full text-muted-foreground transition-colors hover:bg-primary hover:text-primary-foreground"
                  title={needsModel ? "请先选择模型" : "发送（Ctrl+Enter）"}
                  disabled={
                    needsModel || (!text.trim() && documents.length === 0)
                  }
                  onClick={submit}
                >
                  <ArrowUp className="h-4 w-4" />
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

interface SessionUsageStat {
  input: number;
  output: number;
  cached: number;
  total: number;
  hitRate: number | null;
}

/** 上下文占用指示器：环形进度 + 悬浮明细（会话累计 tokens / 缓存命中率）。 */
function ContextGauge({
  usage,
  contextLimit,
}: {
  usage: SessionUsageStat | null;
  contextLimit: number;
}) {
  const hasUsage = !!usage && usage.total > 0;
  const ratio =
    hasUsage && contextLimit > 0 ? Math.min(1, usage.total / contextLimit) : 0;
  const color =
    ratio >= 0.9
      ? "var(--ember)"
      : ratio >= 0.7
        ? "var(--amber)"
        : "var(--jade)";
  const radius = 6;
  const circumference = 2 * Math.PI * radius;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="会话用量与上下文占用"
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-wash"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden>
            <circle
              cx="8"
              cy="8"
              r={radius}
              fill="none"
              strokeWidth="2"
              className="stroke-border"
            />
            {hasUsage && contextLimit > 0 ? (
              <circle
                cx="8"
                cy="8"
                r={radius}
                fill="none"
                stroke={color}
                strokeWidth="2"
                strokeLinecap="round"
                strokeDasharray={circumference}
                strokeDashoffset={circumference * (1 - ratio)}
                transform="rotate(-90 8 8)"
              />
            ) : null}
          </svg>
        </button>
      </TooltipTrigger>
      <TooltipContent className="space-y-0.5 text-2xs">
        {hasUsage && usage ? (
          <>
            <p className="tnum">
              会话累计：↑{compactNumber(usage.input)} ↓
              {compactNumber(usage.output)} tokens
            </p>
            {usage.cached > 0 ? (
              <p className="tnum">
                缓存命中：{compactNumber(usage.cached)}
                {usage.hitRate != null
                  ? `（${formatHitRate(usage.hitRate)}）`
                  : ""}
              </p>
            ) : null}
            <p className="tnum">共 {compactNumber(usage.total)} tokens</p>
            {contextLimit > 0 ? (
              <p className="tnum text-muted-foreground">
                上下文占用 {formatHitRate(ratio)}（按模型 MaxTokens{" "}
                {compactNumber(contextLimit)} 估算）
              </p>
            ) : (
              <p className="text-muted-foreground">
                模型未设置 MaxTokens，无法估算上下文占用
              </p>
            )}
          </>
        ) : (
          <p>本会话暂无 token 消耗</p>
        )}
      </TooltipContent>
    </Tooltip>
  );
}
