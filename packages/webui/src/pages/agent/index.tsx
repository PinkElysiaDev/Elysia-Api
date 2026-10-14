import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { useToast } from "@/components/ui/use-toast";
import { PageHeader } from "@/components/page-header";
import { POLL } from "@/lib/hooks";
import useSWR from "swr";
import {
  clearAgentMessages,
  createAgentSession,
  deleteAgentSession,
  listAgentSessions,
  restoreAgentDraft,
} from "@/lib/agent/api";
import { deleteComposerDraft } from "@/lib/agent/draft-store";
import {
  type AgentContextTab,
  type AgentDocument,
  type AgentSettings,
  type AgentStreamEvent,
} from "@/lib/agent/types";
import { useAgentStream } from "@/lib/agent/use-agent-stream";
import { ChatPanel } from "./chat-panel";
import { ContextPanel } from "./context-panel";
import { SessionOverview } from "./session-overview";
import { TurnRail } from "./turn-rail";
import { WorkspaceHeader } from "./workspace-header";
import { useAgentSession } from "./use-agent-session";
import { useAgentDrafts } from "./use-agent-drafts";

/**
 * AI 助手页：总览（会话卡片网格）⇄ 工作区（轮数条 | 聊天 | 按需打开的任务资料）。
 * 点击卡片或新建任务以过渡动画进入工作区；返回总览不中断进行中的轮次。
 * 侧栏宽度可拖拽调整并记忆（localStorage）。
 */
/** 两个视图共用的满高工作区布局（抵消页面容器的下内边距）。 */
const WORKSPACE_CLASS = "-mb-14 flex h-[max(560px,calc(100dvh-46px))] min-h-0";

const CONTEXT_PANEL_UNMOUNT_DELAY_MS = 320;
const APPROVAL_LIST_REFRESH_DELAY_MS = 300;
const SESSION_LIST_DEDUPLICATION_MS = 2000;

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
  /** 入口引导只跑一次（ref 而非 state：StrictMode 双挂载下 state 守卫会双双通过）。 */
  const bootstrapRef = useRef(false);
  const [activeTab, setActiveTab] = useState<AgentContextTab | null>(null);
  const [activeTurnSeq, setActiveTurnSeq] = useState<number | null>(null);
  const [jumpTarget, setJumpTarget] = useState<{
    seq: number;
    nonce: number;
  } | null>(null);

  const { data: sessions, mutate: mutateSessions } = useSWRSessionList();
  const onSessionError = useCallback((error: unknown) => {
    failToast("保存会话失败")(error);
  }, [failToast]);
  const {
    activeId, session, setSession, messages, setMessages, sessionError,
    savingSessions, isSavingSettings, selectSession, refreshSession, saveSession,
    isCurrentSelection, invalidateDetailRequests, captureSelection,
    invalidateSelection, isActiveSession, hasPendingUpdate,
  } = useAgentSession(mutateSessions, onSessionError);

  const { composerDraft, draftSessions, draftLoadedFor, handleDraftChange } =
    useAgentDrafts(view, activeId, sessions);
  /** 当前会话的草稿快照（仅归属匹配时才有值——防止上一会话的残留文本
   * 播种进新会话的输入框并随击键写进新会话的草稿）。 */
  const activeDraft =
    composerDraft && composerDraft.sessionId === session?.id
      ? composerDraft.draft
      : null;

  const { live, send, approve, stop, dismissError, dismissResult, hydrateApproval } =
    useAgentStream(activeId, {
      onMessage: (event: AgentStreamEvent) => {
        if (event.message) {
          // A stream event is newer than any detail snapshot already in flight.
          invalidateDetailRequests();
          const message = event.message;
          setMessages((current) => current.some((item) => item.seq === message.seq)
            ? current.map((item) => item.seq === message.seq ? message : item)
            : [...current, message]);
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
    hydrateApproval(hydratable ? pending : null);
  }, [session, hydrateApproval]);

  useEffect(() => {
    // A polling request started before local SSE cannot replace its new messages.
    if (live.running) invalidateDetailRequests();
  }, [live.running, invalidateDetailRequests]);

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

  /** 外部更新轮询：MCP/A2A/插件等远程面驱动的轮次不经过本页的 SSE，
   *  消息与待批动作只落库不推送——打开中的会话定时拉取详情感知它们
   *  （refreshSession 会顺带触发审批卡回灌）。自己的轮次走 SSE，跳过
   *  轮询以免全量替换与流式 append 竞争；远程在跑（会话 running）时
   *  加速到快档。 */
  useEffect(() => {
    if (!activeId || live.running || isSavingSettings) return;
    const interval =
      session?.status === "running"
        ? POLL.AGENT_SESSION_FAST
        : POLL.AGENT_SESSION_IDLE;
    const timer = window.setInterval(() => {
      void refreshSession(activeId);
    }, interval);
    return () => window.clearInterval(timer);
  }, [activeId, live.running, isSavingSettings, session?.status, refreshSession]);

  /** 切换会话：收起任务资料，重置轮次定位。 */
  useEffect(() => {
    setActiveTab(null);
    setActiveTurnSeq(null);
    setJumpTarget(null);
  }, [activeId]);

  const availablePanels: AgentContextTab[] = [];
  if (session?.plan?.length || session?.planSummary?.trim())
    availablePanels.push("plan");
  if (session?.draftConfig != null && session.draftConfig !== "")
    availablePanels.push("draft");
  const visiblePanel =
    activeTab && availablePanels.includes(activeTab) ? activeTab : null;
  // 面板收拢过渡:关闭时保持挂载传 open=false,由面板播放宽度收拢,过渡
  // 结束再卸载(320ms = 面板 300ms 过渡 + 余量)——避免"点收起瞬间消失"。
  const [lastPanel, setLastPanel] = useState<AgentContextTab | null>(null);
  const [panelMounted, setPanelMounted] = useState(visiblePanel != null);
  useEffect(() => {
    if (visiblePanel != null) {
      setLastPanel(visiblePanel);
      setPanelMounted(true);
      return;
    }
    if (!panelMounted) return;
    const timer = setTimeout(() => setPanelMounted(false), CONTEXT_PANEL_UNMOUNT_DELAY_MS);
    return () => clearTimeout(timer);
  }, [visiblePanel, panelMounted]);

  const closeContextPanel = useCallback(() => {
    setActiveTab(null);
    document.getElementById(`agent-context-trigger-${activeTab}`)?.focus();
  }, [activeTab]);

  const createSession = useCallback(
    async (input?: { mode: "create" | "edit"; protocolId?: string }) => {
      const generation = invalidateSelection();
      try {
        const created = await createAgentSession(input ?? { mode: "create" });
        void mutateSessions();
        if (captureSelection() !== generation) return;
        selectSession(created.id);
        setSession(created);
        setMessages([]);
        setView("chat");
      } catch (error) {
        failToast("创建会话失败")(error);
      }
    },
    [failToast, mutateSessions, selectSession, captureSelection, invalidateSelection, setSession, setMessages],
  );

  /** 总览卡片 → 进入工作区。 */
  const handleOpen = useCallback(
    (id: string) => {
      setView("chat");
      selectSession(id);
      if (id !== activeId) {
        setSession(undefined);
        setMessages([]);
      }
      void refreshSession(id);
    },
    [activeId, refreshSession, selectSession, setSession, setMessages],
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
        if (isActiveSession(id)) {
          selectSession(undefined);
          setSession(undefined);
          setMessages([]);
        }
        await mutateSessions();
      } catch (error) {
        failToast("删除失败")(error);
      }
    },
    [confirm, failToast, mutateSessions, sessions, selectSession, isActiveSession, setSession, setMessages],
  );

  const handleRenameSession = useCallback(
    (id: string, title: string) => saveSession(id, { title }),
    [saveSession],
  );

  const handleSettingsChange = useCallback(
    async (patch: {
      settings?: Partial<AgentSettings>;
      title?: string;
    }): Promise<boolean> => {
      if (!session) return false;
      return saveSession(session.id, patch);
    },
    [saveSession, session],
  );

  /** 计划模式：确认执行 → 关闭计划模式并以用户消息通知助手开始执行。
   * ref 守卫挡住双击：live.running 在 PATCH 返回后才置位，期间第二次点击
   * 会重启流（重置现场）然后吃一个假 409。 */
  const confirmingPlanRef = useRef(false);
  const handleConfirmPlan = useCallback(async () => {
    if (!session || live.running || hasPendingUpdate(session.id) || confirmingPlanRef.current) return;
    const generation = captureSelection();
    confirmingPlanRef.current = true;
    try {
      const ok = await handleSettingsChange({ settings: { planMode: false } });
      if (!ok || !isCurrentSelection(session.id, generation)) return;
      send({ content: "确认执行当前方案，请开始执行。" });
    } finally {
      confirmingPlanRef.current = false;
    }
  }, [handleSettingsChange, isCurrentSelection, live.running, send, session, hasPendingUpdate, captureSelection]);

  /** 草稿还原：回滚到最近一轮修改前的还原点。 */
  const handleRestoreDraft = useCallback(async () => {
    if (!session || live.running) return;
    const generation = captureSelection();
    const ok = await confirm({
      title: "还原到上一轮修改前？",
      description:
        "配置草稿将回滚到最近一轮对话修改前的状态，对话记录不受影响。",
      confirmText: "还原",
    });
    if (!ok) return;
    try {
      await restoreAgentDraft(session.id);
      if (!isCurrentSelection(session.id, generation)) return;
      await refreshSession(session.id);
      if (isCurrentSelection(session.id, generation)) {
        toast({ description: "已还原到上一轮修改前的配置" });
      }
    } catch (error) {
      failToast("还原失败")(error);
    }
  }, [confirm, failToast, isCurrentSelection, live.running, refreshSession, session, toast, captureSelection]);

  const handleClearHistory = useCallback(async () => {
    if (!session) return;
    const generation = captureSelection();
    try {
      await clearAgentMessages(session.id, 0);
      if (!isCurrentSelection(session.id, generation)) return;
      setMessages([]);
      await refreshSession(session.id);
      if (isCurrentSelection(session.id, generation)) {
        toast({ description: "已清空会话消息" });
      }
    } catch (error) {
      failToast("清空失败")(error);
    }
  }, [failToast, isCurrentSelection, refreshSession, session, toast, captureSelection, setMessages]);

  const handleStop = useCallback(() => {
    void stop();
  }, [stop]);

  const handleApprove = useCallback(
    (decision: { approved: boolean }) => {
      approve(decision);
      // 批准后刷新列表让状态圆点进入 running。
      window.setTimeout(() => void mutateSessions(), APPROVAL_LIST_REFRESH_DELAY_MS);
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
      <div className={WORKSPACE_CLASS}>
        <div key="agent-overview" className="flex min-h-0 flex-1 flex-col">
          <PageHeader title="AI 助手"  />
          <SessionOverview
            sessions={sessions ?? []}
            savingSessions={savingSessions}
            draftSessions={draftSessions}
            onOpen={handleOpen}
            onCreate={() => void createSession()}
            onDelete={(id) => void handleDelete(id)}
            onEditTitle={(id, title) => void handleRenameSession(id, title)}
          />
        </div>
        {confirmDialog}
      </div>
    );
  }

  return (
    <div className={WORKSPACE_CLASS}>
      <div
        key={`agent-chat-${activeId ?? "none"}`}
        className="flex min-h-0 min-w-0 flex-1 flex-col"
      >
        <WorkspaceHeader
          session={session}
          running={live.running}
          hasMessages={messages.length > 0}
          statusBadge={statusBadge}
          availablePanels={availablePanels}
          activePanel={visiblePanel}
          onBack={() => {
            invalidateSelection();
            setView("list");
          }}
          onClearHistory={() => void handleClearHistory()}
          onSelectPanel={(tab) =>
            setActiveTab((current) => (current === tab ? null : tab))
          }
        />

        {sessionError ? (
          <div role="alert" className="mx-4 mb-2 flex items-center gap-3 rounded-md border border-destructive/30 px-3 py-2 text-sm">
            <span className="min-w-0 flex-1">{sessionError}</span>
            <button type="button" className="shrink-0 underline" disabled={isSavingSettings} onClick={() => activeId && void refreshSession(activeId)}>
              重试加载
            </button>
          </div>
        ) : null}

        {session && draftLoadedFor === session.id ? (
          <div className="-mx-6 flex min-h-0 flex-1 overflow-hidden max-rail:-mx-4">
            <ChatPanel
              key={session.id}
              session={session}
              turnRail={
                <TurnRail
                  messages={messages}
                  live={live}
                  activeSeq={activeTurnSeq}
                  onJump={handleJumpTurn}
                />
              }
              initialDraft={activeDraft}
              onDraftChange={handleDraftChange}
              messages={messages}
              live={live}
              isSavingSettings={isSavingSettings}
              canSend={() => !hasPendingUpdate(session.id)}
              onSettingsChange={handleSettingsChange}
              onDismissError={dismissError}
              onDismissResult={dismissResult}
              onSend={(input: {
                content?: string;
                documents?: AgentDocument[];
                afterSeq?: number;
              }) => {
                if (hasPendingUpdate(session.id)) return;
                if (input.afterSeq != null) {
                  setMessages((current) =>
                    current.filter((message) => message.seq <= input.afterSeq!),
                  );
                }
                send(input);
              }}
              onApprove={handleApprove}
              onStop={handleStop}
              onOpenContextTab={setActiveTab}
              jumpTarget={jumpTarget}
              onActiveTurn={handleActiveTurn}
            />
            {panelMounted && lastPanel ? (
              <ContextPanel
                key={session.id}
                session={session}
                open={visiblePanel != null}
                activePanel={visiblePanel ?? lastPanel}
                busy={live.running || isSavingSettings || session.status !== "idle"}
                onClose={closeContextPanel}
                onConfirmPlan={() => void handleConfirmPlan()}
                onRestoreDraft={() => void handleRestoreDraft()}
              />
            ) : null}
          </div>
        ) : !sessionError ? (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            正在加载会话…
          </div>
        ) : null}
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
    dedupingInterval: SESSION_LIST_DEDUPLICATION_MS,
    refreshInterval: POLL.USAGE,
  });
}
