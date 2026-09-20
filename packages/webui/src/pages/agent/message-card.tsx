import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  AlertTriangle,
  Bot,
  ChevronDown,
  FileText,
  Pencil,
  RefreshCw,
  ShieldCheck,
  Wrench,
  X,
} from 'lucide-react'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { colorize } from '@/lib/json-highlight'
import { ChartBlock } from './chart-block'
import { parseChartSpec } from '@/lib/agent/chart'
import { toolArgsPreview } from '@/lib/agent/mask'
import type { AgentLiveState } from '@/lib/agent/use-agent-stream'
import {
  agentToolLabel,
  formatUsage,
  type AgentApprovalContent,
  type AgentAssistantContent,
  type AgentMessage,
  type AgentSystemContent,
  type AgentToolResultContent,
  type AgentUserContent,
} from '@/lib/agent/types'
import { cn } from '@/lib/utils'

/** 折叠块（思维链 / 工具结果共用）。 */
function Collapse({
  title,
  icon,
  children,
  tone = 'muted',
}: {
  title: string
  icon?: React.ReactNode
  children: React.ReactNode
  tone?: 'muted' | 'amber'
}) {
  const [open, setOpen] = useState(false)
  return (
    <div
      className={cn(
        'rounded-md border text-xs',
        tone === 'amber' ? 'border-amber/30 bg-amber/5' : 'border-border bg-muted/40',
      )}
    >
      <button
        type="button"
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-muted-foreground hover:text-foreground"
        onClick={() => setOpen((value) => !value)}
      >
        {icon}
        <span className="flex-1 truncate">{title}</span>
        <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', open && 'rotate-180')} />
      </button>
      {open && <div className="border-t border-border/60 px-2.5 py-2">{children}</div>}
    </div>
  )
}

function JsonBlock({ value }: { value: unknown }) {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? ''
  return (
    <pre
      className="max-h-72 overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]"
      dangerouslySetInnerHTML={{ __html: colorize(text) }}
    />
  )
}

/** Markdown 渲染（代码块复用 JSON 高亮，chart 围栏渲染为图表）。 */
function Markdown({ text }: { text: string }) {
  return (
    <div className="agent-markdown space-y-2 break-words text-sm leading-relaxed">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => <div className="overflow-x-auto text-xs">{children}</div>,
          code: ({ className, children, ...props }) => {
            const raw = String(children ?? '')
            const isBlock = /language-/.test(className ?? '')
            if (isBlock) {
              const language = /language-([A-Za-z0-9_-]+)/.exec(className ?? '')?.[1]
              if (language === 'chart') {
                const spec = parseChartSpec(raw)
                if (spec) return <ChartBlock spec={spec} />
              }
              return (
                <pre
                  className="max-h-80 overflow-auto rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]"
                  dangerouslySetInnerHTML={{ __html: colorize(raw) }}
                />
              )
            }
            return (
              <code className="rounded bg-muted px-1 py-0.5 text-xs" {...props}>
                {children}
              </code>
            )
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  )
}

/** 思维链折叠块。 */
function ReasoningBlock({ text }: { text: string }) {
  if (!text.trim()) return null
  return (
    <Collapse title="思考过程" tone="amber" icon={<span className="text-amber">💭</span>}>
      <p className="whitespace-pre-wrap text-2xs leading-relaxed text-muted-foreground">{text}</p>
    </Collapse>
  )
}

/** 工具结果卡片（持久化消息形态）。 */
function ToolResultCard({ content }: { content: AgentToolResultContent }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-1.5 text-xs">
        <Wrench className="h-3.5 w-3.5 text-muted-foreground/60" />
        <span className="font-medium">{content.name}</span>
        <span className={cn('text-2xs', content.ok ? 'text-jade' : 'text-ember')}>{content.ok ? '成功' : '失败'}</span>
        {content.durationMs ? <span className="tnum text-2xs text-muted-foreground">{content.durationMs}ms</span> : null}
      </div>
      {content.summary ? <p className="text-2xs text-muted-foreground">{content.summary}</p> : null}
      {content.data != null ? (
        <Collapse title="结果详情">
          <JsonBlock value={content.data} />
        </Collapse>
      ) : null}
    </div>
  )
}

export interface MessageActions {
  onRetry: (message: AgentMessage) => void
  onEditResend: (message: AgentMessage) => void
  onRegenerate: (message: AgentMessage) => void
}

/** 持久化消息卡片。 */
export function MessageCard({
  message,
  actions,
}: {
  message: AgentMessage
  actions?: MessageActions
}) {
  if (message.role === 'user') {
    const content = message.content as AgentUserContent
    const text = content.text ?? ''
    return (
      <div className="group flex flex-col items-end gap-1">
        <div className="max-w-[85%] space-y-1.5 rounded-2xl rounded-br-md bg-wash px-4 py-2.5 text-sm">
          {text ? <p className="whitespace-pre-wrap break-words">{text}</p> : null}
          {(content.documents ?? []).length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {(content.documents ?? []).map((doc, index) => (
                <span
                  key={index}
                  className="inline-flex items-center gap-1 rounded-md bg-card px-1.5 py-0.5 text-2xs text-muted-foreground"
                >
                  <FileText className="h-3 w-3" />
                  {doc.name ?? `材料 ${index + 1}`}
                </span>
              ))}
            </div>
          ) : null}
        </div>
        {actions ? (
          <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
            <CopyButton value={text} aria-label="复制" />
            <Button variant="ghost" size="iconSm" aria-label="编辑后重发" title="编辑后重发" onClick={() => actions.onEditResend(message)}>
              <Pencil className="h-3.5 w-3.5" />
            </Button>
            <Button variant="ghost" size="iconSm" aria-label="原样重试" title="原样重试" onClick={() => actions.onRetry(message)}>
              <RefreshCw className="h-3.5 w-3.5" />
            </Button>
          </div>
        ) : null}
      </div>
    )
  }

  if (message.role === 'assistant') {
    const content = message.content as AgentAssistantContent
    return (
      <div className="group flex items-start gap-2.5">
        <Bot className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground/50" aria-hidden />
        <div className="min-w-0 flex-1 space-y-2">
          <ReasoningBlock text={content.reasoning ?? ''} />
          {content.text ? <Markdown text={content.text} /> : null}
          {(content.toolCalls ?? []).length > 0 ? (
            <div className="flex flex-wrap gap-1.5 pt-0.5 text-2xs text-muted-foreground">
              {(content.toolCalls ?? []).map((call, index) => (
                <span key={index} className="inline-flex items-center gap-1 text-muted-foreground/70">
                  <Wrench className="h-3 w-3" />
                  {call.name ?? '工具'}
                </span>
              ))}
            </div>
          ) : null}
          <div className="flex items-center gap-1.5 pt-0.5 text-2xs text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100">
            {message.model ? <span>{message.model}</span> : null}
            {message.usage ? <span>{formatUsage(message.usage)}</span> : null}
            {content.text ? <CopyButton value={content.text} aria-label="复制" /> : null}
            {actions ? (
              <Button variant="ghost" size="iconSm" aria-label="重新生成本条回复" title="重新生成本条回复" onClick={() => actions.onRegenerate(message)}>
                <RefreshCw className="h-3 w-3" />
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    )
  }

  if (message.role === 'tool_result') {
    return (
      <div className="max-w-[92%] border-l-2 border-border/60 pl-3">
        <ToolResultCard content={message.content as AgentToolResultContent} />
      </div>
    )
  }

  if (message.role === 'approval') {
    const content = message.content as AgentApprovalContent
    return (
      <div className="flex items-center gap-1.5 self-center rounded-full border border-border bg-muted/40 px-3 py-1 text-2xs text-muted-foreground">
        <ShieldCheck className={cn('h-3.5 w-3.5', content.decision === 'approved' ? 'text-jade' : 'text-ember')} />
        {content.decision === 'approved' ? '已允许' : '已拒绝'}：{content.names?.join('、') ?? ''}
      </div>
    )
  }

  const content = message.content as AgentSystemContent
  return (
    <div
      className={cn(
        'flex items-start gap-1.5 self-center rounded-md px-3 py-1.5 text-2xs',
        content?.kind === 'error'
          ? 'border-[color-mix(in_srgb,var(--ember)_35%,transparent)] bg-[color-mix(in_srgb,var(--ember)_7%,transparent)] text-ember'
          : 'bg-muted/50 text-muted-foreground',
      )}
    >
      {content?.kind === 'error' ? <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" /> : null}
      <span className="line-clamp-3">{content?.text ?? ''}</span>
    </div>
  )
}

/** 进行中的现场气泡（流式增量 + 工具卡片）。 */
export function LiveAssistantView({ live }: { live: AgentLiveState }) {
  const hasContent = live.text || live.reasoning
  return (
    <div className="flex flex-col gap-2">
      {hasContent ? (
        <div className="flex items-start gap-2.5">
          <Bot className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground/50" aria-hidden />
          <div className="min-w-0 flex-1 space-y-2">
            <ReasoningBlock text={live.reasoning} />
            {live.text ? <Markdown text={live.text} /> : null}
          </div>
        </div>
      ) : null}
      {live.toolCards.map((card) => (
        <div
          key={card.callId}
          className="flex max-w-[92%] items-center gap-1.5 border-l-2 border-border/60 pl-3 text-xs"
        >
          <Wrench className="h-3.5 w-3.5 text-muted-foreground/60" />
          <span className="font-medium">{agentToolLabel(card.name)}</span>
          {card.status === 'running' ? (
            <span className="ml-auto flex items-center gap-1 text-2xs text-muted-foreground">
              <RefreshCw className="h-3 w-3 animate-spin" /> 执行中…
            </span>
          ) : (
            <span className={cn('ml-auto text-2xs', card.status === 'done' ? 'text-jade' : 'text-ember')}>
              {card.summary ?? (card.status === 'done' ? '完成' : '失败')}
            </span>
          )}
        </div>
      ))}
    </div>
  )
}

export function ApprovalCard({
  approval,
  onApprove,
  onDeny,
  busy,
}: {
  approval: { calls: { name?: string; arguments?: unknown }[]; reason?: string }
  onApprove: () => void
  onDeny: () => void
  busy?: boolean
}) {
  const argsPreview = approval.calls
    .map((call) => toolArgsPreview(call.arguments))
    .filter(Boolean)
    .join('\n')
  return (
    <div className="w-[92%] space-y-2.5 rounded-xl border-[color-mix(in_srgb,var(--amber)_35%,transparent)] bg-[color-mix(in_srgb,var(--amber)_7%,transparent)] px-3.5 py-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <ShieldCheck className="h-4 w-4 text-amber" />
        请求批准：{approval.calls.map((call) => agentToolLabel(call.name ?? '')).join('、')}
      </div>
      {approval.reason ? (
        <p className="line-clamp-4 text-xs text-muted-foreground">助手说明：{approval.reason}</p>
      ) : null}
      {argsPreview ? (
        <div className="rounded-lg border border-border/70 bg-card px-3 py-2">
          <p className="mb-1.5 text-2xs font-medium text-muted-foreground">执行参数</p>
          <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-2xs leading-relaxed">{argsPreview}</pre>
        </div>
      ) : null}
      <div className="flex items-center gap-2">
        <Button size="sm" disabled={busy} onClick={onApprove}>
          允许
        </Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onDeny}>
          <X className="h-3.5 w-3.5" /> 拒绝
        </Button>
        <span className="text-2xs text-muted-foreground">拒绝后助手会调整方案继续</span>
      </div>
    </div>
  )
}
