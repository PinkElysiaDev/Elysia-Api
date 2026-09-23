import { useEffect, type RefObject } from "react";

/**
 * 浮层的「点击外部 / Escape 关闭」。open 为 false 时不挂监听。
 * 两处以上浮层（composer 菜单、模型选择器）此前各写一份且 Escape 行为
 * 不一致，统一由此提供。
 */
export function useDismissable(
  open: boolean,
  close: () => void,
  ref: RefObject<HTMLElement | null>,
): void {
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) close();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, close, ref]);
}
