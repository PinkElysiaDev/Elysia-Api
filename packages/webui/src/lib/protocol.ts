import type { EnabledProtocol } from './protocol-v2'

type ProtocolName = Pick<EnabledProtocol, 'id' | 'name'>

/** 旧日志中的线路别名只归一到协议 ID，名称由注册定义提供。 */
const LEGACY_PROTOCOL_IDS = new Map(Object.entries({
  responses: 'openai-responses',
  openai_responses: 'openai-responses',
  chat_completions: 'openai-chat-completions',
  openai: 'openai-chat-completions',
  openai_chat: 'openai-chat-completions',
  'openai-compatible': 'openai-chat-completions',
  azure: 'openai-chat-completions',
  deepseek: 'openai-chat-completions',
  anthropic: 'anthropic-messages',
  claude: 'anthropic-messages',
  gemini: 'google-generate-content',
  google: 'google-generate-content',
  'chat-completions-api': 'openai-chat-completions',
  'responses-api': 'openai-responses',
  'anthropic-api': 'anthropic-messages',
  'gemini-api': 'google-generate-content',
}))

/** 表格徽标的紧凑缩写，不用于协议名称或身份判断。 */
const SHORT_LABELS = new Map(Object.entries({
  'openai-chat-completions': 'chat_cmpl',
  'openai-responses': 'responses',
  'anthropic-messages': 'anthropic',
  'google-generate-content': 'gemini',
  azure: 'azure',
  deepseek: 'deepseek',
  google: 'google',
}))

/** 自定义协议平台的 platform 值前缀(全仓唯一定义处)。 */
const CUSTOM_PLATFORM_PREFIX = 'custom:'

export function isCustomPlatform(platform: string): platform is `custom:${string}` {
  return platform.trim().toLowerCase().startsWith(CUSTOM_PLATFORM_PREFIX)
}

export function customProtocolID(platform: string): string {
  return isCustomPlatform(platform) ? platform.trim().slice(CUSTOM_PLATFORM_PREFIX.length).trim() : ''
}

export function customPlatformValue(protocolID: string): string {
  return `${CUSTOM_PLATFORM_PREFIX}${protocolID}`
}

export function protocolID(format: string, protocols: readonly ProtocolName[] = []): string {
  const value = format.trim()
  const id = isCustomPlatform(value) ? customProtocolID(value) : value
  if (protocols.some((protocol) => protocol.id === id)) return id
  return LEGACY_PROTOCOL_IDS.get(id.toLowerCase()) ?? id
}

export function protocolLabel(format: string, variant: 'short' | 'long' = 'short', protocols: readonly ProtocolName[] = []): string {
  const id = protocolID(format, protocols)
  if (variant === 'long') return protocols.find((protocol) => protocol.id === id)?.name?.trim() || id
  return SHORT_LABELS.get(format.trim().toLowerCase()) ?? SHORT_LABELS.get(id) ?? id
}
