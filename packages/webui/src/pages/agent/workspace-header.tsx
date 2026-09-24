import {
  ArrowLeft,
  Eraser,
  PanelRightClose,
  PanelRightOpen,
} from "lucide-react";
import { TonePill } from "@/components/badges";
import { Button } from "@/components/ui/button";
import type { AgentSession } from "@/lib/agent/types";

export interface WorkspaceHeaderProps {
  session: AgentSession | undefined;
  /** 轮次进行中（清空历史按钮禁用等）。 */
  running: boolean;
  /** 是否有已落库消息（无消息时禁用清空历史）。 */
  hasMessages: boolean;
  /** 状态胶囊（进行中 / 待审批 / 计划模式），null 不渲染。 */
  statusBadge: { text: string; color: string } | null;
  panelOpen: boolean;
  onBack: () => void;
  onClearHistory: () => void;
  onTogglePanel: () => void;
}

/** 工作区顶栏：返回总览 + 会话标题（编辑模式附加协议标识）+ 状态胶囊 +
 * 清空历史 + 侧栏开关。 */
export function WorkspaceHeader({
  session,
  running,
  hasMessages,
  statusBadge,
  panelOpen,
  onBack,
  onClearHistory,
  onTogglePanel,
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
      <Button
        variant="ghost"
        size="icon"
        className="h-8 w-8"
        title={panelOpen ? "收起侧栏" : "展开侧栏"}
        onClick={onTogglePanel}
      >
        {panelOpen ? (
          <PanelRightClose className="h-4 w-4" />
        ) : (
          <PanelRightOpen className="h-4 w-4" />
        )}
      </Button>
    </div>
  );
}
