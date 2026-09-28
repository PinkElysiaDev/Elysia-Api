import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { compactNumber, formatHitRate, formatNumber } from "@/lib/utils";

const GAUGE_WARN_RATIO = 0.7; // 上下文占用环的黄/绿分界
const GAUGE_DANGER_RATIO = 0.9; // 红/黄分界

export interface SessionUsageStat {
  input: number;
  output: number;
  cached: number;
  total: number;
  hitRate: number | null;
  /** 最近一次调用的输入 token；不能用会话累计用量代替上下文水位。 */
  contextTokens?: number;
}

/** 上下文占用指示器：环形进度 + 悬浮明细（会话累计 tokens / 缓存命中率）。 */
export function ContextGauge({
  usage,
  contextLimit,
}: {
  usage: SessionUsageStat | null;
  contextLimit: number;
}) {
  const hasUsage = !!usage && usage.total > 0;
  const hasLimit = Number.isFinite(contextLimit) && contextLimit > 0;
  const showRing = hasLimit && usage?.contextTokens != null;
  const ratio = showRing ? Math.min(1, usage.contextTokens! / contextLimit) : 0;
  const color =
    ratio >= GAUGE_DANGER_RATIO
      ? "var(--ember)"
      : ratio >= GAUGE_WARN_RATIO
        ? "var(--amber)"
        : "var(--jade)";
  const radius = 6;
  const circumference = 2 * Math.PI * radius;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="会话用量与上下文占用"
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full outline-none transition-colors hover:bg-wash focus-visible:bg-wash"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden>
            <circle
              cx="8"
              cy="8"
              r={radius}
              fill="none"
              strokeWidth="2"
              className="stroke-border"
            />
            {showRing ? (
              <circle
                cx="8"
                cy="8"
                r={radius}
                fill="none"
                stroke={color}
                strokeWidth="2"
                strokeLinecap="round"
                strokeDasharray={circumference}
                strokeDashoffset={circumference * (1 - ratio)}
                transform="rotate(-90 8 8)"
              />
            ) : null}
          </svg>
        </button>
      </TooltipTrigger>
      <TooltipContent className="space-y-0.5 text-2xs">
        {hasUsage ? (
          <>
            <p className="tnum">
              会话累计：↑{compactNumber(usage.input)} ↓
              {compactNumber(usage.output)} tokens
            </p>
            {usage.cached > 0 ? (
              <p className="tnum">
                缓存命中：{compactNumber(usage.cached)}（
                {formatHitRate(usage.hitRate ?? 0)}）
              </p>
            ) : null}
            <p className="tnum">共 {compactNumber(usage.total)} tokens</p>
          </>
        ) : (
          <p>本会话暂无 token 消耗</p>
        )}
        {hasLimit ? (
          <p className="tnum text-muted-foreground">
            模型 MaxTokens：{formatNumber(contextLimit)}
          </p>
        ) : null}
        {showRing ? (
          <p className="tnum text-muted-foreground">
            上下文占用：{formatHitRate(ratio)}
          </p>
        ) : null}
      </TooltipContent>
    </Tooltip>
  );
}
