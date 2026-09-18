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

/** 自定义协议平台的 platform 值前缀(全仓唯一定义处)。 */
export const CUSTOM_PLATFORM_PREFIX = 'custom:'

export function isCustomPlatform(platform: string): platform is `custom:${string}` {
  return platform.trim().toLowerCase().startsWith(CUSTOM_PLATFORM_PREFIX)
}

export function customProtocolID(platform: string): string {
  return isCustomPlatform(platform) ? platform.slice(CUSTOM_PLATFORM_PREFIX.length).trim() : ''
}

export function customPlatformValue(protocolID: string): string {
  return `${CUSTOM_PLATFORM_PREFIX}${protocolID}`
}

export function protocolLabel(format: string, variant: 'short' | 'long' = 'short'): string {
  const value = format.trim()
  if (!value) return ''
  const normalized = value.toLowerCase()
  if (isCustomPlatform(normalized)) {
    const id = customProtocolID(value)
    if (variant === 'long') return id ? `自定义协议 · ${id}` : '自定义协议'
    return id ? `custom·${id}` : 'custom'
  }
  return PROTOCOL_ALIASES[normalized]?.[variant] ?? value
}
