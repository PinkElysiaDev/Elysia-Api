/** 协议短名（表格徽标）与长名（详情）共用一份别名表。 */

type ProtocolAlias = { short: string; long: string }

const PROTOCOL_ALIASES: Record<string, ProtocolAlias> = {
  responses: { short: 'responses', long: 'Responses API' },
  openai_responses: { short: 'responses', long: 'Responses API' },
  'openai-responses': { short: 'responses', long: 'Responses API' },
  chat_completions: { short: 'chat_cmpl', long: 'Chat Completions API' },
  openai: { short: 'chat_cmpl', long: 'Chat Completions API' },
  openai_chat: { short: 'chat_cmpl', long: 'Chat Completions API' },
  'openai-compatible': { short: 'chat_cmpl', long: 'Chat Completions API' },
  azure: { short: 'azure', long: 'Chat Completions API' },
  deepseek: { short: 'deepseek', long: 'Chat Completions API' },
  anthropic: { short: 'anthropic', long: 'Anthropic API' },
  claude: { short: 'anthropic', long: 'Anthropic API' },
  gemini: { short: 'gemini', long: 'Gemini API' },
  google: { short: 'google', long: 'Gemini API' },
}

/**
 * 四条预置协议与其内置线路线缆等价（shape 一一对应），显示与内置线一致的
 * 规范名——usage 链路里「Gemini API → custom:gemini-api」这类同线制对两端
 * 同名、不再呈现为转换。
 */
const PRESET_PROTOCOL_ALIASES: Record<string, ProtocolAlias> = {
  'chat-completions-api': { short: 'chat_cmpl', long: 'Chat Completions API' },
  'responses-api': { short: 'responses', long: 'Responses API' },
  'anthropic-api': { short: 'anthropic', long: 'Anthropic API' },
  'gemini-api': { short: 'gemini', long: 'Gemini API' },
}

/** 非预置自定义协议的注册名（listCustomProtocols 拉取后注入；键为协议 id）。 */
const customProtocolNames = new Map<string, string>()

/** 注入自定义协议注册名（应用初始化或协议列表加载后调用；幂等全量替换）。 */
export function setCustomProtocolDisplayNames(names: Record<string, string>) {
  customProtocolNames.clear()
  for (const [id, name] of Object.entries(names)) {
    if (id && name) customProtocolNames.set(id.trim().toLowerCase(), name)
  }
}

/** 自定义协议平台的 platform 值前缀(全仓唯一定义处)。 */
const CUSTOM_PLATFORM_PREFIX = 'custom:'

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
    const id = customProtocolID(value).toLowerCase()
    if (!id) return '自定义协议'
    const preset = PRESET_PROTOCOL_ALIASES[id]
    if (preset) return preset[variant]
    const registered = customProtocolNames.get(id)
    if (registered) return variant === 'long' ? registered : `custom·${id}`
    return variant === 'long' ? `自定义协议 · ${id}` : `custom·${id}`
  }
  return PROTOCOL_ALIASES[normalized]?.[variant] ?? value
}
