import {
  ArrowLeft,
  Eraser,
  PanelRightClose,
  PanelRightOpen,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { TonePill } from "@/components/badges";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { useToast } from "@/components/ui/use-toast";
import { PageHeader } from "@/components/page-header";
import { POLL } from "@/lib/hooks";
import useSWR from "swr";
import {
  clearAgentMessages,
  createAgentSession,
  deleteAgentSession,
  getAgentSession,
  listAgentSessions,
  restoreAgentDraft,
  updateAgentSession,
} from "@/lib/agent/api";
import {
  AGENT_CONTEXT_TAB_ORDER,
  type AgentContextTab,
  type AgentDocument,
  type AgentMessage,
  type AgentSession,
  type AgentSettings,
  type AgentStreamEvent,
} from "@/lib/agent/types";
import { useAgentStream } from "@/lib/agent/use-agent-stream";
import {
  deleteComposerDraft,
  loadComposerDraft,
  saveComposerDraft,
  sessionsWithDrafts,
  type AgentComposerDraft,
} from "@/lib/agent/draft-store";
import { cn } from "@/lib/utils";
import { ChatPanel } from "./chat-panel";
import { ContextPanel } from "./context-panel";
import { SessionOverview } from "./session-overview";
import { TurnRail } from "./turn-rail";
import { useDraggablePanelWidth } from "./use-draggable-panel-width";

/**
 * AI 助手页：总览（会话卡片网格）⇄ 工作区（轮数条 | 聊天 | 标签页侧栏）。
 * 点击卡片或新建任务以过渡动画进入工作区；返回总览不中断进行中的轮次。
 * 侧栏宽度可拖拽调整并记忆（localStorage）。
 */
export function AgentPage() {
  const { toast } = useToast();
  const { confirm, dialog: confirmDialog } = useConfirm();
  const failToast = useCallback(
    (prefix: string) => (error: unknown) =>
      toast({ description: error instanceof Error ? error.message : prefix }),
    [toast],
  );
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [view, setView] = useState<"list" | "chat">("list");
  const [activeId, setActiveId] = useState<string | undefined>();
  const [session, setSession] = useState<AgentSession | undefined>();
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  /** 入口引导只跑一次（ref 而非 state：StrictMode 双挂载下 state 守卫会双双通过）。 */
  const bootstrapRef = useRef(false);
  const [panelOpen, setPanelOpen] = useState(true);
  const [tabs, setTabs] = useState<AgentContextTab[]>([]);
  const [activeTab, setActiveTab] = useState<AgentContextTab | null>(null);
  const [activeTurnSeq, setActiveTurnSeq] = useState<number | null>(null);
  const [jumpTarget, setJumpTarget] = useState<{
    seq: number;
    nonce: number;
  } | null>(null);
  const [composerDraft, setComposerDraft] = useState<{
    sessionId: string;
    draft: AgentComposerDraft;
  } | null>(null);
  /** 当前会话的草稿快照（仅归属匹配时才有值——防止上一会话的残留文本
   * 播种进新会话的输入框并随击键写进新会话的草稿）。 */
  const activeDraft =
    composerDraft && composerDraft.sessionId === session?.id
      ? composerDraft.draft
      : null;
  const [draftSessions, setDraftSessions] = useState<Set<string>>(new Set());
  /** 草稿读取已完成的会话：ChatPanel 等它就绪再挂载，避免先播空再补草稿。 */
  const [draftLoadedFor, setDraftLoadedFor] = useState<string | null>(null);
  const {
    panelW,
    panelDragging,
    onPanelHandleDown,
    onPanelHandleMove,
    onPanelHandleUp,
  } = useDraggablePanelWidth();

  const { data: sessions, mutate: mutateSessions } = useSWRSessionList();

  /** 总览打开时看哪些会话留有未发送内容（文本或附件）。 */
  useEffect(() => {
    if (view !== "list" || !sessions) return;
    let cancelled = false;
    void sessionsWithDrafts(sessions.map((item) => item.id)).then((found) => {
      if (!cancelled) setDraftSessions(found);
    });
    return () => {
      cancelled = true;
    };
  }, [view, sessions]);

  const activeIdRef = useRef(activeId);
  useEffect(() => {
    activeIdRef.current = activeId;
  }, [activeId]);

  const refreshSession = useCallback(async (id: string) => {
    try {
      const detail = await getAgentSession(id);
      // 快速 A→B 切换时 A 的慢响应不能覆盖已激活的 B。
      if (activeIdRef.current !== id) return;
      setSession(detail.session);
      setMessages(detail.messages ?? []);
    } catch {
      /* 会话可能已删除 */
    }
  }, []);

  const { live, send, approve, stop, dismissError, hydrateApproval } =
    useAgentStream(activeId, {
      onMessage: (event: AgentStreamEvent) => {
        if (event.message) {
          setMessages((current) => [...current, event.message as AgentMessage]);
          if (event.message.role === "user") {
            void mutateSessions();
          }
        }
      },
      onSessionDirty: () => {
        if (activeId) void refreshSession(activeId);
      },
    });

  /** 审批卡回灌：waiting_approval 的轮次在刷新/切会话后 SSE 现场已丢，
   * 用会话详情里的 pendingAction 重建审批卡，否则待批轮次永远无法批准。
   * plan 型待批没有 calls，按 kind 放行。 */
  useEffect(() => {
    const pending = session?.pendingAction;
    const hydratable =
      session?.status === "waiting_approval" &&
      (pending?.kind === "plan" ||
        pending?.kind === "question" ||
        Boolean(pending?.calls?.length));
    if (hydratable && pending) {
      hydrateApproval(pending);
    }
  }, [session, hydrateApproval]);

  /** 入口跳转：?mode=create | ?mode=edit&protocol=<id> 自动建会话并进入工作区。 */
  useEffect(() => {
    if (bootstrapRef.current) return;
    bootstrapRef.current = true;
    const mode = searchParams.get("mode");
    if (mode === "create" || mode === "edit") {
      const protocolId = searchParams.get("protocol") ?? "";
      if (mode === "edit" && !protocolId) return;
      void createSession({ mode, protocolId: protocolId || undefined });
      navigate("/agent", { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** 会话列表轻轮询：运行中状态可感知（断连后回来能看到轮次结束）。 */
  useEffect(() => {
    if (!live.running) return;
    const timer = window.setInterval(
      () => void mutateSessions(),
      POLL.AGENT_SESSION_FAST,
    );
    return () => window.clearInterval(timer);
  }, [live.running, mutateSessions]);

  /** 切换会话：标签页与轮次定位状态归零。 */
  useEffect(() => {
    setTabs([]);
    setActiveTab(null);
    setActiveTurnSeq(null);
    setJumpTarget(null);
  }, [activeId]);

  /** 打开（必要时追加）并激活一个侧栏标签；reveal 为 true 时同时展开侧栏。 */
  const openContextTab = useCallback((tab: AgentContextTab, reveal = true) => {
    setTabs((current) =>
      current.includes(tab)
        ? current
        : [...current, tab].sort(
            (a, b) =>
              AGENT_CONTEXT_TAB_ORDER.indexOf(a) -
              AGENT_CONTEXT_TAB_ORDER.indexOf(b),
          ),
    );
    setActiveTab(tab);
    if (reveal) setPanelOpen(true);
  }, []);

  const closeContextTab = useCallback(
    (tab: AgentContextTab) => {
      const next = tabs.filter((item) => item !== tab);
      setTabs(next);
      setActiveTab((active) =>
        active === tab ? (next[next.length - 1] ?? null) : active,
      );
    },
    [tabs],
  );

  const togglePanel = useCallback(() => {
    setPanelOpen((value) => !value);
    if (!panelOpen && activeTab == null && tabs.length > 0) {
      setActiveTab(tabs[tabs.length - 1] ?? null);
    }
  }, [activeTab, panelOpen, tabs]);

  const createSession = useCallback(
    async (input?: { mode: "create" | "edit"; protocolId?: string }) => {
      try {
        const created = await createAgentSession(input ?? { mode: "create" });
        await mutateSessions();
        setActiveId(created.id);
        setSession(created);
        setMessages([]);
        setView("chat");
      } catch (error) {
        failToast("创建会话失败")(error);
      }
    },
    [failToast, mutateSessions],
  );

  /** 未发送内容（文本 + 附件）随会话进出：每次进入 chat 视图都重读——
   * 只按 activeId 加载的话，重进同一会话会播种首次进入时的旧快照。 */
  useEffect(() => {
    if (view !== "chat" || !activeId) return;
    let cancelled = false;
    void loadComposerDraft(activeId).then((draft) => {
      if (!cancelled) {
        setComposerDraft(draft ? { sessionId: activeId, draft } : null);
        setDraftLoadedFor(activeId);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [view, activeId]);

  const handleDraftChange = useCallback(
    (draft: AgentComposerDraft) => {
      if (!activeId) return;
      void saveComposerDraft(activeId, draft);
      setDraftSessions((current) => {
        const next = new Set(current);
        if (draft.text.trim() || draft.documents.length > 0) next.add(activeId);
        else next.delete(activeId);
        return next;
      });
    },
    [activeId],
  );

  /** 总览卡片 → 进入工作区。 */
  const handleOpen = useCallback(
    (id: string) => {
      setView("chat");
      if (id === activeId) return;
      setActiveId(id);
      void refreshSession(id);
    },
    [activeId, refreshSession],
  );

  const handleDelete = useCallback(
    async (id: string) => {
      const target = (sessions ?? []).find((item) => item.id === id);
      const ok = await confirm({
        title: `删除会话「${target?.title || "未命名"}」？`,
        description: "消息历史与方案进度将一并删除，无法恢复。",
        confirmText: "删除",
      });
      if (!ok) return;
      try {
        await deleteAgentSession(id);
        void deleteComposerDraft(id);
        if (id === activeId) {
          setActiveId(undefined);
          setSession(undefined);
          setMessages([]);
        }
        await mutateSessions();
      } catch (error) {
        failToast("删除失败")(error);
      }
    },
    [activeId, confirm, failToast, mutateSessions, sessions],
  );

  const handleSettingsChange = useCallback(
    async (patch: {
      settings?: Partial<AgentSettings>;
      title?: string;
    }): Promise<boolean> => {
      if (!session) return false;
      try {
        const updated = await updateAgentSession(session.id, patch);
        setSession((current) =>
          current ? { ...current, settings: updated.settings } : updated,
        );
        await mutateSessions();
        return true;
      } catch (error) {
        toast({
          description: error instanceof Error ? error.message : "保存设置失败",
        });
        return false;
      }
    },
    [mutateSessions, session, toast],
  );

  /** 计划模式：确认执行 → 关闭计划模式并以用户消息通知助手开始执行。
   * ref 守卫挡住双击：live.running 在 PATCH 返回后才置位，期间第二次点击
   * 会重启流（重置现场）然后吃一个假 409。 */
  const confirmingPlanRef = useRef(false);
  const handleConfirmPlan = useCallback(async () => {
    if (!session || live.running || confirmingPlanRef.current) return;
    confirmingPlanRef.current = true;
    try {
      const ok = await handleSettingsChange({ settings: { planMode: false } });
      if (!ok) return; // PATCH 失败已提示；计划模式仍开启，直接发送只会被门控拒绝
      send({ content: "确认执行当前方案，请开始执行。" });
    } finally {
      confirmingPlanRef.current = false;
    }
  }, [handleSettingsChange, live.running, send, session]);

  /** 草稿还原：回滚到最近一轮修改前的还原点。 */
  const handleRestoreDraft = useCallback(async () => {
    if (!session || live.running) return;
    const ok = await confirm({
      title: "还原到上一轮修改前？",
      description:
        "配置草稿将回滚到最近一轮对话修改前的状态，对话记录不受影响。",
      confirmText: "还原",
    });
    if (!ok) return;
    try {
      const updated = await restoreAgentDraft(session.id);
      setSession(updated);
      toast({ description: "已还原到上一轮修改前的配置" });
    } catch (error) {
      failToast("还原失败")(error);
    }
  }, [confirm, failToast, live.running, session, toast]);

  const handleClearHistory = useCallback(async () => {
    if (!session) return;
    try {
      await clearAgentMessages(session.id, 0);
      setMessages([]);
      await refreshSession(session.id);
      toast({ description: "已清空会话消息" });
    } catch (error) {
      failToast("清空失败")(error);
    }
  }, [failToast, refreshSession, session, toast]);

  const handleStop = useCallback(() => {
    void stop();
  }, [stop]);

  const handleApprove = useCallback(
    (decision: { approved: boolean }) => {
      approve(decision);
      // 批准后刷新列表让状态圆点进入 running。
      window.setTimeout(() => void mutateSessions(), 300);
    },
    [approve, mutateSessions],
  );

  const handleJumpTurn = useCallback((seq: number) => {
    setJumpTarget({ seq, nonce: Date.now() });
  }, []);

  const handleActiveTurn = useCallback((seq: number | null) => {
    setActiveTurnSeq(seq);
  }, []);

  const statusBadge = useMemo(() => {
    if (live.running) return { text: "进行中", color: "var(--jade)" };
    if (session?.status === "waiting_approval" && live.approvalPending) {
      return { text: "待审批", color: "var(--amber)" };
    }
    if (session?.settings.planMode)
      return { text: "计划模式", color: "var(--amber)" };
    return null;
  }, [
    live.approvalPending,
    live.running,
    session?.settings.planMode,
    session?.status,
  ]);

  if (view === "list") {
    return (
      <div className="-mb-14 flex h-[max(560px,calc(100dvh-46px))] min-h-0">
        <div
          key="agent-overview"
          className="flex min-h-0 flex-1 animate-in fade-in duration-300 flex-col"
        >
          <PageHeader
            title="AI 助手"
            titleContent={
              <>
                <span className="font-sans font-medium">AI</span> 助手
              </>
            }
          />
          <SessionOverview
            sessions={sessions ?? []}
            draftSessions={draftSessions}
            onOpen={handleOpen}
            onCreate={() => void createSession()}
            onDelete={(id) => void handleDelete(id)}
          />
        </div>
        {confirmDialog}
      </div>
    );
  }

  return (
    <div className="-mb-14 flex h-[max(560px,calc(100dvh-46px))] min-h-0">
      <div
        key={`agent-chat-${activeId ?? "none"}`}
        className="flex min-h-0 flex-1 animate-in fade-in slide-in-from-bottom-2 duration-300 flex-col"
      >
        <div className="flex items-center gap-2 px-4 pb-2 pt-1">
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            title="返回会话总览"
            onClick={() => setView("list")}
          >
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <p className="min-w-0 flex-1 truncate text-sm font-semibold">
            {session?.title || "AI 助手"}
            {session?.mode === "edit" ? (
              <span className="ml-2 font-normal text-muted-foreground">
                编辑协议 {session.protocolId ?? ""}
              </span>
            ) : null}
          </p>
          {statusBadge ? (
            <TonePill color={statusBadge.color} className="text-2xs">
              {statusBadge.text}
            </TonePill>
          ) : null}
          {session ? (
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              title="清空本会话消息（保留草稿与设置）"
              disabled={live.running || messages.length === 0}
              onClick={() => void handleClearHistory()}
            >
              <Eraser className="h-4 w-4" />
            </Button>
          ) : null}
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            title={panelOpen ? "收起侧栏" : "展开侧栏"}
            onClick={togglePanel}
          >
            {panelOpen ? (
              <PanelRightClose className="h-4 w-4" />
            ) : (
              <PanelRightOpen className="h-4 w-4" />
            )}
          </Button>
        </div>

        {session && draftLoadedFor === session.id ? (
          <div className="flex min-h-0 flex-1">
            <TurnRail
              messages={messages}
              live={live}
              activeSeq={activeTurnSeq}
              onJump={handleJumpTurn}
            />
            <ChatPanel
              key={session.id}
              session={session}
              initialDraft={activeDraft}
              onDraftChange={handleDraftChange}
              messages={messages}
              live={live}
              onSettingsChange={handleSettingsChange}
              onDismissError={dismissError}
              onSend={(input: {
                content?: string;
                documents?: AgentDocument[];
                afterSeq?: number;
              }) => {
                if (input.afterSeq != null) {
                  setMessages((current) =>
                    current.filter((message) => message.seq <= input.afterSeq!),
                  );
                }
                send(input);
              }}
              onApprove={handleApprove}
              onStop={handleStop}
              onOpenContextTab={(tab) => openContextTab(tab, true)}
              jumpTarget={jumpTarget}
              onActiveTurn={handleActiveTurn}
            />
            <div
              className={cn(
                "flex h-full shrink-0 overflow-hidden",
                !panelDragging && "transition-[width] duration-300 ease-in-out",
              )}
              style={{ width: panelOpen ? panelW : 0 }}
            >
              <div
                role="separator"
                aria-orientation="vertical"
                aria-label="拖拽调整侧栏宽度"
                onPointerDown={(event) => onPanelHandleDown(event, panelOpen)}
                onPointerMove={onPanelHandleMove}
                onPointerUp={onPanelHandleUp}
                onPointerCancel={onPanelHandleUp}
                className={cn(
                  "group relative w-1 shrink-0 cursor-col-resize touch-none select-none",
                  !panelOpen && "pointer-events-none",
                )}
              >
                <span
                  className={cn(
                    "absolute inset-y-2 left-1/2 w-px -translate-x-1/2 bg-border transition-colors group-hover:bg-rose/50",
                    panelDragging && "bg-rose/50",
                  )}
                />
              </div>
              <div
                className={cn(
                  "h-full min-w-0 flex-1 transition-opacity duration-300",
                  panelOpen ? "opacity-100" : "pointer-events-none opacity-0",
                )}
              >
                <ContextPanel
                  key={session.id}
                  session={session}
                  messages={messages}
                  live={live}
                  tabs={tabs}
                  activeTab={activeTab}
                  onTabSelect={setActiveTab}
                  onTabClose={closeContextTab}
                  onAutoOpen={(tab) => openContextTab(tab, false)}
                  onConfirmPlan={() => void handleConfirmPlan()}
                  onRestoreDraft={() => void handleRestoreDraft()}
                />
              </div>
            </div>
          </div>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            正在加载会话…
          </div>
        )}
      </div>
      {confirmDialog}
    </div>
  );
}

/** 会话列表 SWR（本页专用）。 */
function useSWRSessionList() {
  return useSWR("agent-sessions", () => listAgentSessions(), {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
    dedupingInterval: 2000,
    refreshInterval: POLL.USAGE,
  });
}
