import { memo, useMemo, type ComponentProps } from "react";
import ReactMarkdown, { type Components, type ExtraProps } from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkBreaks from "remark-breaks";
import { colorize } from "@/lib/json-highlight";
import { highlightCode } from "@/lib/prism-highlight";
import { CopyButton } from "@/components/copy-button";
import { ChartBlock } from "./chart-block";
import { Collapse } from "./ui-blocks";
import { parseChartSpec } from "@/lib/agent/chart";

function MarkdownCode({ className, children, node: _node, ...props }: ComponentProps<"code"> & ExtraProps) {
  const raw = String(children ?? "");
  const language = /language-([A-Za-z0-9_-]+)/.exec(className ?? "")?.[1];
  const spec = useMemo(() => language === "chart" ? parseChartSpec(raw) : null, [language, raw]);
  if (spec) return <ChartBlock spec={spec} />;
  if (/language-/.test(className ?? "")) {
    // JSON 走既有 colorize（观感不变），其余语言用 Prism token（配色见
    // index.css 的 .agent-markdown .token 规则）；块级 code 自带完整容器，
    // 外层 pre 由 pre 覆写透传。
    const html = useMemo(
      () => (language === "json" || language === "" ? colorize(raw) : highlightCode(raw, language ?? "")),
      [language, raw],
    );
    return (
      <div className="overflow-hidden rounded-[7px] border border-border bg-code">
        <div className="flex items-center justify-between border-b border-border/60 pl-3 pr-1">
          <span className="font-mono text-2xs tracking-wide text-muted-foreground/80">{language || "text"}</span>
          <CopyButton value={raw} className="h-6 w-6" />
        </div>
        <pre
          className="max-h-80 overflow-auto px-3 py-2.5 font-mono text-2xs leading-[1.7]"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      </div>
    );
  }
  return (
    <code className="rounded-[5px] border border-border/60 bg-muted/60 px-1.5 py-px font-mono text-[0.85em]" {...props}>
      {children}
    </code>
  );
}

// 组件类型必须稳定：在 Markdown render 内定义会使代码块及图表每次都卸载重建。
const markdownComponents: Components = {
  pre: ({ children }) => <>{children}</>,
  table: ({ children }) => (
    <div className="overflow-x-auto">
      <table>{children}</table>
    </div>
  ),
  code: MarkdownCode,
  a: ({ children, href }) => (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  ),
  img: ({ src, alt }) => (
    <img src={src} alt={alt} loading="lazy" decoding="async" className="max-h-96 max-w-full rounded-lg border border-border/70" />
  ),
};
const remarkPlugins = [remarkGfm, remarkBreaks];

/** 流式渲染前补齐未配对围栏：流到一半的 ``` 块按 micromark 语义会吞掉后续
 * 一切内容直到 EOF，补一个闭合围栏让未完代码块先以代码块形态呈现。 */
export function closeUnbalancedFences(text: string): string {
  const fences = text.match(/^[ \t]*(?:```|~~~)/gm) ?? [];
  return fences.length % 2 === 1 ? text + "\n```" : text;
}

/** Markdown 渲染：GFM + 单换行成行（与用户侧 pre-wrap 对齐）、ChatGPT 式代码
 * 块头栏（语言 + 复制）、多语言语法高亮，chart 围栏渲染为图表。 */
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
