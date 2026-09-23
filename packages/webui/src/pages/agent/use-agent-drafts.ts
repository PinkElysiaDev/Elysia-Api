import { useCallback, useEffect, useState } from "react";
import {
  loadComposerDraft,
  saveComposerDraft,
  sessionsWithDrafts,
  type AgentComposerDraft,
} from "@/lib/agent/draft-store";
import type { AgentSession } from "@/lib/agent/types";

/**
 * 未发送内容（文本 + 附件）的本机草稿状态：进入会话加载、击键保存、
 * 总览徽标扫描（哪些会话留有未发送内容）。
 */
export function useAgentDrafts(
  view: "list" | "chat",
  activeId: string | undefined,
  sessions: AgentSession[] | undefined,
) {
  const [composerDraft, setComposerDraft] = useState<{
    sessionId: string;
    draft: AgentComposerDraft;
  } | null>(null);
  const [draftSessions, setDraftSessions] = useState<Set<string>>(new Set());
  /** 草稿读取已完成的会话：ChatPanel 等它就绪再挂载，避免先播空再补草稿。 */
  const [draftLoadedFor, setDraftLoadedFor] = useState<string | null>(null);

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

  return { composerDraft, draftSessions, draftLoadedFor, handleDraftChange };
}
