import { bashCommandOf } from "@/lib/agent/types";
import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { toolArgsPreview } from "@/lib/agent/mask";
import { cn } from "@/lib/utils";

export type ToolRowStatus = "running" | "done" | "failed" | "denied";

export interface ToolCallEntry {
  id: string;
  seq?: number;
  name: string;
  status: ToolRowStatus;
  input?: unknown;
  result?: unknown;
  summary?: string;
  progressText?: string;
  elapsedMs?: number;
  durationMs?: number;
}

/** 工具组内的紧凑行：命令优先，参数单行省略，按需展开完整输出。 */
export function ToolCallRow({ call }: { call: ToolCallEntry }) {
  const [expanded, setExpanded] = useState(false);
  const parameters = formatParameters(call.name, call.input);
  const output = formatOutput(call.result);
  const failed = call.status === "failed" || call.status === "denied";
  const status = call.status === "running"
    ? "执行中"
    : call.status === "denied" ? "已拒绝" : failed ? "失败" : "";
  const duration = call.durationMs ?? call.elapsedMs;

  return (
    <div data-seq={call.seq} className="min-w-0 text-xs text-muted-foreground">
      <button
        type="button"
        aria-label={`查看 ${call.name} 调用详情`}
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
        className="flex w-full min-w-0 items-start gap-2 rounded-md py-1.5 text-left outline-none transition-colors hover:bg-wash/50 hover:text-foreground focus-visible:bg-wash"
      >
        <ChevronRight
          aria-hidden
          className={cn("mt-1 h-3 w-3 shrink-0 transition-transform", expanded && "rotate-90")}
        />
        <span className="mt-px shrink-0 text-2xs">{call.name}</span>
        <span
          className={cn(
            "min-w-0 flex-1 font-mono leading-relaxed text-foreground/85",
            expanded ? "whitespace-pre-wrap break-words [overflow-wrap:anywhere]" : "truncate",
          )}
        >
          {expanded ? parameters : parameters.replace(/\s+/g, " ")}
        </span>
        {status ? (
          <span className={cn("mt-px shrink-0 text-2xs", failed && "text-ember")}>
            {status}
          </span>
        ) : null}
      </button>
      {expanded ? (
        <div className="space-y-1.5 pb-2 pl-5">
          <div className="flex items-start gap-2 text-2xs">
            <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">
              {call.status === "running"
                ? call.progressText || "正在等待工具返回…"
                : call.summary || (failed ? "执行未完成" : "执行完成")}
            </span>
            {output.exitCode != null && output.exitCode !== 0 ? (
              <span className="shrink-0 text-ember">退出码 {output.exitCode}</span>
            ) : null}
            {duration != null ? (
              <span className="tnum shrink-0">{formatDuration(duration)}</span>
            ) : null}
          </div>
          {call.status !== "running" && (output.text || call.summary) ? (
            <div className="relative rounded-md bg-muted/40">
              <CopyButton
                value={output.text || call.summary || ""}
                aria-label="复制工具结果"
                className="absolute right-1.5 top-1.5 z-10 h-6 w-6 bg-background/80"
              />
              <pre className="max-h-64 overflow-y-auto whitespace-pre-wrap break-words px-3 py-2 pr-10 font-mono text-2xs leading-relaxed [overflow-wrap:anywhere]">
                {output.text || call.summary}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function formatDuration(ms: number): string {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

function formatParameters(name: string, input: unknown): string {
  const safe = toolArgsPreview(input);
  if (!safe) return "无参数记录";
  if (typeof input !== "object" || input == null || Array.isArray(input)) return safe;
  // 沿用参数脱敏，bash 直接展示命令，其余参数使用紧凑的 key=value。
  const fields = JSON.parse(safe) as Record<string, unknown>;
  const command = bashCommandOf(name, fields) ?? "";
  if (command) delete fields.command;
  const rest = Object.entries(fields).map(([key, value]) => `${key}=${JSON.stringify(value)}`).join("  ");
  return [command, rest].filter(Boolean).join("\n") || "无参数";
}

/** CLI 输出保留真实换行；额外结果字段完整保留，避免隐藏错误上下文。 */
function formatOutput(result: unknown): { text: string; exitCode?: number } {
  if (result == null) return { text: "" };
  if (typeof result === "string") return { text: result };
  if (typeof result === "object" && !Array.isArray(result)) {
    const fields = result as Record<string, unknown>;
    if (typeof fields.output === "string") {
      const { output, exitCode, ...extra } = fields;
      // 非数字 exitCode 仍放回结果，不能无声丢掉未知字段。
      if (exitCode != null && typeof exitCode !== "number") extra.exitCode = exitCode;
      return {
        text: [output, Object.keys(extra).length ? JSON.stringify(extra, null, 2) : ""].filter(Boolean).join("\n\n"),
        exitCode: typeof exitCode === "number" ? exitCode : undefined,
      };
    }
  }
  return { text: JSON.stringify(result, null, 2) ?? "" };
}
