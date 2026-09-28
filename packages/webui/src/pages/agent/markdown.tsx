import { memo, useMemo, type ComponentProps } from "react";
import ReactMarkdown, { type Components, type ExtraProps } from "react-markdown";
import remarkGfm from "remark-gfm";
import { colorize } from "@/lib/json-highlight";
import { ChartBlock } from "./chart-block";
import { Collapse } from "./ui-blocks";
import { parseChartSpec } from "@/lib/agent/chart";

function MarkdownCode({ className, children, node: _node, ...props }: ComponentProps<"code"> & ExtraProps) {
  const raw = String(children ?? "");
  const language = /language-([A-Za-z0-9_-]+)/.exec(className ?? "")?.[1];
  const spec = useMemo(() => language === "chart" ? parseChartSpec(raw) : null, [language, raw]);
  if (spec) return <ChartBlock spec={spec} />;
  if (/language-/.test(className ?? "")) {
    return (
      <pre
        className="max-h-80 overflow-auto rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]"
        dangerouslySetInnerHTML={{ __html: colorize(raw) }}
      />
    );
  }
  return <code className="rounded bg-muted px-1 py-0.5 text-xs" {...props}>{children}</code>;
}

// 组件类型必须稳定：在 Markdown render 内定义会使代码块及图表每次都卸载重建。
const markdownComponents: Components = {
  pre: ({ children }) => <div className="overflow-x-auto text-xs">{children}</div>,
  table: ({ children }) => <div className="overflow-x-auto"><table>{children}</table></div>,
  code: MarkdownCode,
};
const remarkPlugins = [remarkGfm];

/** Markdown 渲染（代码块复用 JSON 高亮，chart 围栏渲染为图表）。 */
export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="agent-markdown space-y-2 break-words text-sm leading-relaxed">
      <ReactMarkdown remarkPlugins={remarkPlugins} components={markdownComponents}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

/** 思维链折叠块。 */
export function ReasoningBlock({ text }: { text: string }) {
  if (!text.trim()) return null;
  return (
    <Collapse
      stopPropagation
      title="已思考"
    >
      <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-muted-foreground">
        {text}
      </p>
    </Collapse>
  );
}
