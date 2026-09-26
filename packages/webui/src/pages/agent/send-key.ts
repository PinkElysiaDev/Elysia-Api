// agent 输入的发送键偏好（Enter 直发 / Ctrl+Enter 发送），localStorage
// 持久化 + useSyncExternalStore 订阅：composer、编辑重发、方案确认卡等
// 多处消费即时同步（同 tab 经自定义事件，跨 tab 经 storage 事件）。
import { useEffect, useSyncExternalStore } from "react";
import { STORAGE_KEYS } from "@/lib/storage-keys";

export type SendKeyMode = "enter" | "ctrl-enter";

const CHANGE_EVENT = "elysia-webui:agent-send-key-change";

function readMode(): SendKeyMode {
  try {
    const value = window.localStorage.getItem(STORAGE_KEYS.agentSendKey);
    if (value === "enter" || value === "ctrl-enter") return value;
  } catch {
    /* 隐私模式等读取失败：回退默认 */
  }
  return "enter";
}

function subscribe(callback: () => void): () => void {
  window.addEventListener(CHANGE_EVENT, callback);
  window.addEventListener("storage", callback);
  return () => {
    window.removeEventListener(CHANGE_EVENT, callback);
    window.removeEventListener("storage", callback);
  };
}

export function setSendKeyMode(mode: SendKeyMode): void {
  try {
    window.localStorage.setItem(STORAGE_KEYS.agentSendKey, mode);
  } catch {
    /* 写失败不影响本次会话（内存快照仍会更新） */
  }
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

/** 当前生效的发送键模式（组件外也可用，如按钮 title 计算）。 */
export function sendKeyMode(): SendKeyMode {
  return readMode();
}

/** 组件内订阅式读取。 */
export function useSendKeyMode(): SendKeyMode {
  return useSyncExternalStore(subscribe, readMode, () => "enter");
}

/**
 * 按模式判定一次按键是否为「发送」。
 * IME 守卫：中文等输入法组词期间的 Enter（isComposing）不视为发送，
 * 否则选词回车会把半成品发出去。
 */
export function isSendKeyEvent(
  event: React.KeyboardEvent,
  mode: SendKeyMode,
): boolean {
  if (event.key !== "Enter") return false;
  if (event.nativeEvent.isComposing) return false;
  if (mode === "enter") {
    // 裸 Enter 发送；Shift+Enter（及其他修饰组合）留给换行。
    return !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey;
  }
  return event.ctrlKey || event.metaKey;
}

/** 发送按钮 title / placeholder 用的提示文案。 */
export function sendKeyHint(mode: SendKeyMode): string {
  return mode === "enter" ? "Enter" : "Ctrl+Enter";
}

// ---- 审批/提问卡的 Esc 快捷键（拒绝审批 / 跳过作答） ----

export type EscActionMode = "on" | "off";

function readEscMode(): EscActionMode {
  try {
    const value = window.localStorage.getItem(STORAGE_KEYS.agentEscAction);
    if (value === "on" || value === "off") return value;
  } catch {
    /* 读取失败回退默认 */
  }
  return "on";
}

export function setEscActionMode(mode: EscActionMode): void {
  try {
    window.localStorage.setItem(STORAGE_KEYS.agentEscAction, mode);
  } catch {
    /* 写失败不影响本次会话 */
  }
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

export function useEscActionMode(): EscActionMode {
  return useSyncExternalStore(subscribe, readEscMode, () => "on");
}

/**
 * 全局 Escape 触发卡片的快捷动作（拒绝审批 / 跳过提问）。
 * 守卫：浮层或对话框打开时不触发（各自的 Esc 关闭逻辑优先）；焦点在
 * 有内容的输入框时不触发（防误按丢掉正在输入的答案）；带修饰键不触发。
 * action 为 null 时不挂监听（偏好关闭 / busy / 卡片未挂载）。
 */
export function useEscapeAction(action: (() => void) | null): void {
  useEffect(() => {
    if (!action) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return;
      if (document.querySelector('[role="menu"], [role="dialog"]')) return;
      const active = document.activeElement;
      if (
        active instanceof HTMLInputElement ||
        active instanceof HTMLTextAreaElement
      ) {
        if (active.value.trim() !== "") return;
      }
      action();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [action]);
}
