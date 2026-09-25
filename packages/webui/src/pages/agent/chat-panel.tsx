import { AlertTriangle, ArrowDown, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
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
import { cn } from "@/lib/utils";
import { useComposerAttachments } from "./use-composer-attachments";
import { MessageCard } from "./message-card";
import { LiveAssistantView } from "./live-view";
import { ApprovalCard, PlanConfirmCard, QuestionCard } from "./approval-cards";
import { useChatScroll } from "./use-chat-scroll";
import { ComposerDock } from "./composer-dock";

/** 本文件的时限与阈值锚点。 */
const DRAFT_DEBOUNCE_MS = 300; // 草稿回写防抖（附件可达数 MB）

export interface ChatPanelProps {
  session: AgentSession;
  /** 本会话未发送的文本与附件（本机缓存，进入时回填）。 */
  initialDraft?: { text: string; documents: AgentDocument[] } | null;
  /** 文本或附件变化时回写本机缓存。 */
  onDraftChange?: (draft: { text: string; documents: AgentDocument[] }) => void;
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
 */
export function ChatPanel({
  session,
  initialDraft,
  onDraftChange,
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
  const [text, setText] = useState(initialDraft?.text ?? "");
  const notify = (description: string) => toast({ description });
  const { documents, setDocuments, addFiles } = useComposerAttachments(
    notify,
    initialDraft?.documents,
  );
  const [dragOver, setDragOver] = useState(false);
  const [editingMessage, setEditingMessage] = useState<{
    seq: number;
    text: string;
  } | null>(null);
  const [saving, setSaving] = useState(false);
  // 编辑重发期间的草稿附件备份（取消编辑时恢复）。
  const editDraftBackupRef = useRef<AgentDocument[] | null>(null);
  const lastSentRef = useRef<{
    content: string;
    documents: AgentDocument[];
    priorLastUserSeq: number;
  } | null>(null);

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

  // 未发送内容回写本机缓存：300ms 尾随防抖（附件可达数 MB，逐键全量
  // structured clone + 写盘太重）。首次渲染跳过：那时的值就是刚读出的草稿。
  const draftReady = useRef(false);
  const draftTimer = useRef<number | null>(null);
  const draftLatest = useRef({ text, documents });
  draftLatest.current = { text, documents };
  const clearDraftTimer = () => {
    if (draftTimer.current != null) {
      window.clearTimeout(draftTimer.current);
      draftTimer.current = null;
    }
  };
  useEffect(() => {
    if (!draftReady.current) {
      draftReady.current = true;
      return;
    }
    clearDraftTimer();
    draftTimer.current = window.setTimeout(() => {
      draftTimer.current = null;
      onDraftChange?.(draftLatest.current);
    }, DRAFT_DEBOUNCE_MS);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text, documents]);
  // 卸载时把挂起的最后一次变更落盘。
  useEffect(
    () => () => {
      if (draftTimer.current != null) onDraftChange?.(draftLatest.current);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const {
    scrollRef,
    bottomRef,
    stickToBottomRef,
    showJumpBottom,
    setShowJumpBottom,
    jumpToBottom,
  } = useChatScroll({ messages, live, jumpTarget, onActiveTurn });

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

  const handleSettingsSave = async (patch: {
    settings?: Partial<AgentSettings>;
  }) => {
    setSaving(true);
    try {
      await onSettingsChange(patch);
    } finally {
      setSaving(false);
    }
  };

  const handleModelSelect = (source: ModelSource, model: Model) => {
    void handleSettingsSave({
      settings: { modelSourceId: source.id, modelName: model.name },
    });
  };

  const handleSubmit = () => {
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
    // 发送成功：取消挂起的防抖写并立即清空缓存（总览徽标即时消失）。
    clearDraftTimer();
    onDraftChange?.({ text: "", documents: [] });
    // 发送即回到跟随模式：新一轮输出应该跟着滚，否则用户上翻后发出的消息
    // 不会自动滚入视野。
    stickToBottomRef.current = true;
    setShowJumpBottom(false);
  };

  // 取消编辑：恢复进入编辑前暂存的草稿附件。
  const handleEditMessageChange = (next: {
    seq: number;
    text: string;
  } | null) => {
    if (next == null && editDraftBackupRef.current != null) {
      setDocuments(editDraftBackupRef.current);
      editDraftBackupRef.current = null;
    }
    setEditingMessage(next);
  };

  // 编辑重发：附件随消息一起重发（原消息附件已在进入编辑时灌入 composer）。
  const handleEditSend = (payload: {
    content?: string;
    documents?: AgentDocument[];
    afterSeq?: number;
  }) => {
    onSend({ ...payload, documents });
    editDraftBackupRef.current = null;
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
        const content = message.content as {
          text?: string;
          documents?: AgentDocument[];
        };
        // 暂存当前草稿附件：编辑态把原消息的附件灌进 composer 供增删，
        // 取消编辑时恢复，避免静默丢掉未发送的草稿。
        editDraftBackupRef.current = documents;
        setDocuments(content.documents ?? []);
        setEditingMessage({ seq: message.seq, text: content.text ?? "" });
      },
      onRegenerate: (message: AgentMessage) => {
        if (busy) return;
        // 截断到该助手消息之前，从上一条用户消息重新生成。
        onSend({ afterSeq: message.seq - 1 });
      },
    }),
    [busy, onSend, documents, setDocuments],
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
      {/* 扁平消息流：直接浮在页面背景上。跳底浮标是滚动容器的兄弟节点——
          放在滚动内容里会随内容滚走（absolute 的包含块是滚动区的内容坐标）。 */}
      <div className="relative flex min-h-0 flex-1">
        <div
          ref={scrollRef}
          className={cn(
            "no-scrollbar relative min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-5",
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
                <MessageCard message={message} actions={messageActions} />
              </div>
            );
          })}
          {live.running && live.statusText ? (
            <div role="status" className="flex items-center gap-2 pl-1 text-2xs text-muted-foreground">
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
          {live.error ? (
            <div role="alert" className="tone-ember flex items-center gap-2 rounded-lg border px-3 py-2 text-xs">
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
        </div>
        {showJumpBottom ? (
          <button
            type="button"
            className="absolute bottom-3 left-1/2 inline-flex -translate-x-1/2 items-center gap-1 rounded-full border border-border bg-card px-3 py-1 text-2xs text-muted-foreground shadow-sm hover:text-foreground"
            onClick={jumpToBottom}
          >
            <ArrowDown className="h-3 w-3" /> 回到底部
          </button>
        ) : null}
      </div>

      {/* 待审批/提问/方案确认时输入框整块让位给确认卡——交互发生在输入位置。 */}
      <div className="px-4 pb-2">
        {approval ? (
          approval.kind === "question" && approval.question ? (
            <QuestionCard
              question={approval.question}
              busy={busy}
              onAnswer={(answer) => onApprove({ approved: true, answer })}
            />
          ) : approval.kind === "plan" ? (
            <PlanConfirmCard
              plan={approval.plan ?? []}
              summary={approval.planSummary}
              busy={busy}
              onConfirm={() => onApprove({ approved: true })}
              onRevise={(note) => onApprove({ approved: false, note })}
            />
          ) : (
            <ApprovalCard
              approval={approval}
              busy={busy}
              onApprove={(extra) => onApprove({ approved: true, ...extra })}
              onDeny={(note) => onApprove({ approved: false, note })}
            />
          )
        ) : (
          <ComposerDock
            text={text}
            onTextChange={setText}
            documents={documents}
            setDocuments={setDocuments}
            addFiles={addFiles}
            busy={busy}
            needsModel={needsModel}
            saving={saving}
            settings={settings}
            usage={usageStat}
            contextLimit={contextLimit}
            editingMessage={editingMessage}
            onEditMessageChange={handleEditMessageChange}
            onSubmit={handleSubmit}
            onStop={onStop}
            onSend={handleEditSend}
            onSettingsSave={handleSettingsSave}
            onModelSelect={handleModelSelect}
          />
        )}
      </div>
    </div>
  );
}
