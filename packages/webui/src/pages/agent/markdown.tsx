import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { colorize } from "@/lib/json-highlight";
import { ChartBlock } from "./chart-block";
import { Collapse } from "./ui-blocks";
import { parseChartSpec } from "@/lib/agent/chart";

/** Markdown 渲染（代码块复用 JSON 高亮，chart 围栏渲染为图表）。 */
export function Markdown({ text }: { text: string }) {
  return (
    <div className="agent-markdown space-y-2 break-words text-sm leading-relaxed">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => (
            <div className="overflow-x-auto text-xs">{children}</div>
          ),
          code: ({ className, children, ...props }) => {
            const raw = String(children ?? "");
            const isBlock = /language-/.test(className ?? "");
            if (isBlock) {
              const language = /language-([A-Za-z0-9_-]+)/.exec(
                className ?? "",
              )?.[1];
              if (language === "chart") {
                const spec = parseChartSpec(raw);
                if (spec) return <ChartBlock spec={spec} />;
              }
              return (
                <pre
                  className="max-h-80 overflow-auto rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]"
                  dangerouslySetInnerHTML={{ __html: colorize(raw) }}
                />
              );
            }
            return (
              <code className="rounded bg-muted px-1 py-0.5 text-xs" {...props}>
                {children}
              </code>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

/** 思维链折叠块。 */
export function ReasoningBlock({ text }: { text: string }) {
  if (!text.trim()) return null;
  return (
    <Collapse
      stopPropagation
      title="思考过程"
      tone="amber"
      icon={<span className="text-amber">💭</span>}
    >
      <p className="whitespace-pre-wrap text-2xs leading-relaxed text-muted-foreground">
        {text}
      </p>
    </Collapse>
  );
}
