import { useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import { colorize } from "@/lib/json-highlight";

/** 消息流与任务资料共用的文本折叠区块。 */
export function Collapse({
  title,
  children,
  stopPropagation = false,
  summary,
}: {
  title: string;
  children: ReactNode;
  /** 嵌在可点击卡片内时阻止事件冒泡（消息卡里的工具详情块）。 */
  stopPropagation?: boolean;
  summary?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="text-sm">
      <button
        type="button"
        aria-expanded={open}
        className="flex w-fit items-center gap-1.5 rounded py-1 text-left text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={(event) => {
          if (stopPropagation) event.stopPropagation();
          setOpen((value) => !value);
        }}
      >
        <span className="truncate">{title}</span>
        <ChevronRight
          className={cn(
            "h-3.5 w-3.5 transition-transform",
            open && "rotate-90",
          )}
        />
        {summary}
      </button>
      {open && (
        <div className="mt-1 border-l border-border/60 pl-3">
          {children}
        </div>
      )}
    </div>
  );
}

/** JSON/文本展示块：语法高亮 + 可控最大高度。 */
export function JsonBlock({
  value,
  maxHeight = "max-h-72",
}: {
  value: unknown;
  maxHeight?: string;
}) {
  const text =
    typeof value === "string" ? value : (JSON.stringify(value, null, 2) ?? "");
  return (
    <pre
      className={cn(
        "overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]",
        maxHeight,
      )}
      dangerouslySetInnerHTML={{ __html: colorize(text) }}
    />
  );
}
