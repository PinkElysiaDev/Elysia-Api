import { Check, Circle, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

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
  const done = steps.filter((step) => step.status === "done").length;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="no-scrollbar min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain px-1 pb-4">
        {summary ? (
          <div className="rounded-xl bg-wash px-3 py-3">
            <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground/90">
              {summary}
            </p>
          </div>
        ) : null}
        {steps.length > 0 ? (
          <div className="space-y-2 px-1">
            <div className="flex items-center justify-between text-2xs text-muted-foreground">
              <span>{done === steps.length ? "全部完成" : "任务进度"}</span>
              <span className="tnum">{done} / {steps.length}</span>
            </div>
            <div role="progressbar" aria-label="方案完成进度" aria-valuemin={0} aria-valuemax={steps.length} aria-valuenow={done} className="h-1 overflow-hidden rounded-full bg-muted">
              <div className="h-full rounded-full bg-primary/60 transition-[width] duration-300 motion-reduce:transition-none" style={{ width: `${done / steps.length * 100}%` }} />
            </div>
          </div>
        ) : null}
        <ol className="space-y-1">
        {steps.map((step, index) => (
          <li
            key={index}
            className={cn(
              "flex items-start gap-3 rounded-xl px-2.5 py-3 text-xs",
              step.status === "in_progress" && "bg-wash",
              step.status === "done" && "text-muted-foreground",
            )}
          >
            {step.status === "done" ? (
              <Check aria-label="已完成" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
            ) : step.status === "in_progress" ? (
              <span aria-label="进行中" className="tnum mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-primary/10 text-[10px] font-medium text-primary">{index + 1}</span>
            ) : (
              <Circle aria-label="待开始" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground/40" />
            )}
            <span
              className={cn(
                "leading-relaxed",
                step.status === "in_progress" && "font-medium",
              )}
            >
              {step.title}
            </span>
          </li>
        ))}
        </ol>
      </div>
      {planMode && steps.length > 0 ? (
        <div className="space-y-1.5 border-t border-border/50 px-1 pt-2.5">
          <Button
            size="sm"
            variant="primary"
            className="w-full gap-1.5 text-xs focus-visible:ring-0 focus-visible:ring-offset-0 focus-visible:brightness-110"
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
