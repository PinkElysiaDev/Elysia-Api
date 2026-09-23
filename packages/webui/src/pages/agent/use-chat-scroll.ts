import { useEffect, useRef, useState } from "react";
import type { AgentMessage } from "@/lib/agent/types";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";

/** 距底小于此值视为“贴底”，恢复自动跟随 */
const SCROLL_STICKY_PX = 120;

/**
 * 聊天滚动控制：贴底自动跟随 + 跳底浮标显隐 + 轮数条跳转定位 + 当前轮上报。
 * live 也要传进来：流式增量（text/toolCards）不改变 messages.length，
 * 跟随滚动的触发依赖里必须包含它们，否则流式输出不会自动滚屏。
 */
export function useChatScroll({
  messages,
  live,
  jumpTarget,
  onActiveTurn,
}: {
  messages: AgentMessage[];
  live: AgentLiveState;
  /** 轮数条跳转目标：变化时滚动定位到对应消息（nonce 保证重复点击也生效）。 */
  jumpTarget?: { seq: number; nonce: number } | null;
  /** 滚动时上报当前视口所在的轮（最近一条用户消息 seq）。 */
  onActiveTurn?: (seq: number | null) => void;
}) {
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  // 用户上翻离开底部超过这个距离就停止自动跟随，避免流式增量把正在回看的
  // 历史拽回底部。
  const stickToBottomRef = useRef(true);
  const [showJumpBottom, setShowJumpBottom] = useState(false);

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages.length, live.text, live.toolCards.length]);

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
      stickToBottomRef.current = distance < SCROLL_STICKY_PX;
      setShowJumpBottom(distance >= SCROLL_STICKY_PX);
    };
    container.addEventListener("scroll", report, { passive: true });
    report();
    return () => container.removeEventListener("scroll", report);
  }, [messages, onActiveTurn]);

  /** 跳底浮标：恢复自动跟随并平滑滚到底部。 */
  const jumpToBottom = () => {
    stickToBottomRef.current = true;
    setShowJumpBottom(false);
    bottomRef.current?.scrollIntoView({
      behavior: "smooth",
      block: "end",
    });
  };

  return {
    scrollRef,
    bottomRef,
    stickToBottomRef,
    showJumpBottom,
    setShowJumpBottom,
    jumpToBottom,
  };
}
