/** 协议短名（表格徽标）与长名（详情）共用一份别名表。 */

type ProtocolAlias = { short: string; long: string }

const PROTOCOL_ALIASES: Record<string, ProtocolAlias> = {
  responses: { short: 'responses', long: 'Responses API' },
  openai_responses: { short: 'responses', long: 'Responses API' },
  'openai-responses': { short: 'responses', long: 'Responses API' },
  chat_completions: { short: 'chat_cmpl', long: 'Chat Completions API' },
  openai: { short: 'chat_cmpl', long: 'Chat Completions API' },
  'openai-compatible': { short: 'chat_cmpl', long: 'Chat Completions API' },
  azure: { short: 'azure', long: 'Chat Completions API' },
  deepseek: { short: 'deepseek', long: 'Chat Completions API' },
  anthropic: { short: 'anthropic', long: 'Anthropic API' },
  claude: { short: 'anthropic', long: 'Anthropic API' },
  gemini: { short: 'gemini', long: 'Gemini API' },
  google: { short: 'google', long: 'Gemini API' },
}

export function protocolLabel(format: string, variant: 'short' | 'long' = 'short'): string {
  const value = format.trim()
  if (!value) return ''
  const normalized = value.toLowerCase()
  if (normalized.startsWith('custom:')) {
    const id = value.slice('custom:'.length).trim()
    if (variant === 'long') return id ? `自定义协议 · ${id}` : '自定义协议'
    return id ? `custom·${id}` : 'custom'
  }
  return PROTOCOL_ALIASES[normalized]?.[variant] ?? value
}
