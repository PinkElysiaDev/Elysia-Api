import { useEffect, useState } from "react";
import { ChevronDown, Wrench, X } from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { agentToolLabel, toolStatusVerb } from "@/lib/agent/types";
import { cn } from "@/lib/utils";

const ELAPSED_TICK_MS = 500; // 运行中本地计时刷新间隔

export type ToolRowStatus = "running" | "done" | "failed" | "denied";

/**
 * 工具执行行：扁平竖线样式，一行说清动作、状态动词和耗时。
 * 运行中用品牌渐变文字；完成后自动可折叠，失败保持展开并提供复制。
 */
export function ToolCallRow({
  name,
  status,
  summary,
  command,
  elapsedMs,
  startedAt,
  durationMs,
  detail,
  onOpen,
}: {
  name: string;
  status: ToolRowStatus;
  summary?: string;
  /** bash 工具的命令回显（出口已脱敏）。 */
  command?: string;
  elapsedMs?: number;
  startedAt?: number;
  durationMs?: number;
  detail?: React.ReactNode;
  onOpen?: () => void;
}) {
  const [open, setOpen] = useState(status === "failed" || status === "denied");
  // live 卡以 running 挂载、状态随后翻转：失败/被拒时要主动展开（初始值只
  // 在挂载时生效，覆盖不到状态变化）。
  useEffect(() => {
    if (status === "failed" || status === "denied") setOpen(true);
  }, [status]);
  const elapsed = useElapsed(status === "running", startedAt, elapsedMs);
  const verb = toolStatusVerb(status);
  const timing =
    status === "running"
      ? formatElapsed(elapsed)
      : durationMs
        ? `${durationMs}ms`
        : "";

  return (
    <div className="max-w-tool border-l-2 border-border/60 pl-3">
      <div className="flex items-center gap-1.5 text-xs">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
          aria-expanded={detail ? open : undefined}
          onClick={() => {
            if (detail) setOpen((value) => !value);
            onOpen?.();
          }}
        >
          <Wrench className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
          <span className="truncate font-medium">{agentToolLabel(name)}</span>
          <span
            className={cn(
              "shrink-0",
              status === "running" ? "tool-running-text" : statusTone(status),
            )}
          >
            {verb}
          </span>
          {timing ? (
            <span className="tnum shrink-0 text-2xs text-muted-foreground">
              {timing}
            </span>
          ) : null}
          {detail ? (
            <ChevronDown
              className={cn(
                "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                open && "rotate-180",
              )}
            />
          ) : null}
        </button>
        {status === "failed" || status === "denied" ? (
          <CopyButton
            value={summary ?? verb}
            aria-label="复制错误"
            className="h-5 w-5"
          />
        ) : null}
        {status === "failed" || status === "denied" ? (
          <X className="h-3 w-3 shrink-0 text-ember" aria-hidden />
        ) : null}
      </div>
      {command ? (
        <p className="mt-1 line-clamp-2 truncate font-mono text-2xs text-muted-foreground">
          $ {command}
        </p>
      ) : null}
      {summary && status !== "running" ? (
        <p className="mt-1 line-clamp-2 text-2xs text-muted-foreground">
          {summary}
        </p>
      ) : null}
      {open && detail ? <div className="mt-1.5">{detail}</div> : null}
    </div>
  );
}

function statusTone(status: ToolRowStatus): string {
  if (status === "done") return "text-jade";
  if (status === "failed" || status === "denied") return "text-ember";
  return "text-muted-foreground";
}

function formatElapsed(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

/** 运行中优先本地计时（平滑走秒）；无 startedAt 时退回服务端心跳值。 */
function useElapsed(
  running: boolean,
  startedAt: number | undefined,
  elapsedMs: number | undefined,
): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running || !startedAt) return;
    const timer = window.setInterval(() => setNow(Date.now()), ELAPSED_TICK_MS);
    return () => window.clearInterval(timer);
  }, [running, startedAt]);
  if (running && startedAt) return Math.max(0, now - startedAt);
  return elapsedMs ?? 0;
}
