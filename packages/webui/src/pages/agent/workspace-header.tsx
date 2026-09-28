import {
  ArrowLeft,
  Eraser,
  FileCode2,
  ListChecks,
} from "lucide-react";
import { TonePill } from "@/components/badges";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { AgentContextTab, AgentSession } from "@/lib/agent/types";

export interface WorkspaceHeaderProps {
  session: AgentSession | undefined;
  /** 轮次进行中（清空历史按钮禁用等）。 */
  running: boolean;
  /** 是否有已落库消息（无消息时禁用清空历史）。 */
  hasMessages: boolean;
  /** 状态胶囊（进行中 / 待审批 / 计划模式），null 不渲染。 */
  statusBadge: { text: string; color: string } | null;
  availablePanels: AgentContextTab[];
  activePanel: AgentContextTab | null;
  onBack: () => void;
  onClearHistory: () => void;
  onSelectPanel: (panel: AgentContextTab) => void;
}

/** 工作区顶栏：返回总览 + 会话标题（编辑模式附加协议标识）+ 状态胶囊 +
 * 清空历史 + 有内容时才出现的任务资料入口。 */
export function WorkspaceHeader({
  session,
  running,
  hasMessages,
  statusBadge,
  availablePanels,
  activePanel,
  onBack,
  onClearHistory,
  onSelectPanel,
}: WorkspaceHeaderProps) {
  return (
    <div className="flex items-center gap-2 pr-4 pb-2 pt-1">
      <Button
        variant="ghost"
        size="icon"
        className="h-8 w-11"
        title="返回会话总览"
        onClick={onBack}
      >
        <ArrowLeft className="h-4 w-4" />
      </Button>
      <p className="min-w-0 flex-1 truncate text-sm font-semibold">
        {session?.title || "AI 助手"}
        {session?.mode === "edit" ? (
          <span className="ml-2 font-normal text-muted-foreground">
            编辑协议 {session.protocolId ?? ""}
          </span>
        ) : null}
      </p>
      {statusBadge ? (
        <TonePill color={statusBadge.color} className="text-2xs">
          {statusBadge.text}
        </TonePill>
      ) : null}
      {session ? (
        <Button
          variant="ghost"
          size="icon"
          className="h-8 w-8"
          title="清空本会话消息（保留草稿与设置）"
          disabled={running || !hasMessages}
          onClick={onClearHistory}
        >
          <Eraser className="h-4 w-4" />
        </Button>
      ) : null}
      {availablePanels.map((panel) => {
        const selected = activePanel === panel;
        const label = panel === "plan" ? "任务方案" : "协议草稿";
        const done = session?.plan?.filter((step) => step.status === "done").length ?? 0;
        return (
          <button
            key={panel}
            id={`agent-context-trigger-${panel}`}
            type="button"
            aria-label={label}
            aria-expanded={selected}
            aria-controls={selected ? "agent-context-panel" : undefined}
            title={`${selected ? "收起" : "查看"}${label}`}
            onClick={() => onSelectPanel(panel)}
            className={cn(
              "inline-flex h-8 shrink-0 items-center gap-1.5 rounded-full px-2.5 text-xs outline-none transition-colors hover:bg-wash hover:text-foreground focus-visible:bg-wash max-rail:h-10",
              selected ? "bg-wash text-foreground" : "text-muted-foreground",
            )}
          >
            {panel === "plan" ? <ListChecks className="h-3.5 w-3.5" /> : <FileCode2 className="h-3.5 w-3.5" />}
            <span className="max-[480px]:sr-only">{label}</span>
            {panel === "plan" && !!session?.plan?.length ? (
              <span className="tnum text-2xs text-muted-foreground">{done}/{session.plan.length}</span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
