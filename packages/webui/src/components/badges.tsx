import { Badge } from './ui/badge'
import type { Platform, ModelType, GroupStrategy } from '@/lib/types'

const PLATFORM_LABEL: Record<string, string> = {
  // 新的 apiFormat 命名
  responses: 'Responses API',
  chat_completions: 'Chat Completions API',
  anthropic: 'Anthropic API',
  gemini: 'Gemini API',
  // relay FormatType 值（usage 日志的 sourceFormat/targetFormat 用这套）
  openai_responses: 'Responses API',
  openai: 'Chat Completions API',
  claude: 'Anthropic API',
  // 旧值向后兼容（库里可能仍是这些）
  'openai-compatible': 'Chat Completions API',
}

export function PlatformBadge({ platform }: { platform: Platform | string }) {
  if (platform.toLowerCase().startsWith('custom:')) {
    return <Badge variant="outline">Custom · {platform.slice('custom:'.length)}</Badge>
  }
  return <Badge variant="outline">{PLATFORM_LABEL[platform] ?? platform}</Badge>
}

const TYPE_LABEL: Record<string, string> = {
  llm: 'LLM',
  embedding: 'Embedding',
  reranker: 'Reranker',
}

export function ModelTypeBadge({ type }: { type: ModelType | string }) {
  return <Badge variant="secondary">{TYPE_LABEL[type] ?? type}</Badge>
}

export function EnabledBadge({ enabled }: { enabled: boolean }) {
  return enabled ? <Badge variant="success">已启用</Badge> : <Badge variant="muted">已停用</Badge>
}

export function StatusCodeBadge({ code }: { code: number }) {
  if (code === 0) return <Badge variant="muted">—</Badge>
  if (code >= 200 && code < 400) return <Badge variant="success">{code}</Badge>
  if (code >= 400 && code < 500) return <Badge variant="destructive">{code}</Badge>
  return <Badge variant="destructive">{code}</Badge>
}

const STRATEGY_LABEL: Record<GroupStrategy, string> = {
  'round-robin': '轮询',
  sequential: '顺序',
  random: '随机',
}

export function StrategyBadge({ strategy }: { strategy: GroupStrategy | string }) {
  return <Badge variant="outline">{STRATEGY_LABEL[strategy as GroupStrategy] ?? strategy}</Badge>
}
