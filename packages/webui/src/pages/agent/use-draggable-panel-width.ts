import { useCallback, useRef, useState } from "react";
import { STORAGE_KEYS } from "@/lib/storage-keys";

/** 侧栏宽度记忆键与范围（拖拽钳制，超范围回退默认 320）。 */
const PANEL_WIDTH_KEY = STORAGE_KEYS.agentPanelWidth;
const PANEL_WIDTH_MIN = 260;
const PANEL_WIDTH_MAX = 560;
const PANEL_WIDTH_DEFAULT = 320;

type PanelDragState = { startX: number; startW: number; latest: number };

/**
 * 侧栏宽度拖拽：向左拖增大、向右拖减小，钳制在范围内；松手时把最新宽度
 * 写入 localStorage（从 ref 取值：pointerup 可能先于最后一次 move 的 state
 * 提交）。返回宽度、拖拽中标志与挂到拖拽把手上的三个指针事件。
 */
export function useDraggablePanelWidth() {
  const [panelW, setPanelW] = useState<number>(() => {
    const saved = Number(window.localStorage.getItem(PANEL_WIDTH_KEY));
    return Number.isFinite(saved) &&
      saved >= PANEL_WIDTH_MIN &&
      saved <= PANEL_WIDTH_MAX
      ? saved
      : PANEL_WIDTH_DEFAULT;
  });
  const [panelDragging, setPanelDragging] = useState(false);
  const panelDragRef = useRef<PanelDragState | null>(null);

  const onPanelHandleDown = useCallback(
    (event: React.PointerEvent<HTMLDivElement>, panelOpen: boolean) => {
      if (!panelOpen) return;
      panelDragRef.current = {
        startX: event.clientX,
        startW: panelW,
        latest: panelW,
      };
      setPanelDragging(true);
      event.currentTarget.setPointerCapture(event.pointerId);
    },
    [panelW],
  );
  const onPanelHandleMove = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      const drag = panelDragRef.current;
      if (!drag) return;
      const next = Math.min(
        PANEL_WIDTH_MAX,
        Math.max(PANEL_WIDTH_MIN, drag.startW + (drag.startX - event.clientX)),
      );
      drag.latest = next;
      setPanelW(next);
    },
    [],
  );
  const onPanelHandleUp = useCallback(() => {
    const drag = panelDragRef.current;
    if (!drag) return;
    panelDragRef.current = null;
    setPanelDragging(false);
    window.localStorage.setItem(
      PANEL_WIDTH_KEY,
      String(Math.round(drag.latest)),
    );
  }, []);

  return {
    panelW,
    panelDragging,
    onPanelHandleDown,
    onPanelHandleMove,
    onPanelHandleUp,
  };
}
