import { FileCode2, ListChecks, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import type { AgentContextTab, AgentSession } from "@/lib/agent/types";
import { cn } from "@/lib/utils";
import { DraftView } from "./draft-view";
import { PlanView } from "./plan-view";
import { useDraggablePanelWidth } from "./use-draggable-panel-width";

export interface ContextPanelProps {
  session: AgentSession;
  activePanel: AgentContextTab;
  busy: boolean;
  onClose: () => void;
  onConfirmPlan: () => void;
  onRestoreDraft: () => void;
  /** 展开/收起受控:父级关闭时保持挂载并传 false,由本组件播放收拢过渡。 */
  open: boolean;
}

/** 只承载可持续查看与操作的任务资料，不重复对话内的工具记录。 */
export function ContextPanel({
  session,
  activePanel,
  busy,
  onClose,
  onConfirmPlan,
  onRestoreDraft,
  open,
}: ContextPanelProps) {
  const [compact, setCompact] = useState(
    () => window.matchMedia("(max-width: 1199px)").matches,
  );
  const closeRef = useRef<HTMLButtonElement>(null);
  const {
    panelW,
    panelDragging,
    onPanelHandleDown,
    onPanelHandleMove,
    onPanelHandleUp,
  } = useDraggablePanelWidth();

  useEffect(() => {
    const media = window.matchMedia("(max-width: 1199px)");
    const sync = () => setCompact(media.matches);
    sync();
    media.addEventListener("change", sync);
    return () => media.removeEventListener("change", sync);
  }, []);

  useEffect(() => {
    if (!compact) closeRef.current?.focus({ preventScroll: true });
  }, [compact]);

  // 展开/收拢过渡:挂载后下一帧从 0 宽/透明过渡到目标宽(入场动画);此后
  // 随 open 受控——父级关闭时保持挂载传 false,由宽屏分支的宽度+淡出收拢,
  // 过渡结束后父级再卸载(父级计时 320ms = 300ms 过渡 + 余量)。这组 hooks
  // 必须无条件调用:窄屏抽屉分支在此之后提前返回,断点切换会改变 hook 数。
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setExpanded(true));
    return () => cancelAnimationFrame(id);
  }, []);
  useEffect(() => {
    setExpanded(open);
  }, [open]);

  const title = activePanel === "plan" ? "任务方案" : "协议草稿";
  const content = (
    <>
      <div className="flex shrink-0 items-center gap-2 px-5 pb-4 pt-5">
        {activePanel === "plan" ? (
          <ListChecks className="h-4 w-4 text-muted-foreground" />
        ) : (
          <FileCode2 className="h-4 w-4 text-muted-foreground" />
        )}
        {compact ? (
          <SheetTitle className="flex-1 font-sans text-sm tracking-normal">
            {title}
          </SheetTitle>
        ) : (
          <h2 id="agent-context-title" className="flex-1 text-sm font-medium">
            {title}
          </h2>
        )}
        <button
          ref={closeRef}
          type="button"
          aria-label="收起任务资料"
          title="收起任务资料"
          onClick={onClose}
          className="flex h-8 w-8 items-center justify-center rounded-full text-muted-foreground outline-none transition-colors hover:bg-wash hover:text-foreground focus-visible:bg-wash"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div key={activePanel} className="min-h-0 flex-1 px-4 pb-5">
        {activePanel === "plan" ? (
          <PlanView
            steps={session.plan ?? []}
            summary={session.planSummary}
            planMode={!!session.settings.planMode}
            busy={busy}
            onConfirm={onConfirmPlan}
          />
        ) : (
          <DraftView session={session} busy={busy} onRestore={onRestoreDraft} />
        )}
      </div>
    </>
  );

  // 窄窗口用抽屉，避免把正文与输入框挤成窄列。
  if (compact) {
    return (
      <Sheet
        open
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
      >
        <SheetContent
          id="agent-context-panel"
          hideClose
          aria-describedby={undefined}
          className="w-[min(380px,94vw)]"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            document
              .getElementById(`agent-context-trigger-${activePanel}`)
              ?.focus();
          }}
        >
          {content}
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <aside
      id="agent-context-panel"
      aria-labelledby="agent-context-title"
      className="relative flex h-full shrink-0 flex-col overflow-hidden border-l border-border/50 transition-[width,max-width,opacity] duration-300 ease-out motion-reduce:transition-none"
      style={{
        width: expanded ? panelW : 0,
        maxWidth: expanded ? "45%" : 0,
        opacity: expanded ? 1 : 0,
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.defaultPrevented) {
          event.preventDefault();
          onClose();
        }
      }}
    >
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="拖拽调整侧栏宽度"
        onPointerDown={(event) => onPanelHandleDown(event, true)}
        onPointerMove={onPanelHandleMove}
        onPointerUp={onPanelHandleUp}
        onPointerCancel={onPanelHandleUp}
        className={cn(
          "absolute inset-y-0 -left-1 w-2 cursor-col-resize touch-none select-none transition-colors hover:bg-wash",
          panelDragging && "bg-wash",
        )}
      />
      {content}
    </aside>
  );
}
