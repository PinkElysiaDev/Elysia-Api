import { CheckCircle2, Circle, ListChecks, Loader2, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { EmptyHint } from "./activity-view";

/** 方案页：update_plan 维护的分析摘要 + 步骤清单；计划模式下提供「确认执行」。 */
export function PlanView({
  steps,
  summary,
  planMode,
  busy,
  onConfirm,
}: {
  steps: { title: string; status: string }[];
  summary?: string;
  planMode: boolean;
  busy: boolean;
  onConfirm: () => void;
}) {
  if (steps.length === 0) {
    return (
      <EmptyHint
        icon={<ListChecks className="h-4 w-4" />}
        text="多步任务开始时，助手会在这里给出分析摘要与方案步骤并随进度更新。"
      />
    );
  }
  const done = steps.filter((step) => step.status === "done").length;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="no-scrollbar min-h-0 flex-1 space-y-1 overflow-y-auto px-1 py-1">
        {summary ? (
          <div className="mb-2 rounded-lg bg-wash px-2.5 py-2">
            <p className="pb-0.5 text-2xs font-medium text-muted-foreground">
              分析摘要
            </p>
            <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground/90">
              {summary}
            </p>
          </div>
        ) : null}
        <p className="tnum px-1 pb-1 text-2xs text-muted-foreground">
          {done}/{steps.length} 已完成
        </p>
        {steps.map((step, index) => (
          <div
            key={index}
            className={cn(
              "flex items-start gap-2 rounded-lg px-2.5 py-2 text-xs",
              step.status === "in_progress" && "bg-wash",
              step.status === "done" && "text-muted-foreground",
            )}
          >
            {step.status === "done" ? (
              <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-jade" />
            ) : step.status === "in_progress" ? (
              <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin text-jade" />
            ) : (
              <Circle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground/50" />
            )}
            <span
              className={cn(
                "leading-relaxed",
                step.status === "done" && "line-through decoration-border",
              )}
            >
              {step.title}
            </span>
          </div>
        ))}
      </div>
      {planMode ? (
        <div className="space-y-1.5 border-t border-border/50 px-1 pt-2.5">
          <Button
            size="sm"
            className="w-full gap-1.5 text-xs"
            disabled={busy}
            onClick={onConfirm}
          >
            <Play className="h-3.5 w-3.5" /> 执行方案
          </Button>
          <p className="text-2xs leading-relaxed text-muted-foreground">
            计划模式已开启：可在对话中对方案提出修改意见，确认后才会执行修改。
          </p>
        </div>
      ) : null}
    </div>
  );
}
