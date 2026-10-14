import { useCallback, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { getAgentSession, updateAgentSession } from "@/lib/agent/api";
import type { AgentMessage, AgentSession, AgentSettings } from "@/lib/agent/types";

/** Owns detail snapshots and serialized edits for the selected Agent session.
 * Live tool secrets remain in useAgentStream, outside this history state.
 */
export function useAgentSession(mutateSessions: () => Promise<unknown>, onError: (error: unknown) => void) {
  const [activeId, setActiveId] = useState<string | undefined>();
  const [session, setSession] = useState<AgentSession | undefined>();
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [sessionError, setSessionError] = useState<string>();
  const pendingUpdates = useRef(new Set<string>());
  const [savingSessions, setSavingSessions] = useState<ReadonlySet<string>>(new Set());
  const isSavingSettings = Boolean(activeId && savingSessions.has(activeId));
  const activeIdRef = useRef(activeId);
  const sessionRequest = useRef(0);
  // Selection ownership survives polling, but expires even after an A → B → A trip.
  const selectionGeneration = useRef(0);
  const selectSession = useCallback((id: string | undefined) => {
    activeIdRef.current = id;
    selectionGeneration.current++;
    sessionRequest.current++;
    setSessionError(undefined);
    setActiveId(id);
  }, []);
  const isCurrentSelection = useCallback((id: string, generation: number) =>
    activeIdRef.current === id && selectionGeneration.current === generation, []);

  const refreshSession = useCallback(async (id: string) => {
    if (pendingUpdates.current.has(id)) return;
    const requestId = ++sessionRequest.current;
    try {
      const detail = await getAgentSession(id);
      // 快速 A→B 切换时 A 的慢响应不能覆盖已激活的 B。
      if (activeIdRef.current !== id || sessionRequest.current !== requestId) return;
      setSession(detail.session);
      setMessages(detail.messages ?? []);
      setSessionError(undefined);
    } catch (error) {
      if (activeIdRef.current !== id || sessionRequest.current !== requestId) return;
      if (error instanceof DOMException && error.name === "AbortError") return;
      if (error instanceof ApiError && error.status === 401) return;
      setSessionError(error instanceof ApiError && error.status === 404
        ? "会话不存在或已被删除"
        : error instanceof Error ? error.message : "加载会话失败");
    }
  }, []);

  // A synchronous per-session guard also covers clicks before React renders.
  const saveSession = useCallback(
    async (id: string, patch: { settings?: Partial<AgentSettings>; title?: string }): Promise<boolean> => {
      if (pendingUpdates.current.has(id)) return false;
      const generation = selectionGeneration.current;
      pendingUpdates.current.add(id);
      setSavingSessions(new Set(pendingUpdates.current));
      if (activeIdRef.current === id) sessionRequest.current++;
      try {
        const updated = await updateAgentSession(id, patch);
        if (isCurrentSelection(id, generation)) {
          sessionRequest.current++;
          setSession((current) => current && {
            ...current,
            ...(patch.title !== undefined ? { title: updated.title } : {}),
            ...(patch.settings ? { settings: updated.settings } : {}),
          });
        }
        void mutateSessions();
        return isCurrentSelection(id, generation);
      } catch (error) {
        onError(error);
        return false;
      } finally {
        pendingUpdates.current.delete(id);
        setSavingSessions(new Set(pendingUpdates.current));
        // A fresh read also unblocks A → B → A while the old write was pending.
        if (activeIdRef.current === id) void refreshSession(id);
      }
    },
    [isCurrentSelection, mutateSessions, refreshSession, onError],
  );

  const invalidateDetailRequests = useCallback(() => { sessionRequest.current++; }, []);
  const captureSelection = useCallback(() => selectionGeneration.current, []);
  const invalidateSelection = useCallback(() => ++selectionGeneration.current, []);
  const isActiveSession = useCallback((id: string) => activeIdRef.current === id, []);
  const hasPendingUpdate = useCallback((id: string) => pendingUpdates.current.has(id), []);

  return {
    activeId, session, setSession, messages, setMessages, sessionError,
    savingSessions, isSavingSettings, selectSession, refreshSession, saveSession,
    isCurrentSelection, invalidateDetailRequests, captureSelection,
    invalidateSelection, isActiveSession, hasPendingUpdate,
  };
}
