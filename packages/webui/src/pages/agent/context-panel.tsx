import { X } from "lucide-react";
import { useEffect, useRef } from "react";
import type {
  AgentContextTab,
  AgentMessage,
  AgentSession,
} from "@/lib/agent/types";
import type { AgentLiveState } from "@/lib/agent/use-agent-stream";
import { cn } from "@/lib/utils";
import { ActivityView } from "./activity-view";
import { DraftView } from "./draft-view";
import { PlanView } from "./plan-view";

/**
 * 通用标签页侧栏：不预设分类——方案更新开「方案」页、草稿变化开「配置」页、
 * 工具活动可点开「动态」页；每页都是窗口内一个可关闭的标签，模型打开什么
 * 就展示什么。标签状态由页面持有（跨组件联动）。
 */

const TAB_META: Record<AgentContextTab, { label: string }> = {
  plan: { label: "方案" },
  draft: { label: "配置" },
  activity: { label: "动态" },
};

export interface ContextPanelProps {
  session: AgentSession;
  messages: AgentMessage[];
  live: AgentLiveState;
  tabs: AgentContextTab[];
  activeTab: AgentContextTab | null;
  onTabSelect: (tab: AgentContextTab) => void;
  onTabClose: (tab: AgentContextTab) => void;
  onAutoOpen: (tab: AgentContextTab) => void;
  /** 计划模式下确认执行当前方案（关闭计划模式并开始执行）。 */
  onConfirmPlan: () => void;
  /** 把草稿回滚到最近一轮修改前的还原点。 */
  onRestoreDraft: () => void;
}

export function ContextPanel({
  session,
  messages,
  live,
  tabs,
  activeTab,
  onTabSelect,
  onTabClose,
  onAutoOpen,
  onConfirmPlan,
  onRestoreDraft,
}: ContextPanelProps) {
  const pinned = useRef(false);
  const wasRunning = useRef(false);
  const activityAutoOpened = useRef(false);

  const plan = session.plan ?? [];
  const planJSON = JSON.stringify(plan);
  const draftJSON =
    session.draftConfig == null ? "" : JSON.stringify(session.draftConfig);
  const lastPlan = useRef(planJSON);
  const lastDraft = useRef(draftJSON);

  // 新 turn 开始：解除手动固定，恢复自动跟随；动态页自动开闸重置。
  useEffect(() => {
    if (live.running && !wasRunning.current) {
      pinned.current = false;
      activityAutoOpened.current = false;
    }
    wasRunning.current = live.running;
  }, [live.running]);

  // 方案变化 → 打开并激活方案页。
  useEffect(() => {
    if (planJSON !== lastPlan.current) {
      lastPlan.current = planJSON;
      if (!pinned.current) onAutoOpen("plan");
    }
  }, [planJSON, onAutoOpen]);

  // 草稿变化 → 打开并激活配置页。
  useEffect(() => {
    if (draftJSON !== lastDraft.current) {
      lastDraft.current = draftJSON;
      if (!pinned.current) onAutoOpen("draft");
    }
  }, [draftJSON, onAutoOpen]);

  // 工具开始执行且当前没有任何激活标签 → 每轮至多自动打开一次动态页。
  useEffect(() => {
    const running = live.toolCards.some((card) => card.name !== "update_plan");
    if (running && !activityAutoOpened.current) {
      activityAutoOpened.current = true;
      if (!pinned.current && activeTab == null) onAutoOpen("activity");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live.toolCards]);

  const doneCount = plan.filter((step) => step.status === "done").length;

  return (
    <div className="flex h-full w-full min-w-0 flex-col pl-4">
      <div
        role="tablist"
        aria-label="侧栏标签页"
        className="flex items-center gap-3 px-1 pb-2 pt-1"
      >
        {tabs.map((tab) => (
          <div key={tab} className="group flex items-center">
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === tab}
              onClick={() => {
                pinned.current = true;
                onTabSelect(tab);
              }}
              className={cn(
                "relative pb-1.5 text-xs transition-colors",
                activeTab === tab
                  ? "font-medium text-rose after:absolute after:inset-x-0 after:bottom-0 after:h-[2px] after:rounded-full after:bg-rose"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {TAB_META[tab].label}
              {tab === "plan" && plan.length > 0 ? (
                <span className="tnum ml-1 text-2xs text-muted-foreground">
                  {doneCount}/{plan.length}
                </span>
              ) : null}
            </button>
            <button
              type="button"
              className="ml-0.5 rounded p-0.5 text-muted-foreground/60 opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100"
              title={`关闭「${TAB_META[tab].label}」`}
              aria-label={`关闭「${TAB_META[tab].label}」标签页`}
              onClick={() => onTabClose(tab)}
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        ))}
      </div>
      <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto pb-2">
        {tabs.length === 0 ? (
          <p className="px-2 py-10 text-center text-2xs text-muted-foreground">
            方案、配置详情与工具动态会以标签页在这里打开
          </p>
        ) : activeTab === "plan" ? (
          <PlanView
            steps={plan}
            planMode={!!session.settings.planMode}
            busy={live.running}
            onConfirm={onConfirmPlan}
          />
        ) : activeTab === "draft" ? (
          <DraftView
            session={session}
            busy={live.running}
            onRestore={onRestoreDraft}
          />
        ) : activeTab === "activity" ? (
          <ActivityView messages={messages} live={live} />
        ) : null}
      </div>
    </div>
  );
}
