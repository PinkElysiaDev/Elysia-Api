// 总览页 KPI 卡的时间窗档位偏好（累计 / 今日），localStorage 持久化 +
// useSyncExternalStore 订阅：多组件消费即时同步（同 tab 经自定义事件，
// 跨 tab 经 storage 事件）。模式与 agent 发送键偏好（send-key.ts）一致。
import { useSyncExternalStore } from "react";
import { STORAGE_KEYS } from "@/lib/storage-keys";

export type OverviewKpiRange = "all" | "today";

const CHANGE_EVENT = "elysia-webui:overview-kpi-range-change";

function readRange(): OverviewKpiRange {
  try {
    const value = window.localStorage.getItem(STORAGE_KEYS.overviewKpiRange);
    if (value === "all" || value === "today") return value;
  } catch {
    /* 隐私模式等读取失败：回退默认 */
  }
  return "all";
}

function subscribe(callback: () => void): () => void {
  window.addEventListener(CHANGE_EVENT, callback);
  window.addEventListener("storage", callback);
  return () => {
    window.removeEventListener(CHANGE_EVENT, callback);
    window.removeEventListener("storage", callback);
  };
}

export function setOverviewKpiRange(range: OverviewKpiRange): void {
  try {
    window.localStorage.setItem(STORAGE_KEYS.overviewKpiRange, range);
  } catch {
    /* 写失败不影响本次会话 */
  }
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

/** 组件内订阅式读取。 */
export function useOverviewKpiRange(): OverviewKpiRange {
  return useSyncExternalStore(subscribe, readRange, () => "all");
}
