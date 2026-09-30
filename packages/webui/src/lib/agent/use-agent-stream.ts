import { useCallback, useEffect, useRef, useState } from "react";
import { streamAgentEvents } from "./sse";
import { agentUsageToTurn } from "./types";
import type {
  AgentCompaction,
  AgentContextUsage,
  AgentPendingAction,
  AgentPlanStep,
  AgentStreamEvent,
  AgentTurnUsage,
} from "./types";

export interface AgentToolCard {
  callId: string;
  name: string;
  status: "running" | "done" | "failed";
  summary?: string;
  input?: unknown;
  result?: unknown;
  /** 执行中由 tool_progress 心跳刷新的已耗时。 */
  elapsedMs?: number;
  /** CLI 批内逐命令进度（「正在执行（2/3）：elysia …」）；心跳无 text。 */
  progressText?: string;
}

export interface AgentLiveState {
  running: boolean;
  statusText: string;
  text: string;
  reasoning: string;
  toolCards: AgentToolCard[];
  approvalPending: AgentPendingAction | null;
  error: { text: string; retryable: boolean } | null;
  turnUsage: AgentTurnUsage | null;
  context: AgentContextUsage | null;
  compaction: AgentCompaction | null;
  /** 本轮现场的方案更新提示（不入库，turn_done 清空）。 */
  planNotice: { done: number; total: number } | null;
}

const initialState: AgentLiveState = {
  running: false,
  statusText: "",
  text: "",
  reasoning: "",
  toolCards: [],
  approvalPending: null,
  error: null,
  turnUsage: null,
  context: null,
  compaction: null,
  planNotice: null,
};

function planNoticeOf(
  plan: AgentPlanStep[] | undefined,
): { done: number; total: number } | null {
  if (!plan || plan.length === 0) return null;
  return {
    total: plan.length,
    done: plan.filter((step) => step.status === "done").length,
  };
}

function reduce(
  state: AgentLiveState,
  event: AgentStreamEvent,
): AgentLiveState {
  switch (event.type) {
    case "status":
      return { ...state, statusText: event.text ?? "" };
    case "text_delta":
      return { ...state, text: state.text + (event.delta ?? "") };
    case "reasoning_delta":
      return { ...state, reasoning: state.reasoning + (event.delta ?? "") };
    case "tool_call": {
      const callId = event.callId ?? `live-${state.toolCards.length}`;
      return {
        ...state,
        toolCards: [
          ...state.toolCards,
          {
            callId,
            name: event.name ?? callId,
            status: "running",
            input: event.input,
          },
        ],
      };
    }
    case "tool_progress": {
      const callId = event.callId ?? "";
      return {
        ...state,
        toolCards: state.toolCards.map((card) =>
          card.callId === callId
            ? {
                ...card,
                elapsedMs: event.elapsedMs,
                // CLI 批内逐命令进度（引擎 ProgressReporter 上报）；纯耗时
                // 心跳不带 text，保留上一条进度不清空。
                ...(event.text ? { progressText: event.text } : {}),
              }
            : card,
        ),
      };
    }
    case "tool_result": {
      const callId = event.callId ?? event.result?.callId ?? "";
      return {
        ...state,
        toolCards: state.toolCards.map((card) =>
          card.callId === callId && card.status === "running"
            ? {
                ...card,
                status: event.result?.ok === false ? "failed" : "done",
                summary: event.result?.summary,
                input: event.result?.input ?? card.input,
                result: event.result?.data,
                progressText: undefined,
              }
            : card,
        ),
      };
    }
    case "plan_updated":
      return { ...state, planNotice: planNoticeOf(event.plan) };
    case "context_updated":
      return { ...state, context: event.context ?? state.context };
    case "context_compacted":
      return { ...state, compaction: event.compaction ?? state.compaction };
    case "approval_required":
      return {
        ...state,
        running: false,
        approvalPending: event.approval ?? null,
        statusText: "",
      };
    case "message": {
      // 持久化消息已落库：清掉对应的现场（终稿或工具结果），避免双份展示。
      const role = event.message?.role;
      if (role === "assistant") return { ...state, text: "", reasoning: "" };
      if (role === "tool_result") {
        const content = event.message?.content as
          { callId?: string } | undefined;
        const callId = content?.callId ?? "";
        return {
          ...state,
          toolCards: state.toolCards.filter((card) => card.callId !== callId),
        };
      }
      return state;
    }
    case "turn_done": {
      const usage = event.usage;
      return {
        ...state,
        running: false,
        statusText: "",
        // 轮次结束即清空现场残留：终稿与工具结果都以落库行（refreshSession
        // 拉回的 messages）呈现。不清的话 live 工具卡与落库 tool_result 行
        // 会并排双渲染；若终稿 message 事件曾被丢（有损 emitEvent），live
        // 文本也会与落库助手消息双份。
        text: "",
        reasoning: "",
        toolCards: [],
        planNotice: null,
        compaction: null,
        turnUsage: usage ? agentUsageToTurn(usage) : null,
      };
    }
    case "error":
      return {
        ...state,
        running: false,
        statusText: "",
        // 出错路径引擎会把部分终稿落库（turn_done 随后触发刷新），live 文本
        // 同样让位给落库行，避免双份。
        text: "",
        reasoning: "",
        error: {
          text: event.text ?? "未知错误",
          retryable: event.retryable ?? false,
        },
      };
    default:
      return state;
  }
}

export interface UseAgentStreamOptions {
  /** 持久化消息事件回调（Chat 负责并入消息列表）。 */
  onMessage?: (event: AgentStreamEvent) => void;
  /** 草稿更新 / 轮次结束等需要刷新会话详情的时机。 */
  onSessionDirty?: () => void;
}

/**
 * 单会话的流式轮次状态机：send/approve 启动 SSE，事件归约为现场态
 * （增量文本/思维链/工具卡片/审批卡/错误）。断流不取消服务端轮次，
 * stop() 才会真正停止。
 */
export function useAgentStream(
  sessionId: string | undefined,
  options: UseAgentStreamOptions = {},
) {
  const [live, setLive] = useState<AgentLiveState>(initialState);
  const abortRef = useRef<AbortController | null>(null);
  const activeSessionRef = useRef<string | undefined>(sessionId);
  const optionsRef = useRef(options);
  optionsRef.current = options;

  useEffect(() => {
    // 会话切换重置现场。
    activeSessionRef.current = sessionId;
    setLive(initialState);
    return () => {
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [sessionId]);

  const runStream = useCallback(
    (path: string, body: unknown) => {
      if (!sessionId) return;
      setLive((state) => ({
        ...initialState,
        running: true,
        turnUsage: state.turnUsage,
      }));
      const controller = new AbortController();
      abortRef.current = controller;
      streamAgentEvents(
        `/api/admin/agent/sessions/${sessionId}${path}`,
        body,
        (event) => {
          // 会话归属守卫：abort 只断流，已解析入队的事件还会派发一次——
          // 快速 A→B 切换的毫秒窗口内，A 的尾部事件不得混进 B 的现场/消息列表。
          if (activeSessionRef.current !== sessionId) return;
          // tool_result 事件直接携带落库消息，并不会再发送单独的 message
          // 事件。所有带消息的事件都即时并入时间线，不能等 turn_done 刷新。
          if (event.message) optionsRef.current.onMessage?.(event);
          if (
            event.type === "draft_updated" ||
            event.type === "plan_updated" ||
            event.type === "turn_done"
          ) {
            optionsRef.current.onSessionDirty?.();
          }
          setLive((state) => {
            const next = reduce(state, event);
            // 落库与清理现场使用同一套逻辑，保留不带消息的旧事件兼容性。
            return event.message && event.type !== "message"
              ? reduce(next, { type: "message", message: event.message })
              : next;
          });
        },
        controller.signal,
      ).catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setLive((state) =>
          state.running
            ? {
                ...state,
                running: false,
                statusText: "",
                error: {
                  text: err instanceof Error ? err.message : "连接中断",
                  retryable: true,
                },
              }
            : state,
        );
        optionsRef.current.onSessionDirty?.();
      }).then(() => {
        // 兜底：流结束（reader done / 连接断）但没收到 turn_done/error 终态
        // 时也必须退出 running，否则界面永卡「进行中」。
        setLive((state) =>
          state.running
            ? { ...state, running: false, statusText: "", toolCards: [] }
            : state,
        );
      });
    },
    [sessionId],
  );

  const send = useCallback(
    (input: { content?: string; documents?: unknown[]; afterSeq?: number }) => {
      runStream("/messages", input);
    },
    [runStream],
  );

  const approve = useCallback(
    (decision: {
      approved: boolean;
      baseUrl?: string;
      apiKey?: string;
      note?: string;
      answer?: string;
    }) => {
      setLive((state) => ({ ...state, approvalPending: null }));
      runStream("/approve", decision);
    },
    [runStream],
  );

  const stop = useCallback(async () => {
    if (!sessionId) return;
    try {
      // 先等后端 Stop 同步收尾（期间 error/turn_done 终态事件可经 SSE 到达
      // 并正常 reduce），再断开连接——反过来（先 abort）终态永远到不了，
      // live.running 挂死、界面卡「进行中」。
      const { stopAgentTurn } = await import("./api");
      await stopAgentTurn(sessionId);
    } catch {
      /* 停止请求失败也要复位本地状态（onSessionDirty 会拉回真实状态） */
    } finally {
      abortRef.current?.abort();
      abortRef.current = null;
      // 兜底复位：流可能已断（终态事件没到），不主动清 running 会永挂。
      setLive((state) =>
        state.running
          ? { ...state, running: false, statusText: "", toolCards: [] }
          : state,
      );
      optionsRef.current.onSessionDirty?.();
    }
  }, [sessionId]);

  const dismissError = useCallback(() => {
    setLive((state) => ({ ...state, error: null }));
  }, []);

  /** 会话详情回灌审批卡：waiting_approval 状态在刷新/切会话后 SSE 现场已
   *  丢失，不回灌的话待审批轮次从此无法在 UI 上批准。
   *  以轮询到的后端状态为准：远程面（插件/MCP/A2A）批准或轮次结束后
   *  pendingAction 已清空或换成了下一张卡，本地旧卡必须同步清除/替换，
   *  否则会僵尸占据交互区；仅本地流式进行中以 SSE 事件为准不覆盖。 */
  const hydrateApproval = useCallback(
    (approval: AgentPendingAction | null | undefined) => {
      setLive((state) => {
        if (state.running) return state
        if (!approval) {
          return state.approvalPending ? { ...state, approvalPending: null } : state
        }
        if (state.approvalPending && samePendingAction(state.approvalPending, approval)) return state
        return { ...state, approvalPending: approval }
      })
    },
    [],
  );

  return { live, send, approve, stop, dismissError, hydrateApproval };
}

/** 两张待批卡是否指向同一动作（类型 + 调用集合一致）。 */
function samePendingAction(a: AgentPendingAction, b: AgentPendingAction) {
  if ((a.kind ?? "") !== (b.kind ?? "")) return false
  const callIds = (pending: AgentPendingAction) =>
    pending.calls.map((call) => call.id ?? call.name ?? "").join(",")
  return callIds(a) === callIds(b)
}
