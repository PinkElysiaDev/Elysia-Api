// SourceFormDialog 的纯逻辑层：表单类型、平台/策略选项与 payload 组装。
import { customPlatformValue } from '@/lib/protocol'
import type {
  ManualModel,
  ModelSource,
  Platform,
  SourceAPIKey,
  SourceKeyStrategy,
} from '@/lib/types'

// 表单态的源：apiKeys/manualModels/keyStrategy 在空态与回填（useEffect）时即
// 归一化为有效值，编辑全程不可能为 undefined，读取侧无需逐处兜底。
export type SourceForm = Omit<ModelSource, 'apiKeys' | 'manualModels' | 'keyStrategy'> & {
  apiKeys: SourceAPIKey[]
  manualModels: ManualModel[]
  keyStrategy: SourceKeyStrategy
}

// 按「线路 API 协议」命名，取代旧的厂商混称（openai/openai-compatible/claude/gemini）。
// 选择 Responses API 表示上游端点类型；默认仍经过 Maheshvara，显式 relay.passthrough
// 才会启用同协议透传。
export const PLATFORMS: { value: string; label: string; hint: string }[] = [
  { value: 'responses', label: 'Responses API', hint: '上游原生 Responses（默认经过 Maheshvara）' },
  { value: 'chat_completions', label: 'Chat Completions API', hint: 'OpenAI 兼容协议，最通用' },
  { value: 'anthropic', label: 'Anthropic API', hint: 'Claude /v1/messages' },
  { value: 'gemini', label: 'Gemini API', hint: 'Gemini /v1beta generateContent' },
]

// 把历史 platform 值归一化到新的四个 apiFormat，使旧源在新下拉里正确回显
// （与后端 NormalizeAPIFormat 保持一致）。
export function normalizePlatform(raw: string | undefined): Platform {
  const normalized = (raw ?? '').trim().toLowerCase()
  if (normalized.startsWith('custom:')) return normalized as Platform
  switch (normalized) {
    case 'responses':
    case 'openai_responses':
      return 'responses'
    case 'anthropic':
    case 'claude':
      return 'anthropic'
    case 'gemini':
    case 'google':
      return 'gemini'
    default:
      // chat_completions / openai / openai-compatible / azure / deepseek / 空
      return 'chat_completions'
  }
}


export const KEY_STRATEGIES: { value: SourceKeyStrategy; label: string; hint: string }[] = [
  { value: 'round-robin', label: '轮询 Round-robin', hint: '每次请求按顺序轮换 Key' },
  { value: 'random', label: '随机 Random', hint: '每次请求随机选取 Key' },
  { value: 'priority', label: '优先级 Priority', hint: '按列表顺序优先，失败先轮换 Key 再换模型' },
]

export function emptySource(): SourceForm {
  return {
    id: '',
    name: '',
    baseUrl: '',
    apiKey: '',
    platform: 'chat_completions',
    enabled: true,
    autoFetchModels: true,
    manualModels: [],
    apiKeys: [],
    keyStrategy: 'round-robin',
  }
}

// 存量兼容：'single'/空策略归一化为「轮询」（单 Key 下三种策略行为完全等价）。
export function normalizeKeyStrategy(raw: string | undefined): SourceKeyStrategy {
  return raw === 'random' || raw === 'priority' ? raw : 'round-robin'
}

/** 手动模式多 key 时,把「模型 ↔ key」选择编译为每个 key 的显式 allowedModels
 * (无 nil 歧义);单 key 或自动模式保持原值。返回错误文案表示校验未过。 */
export function compileApiKeysPayload(
  form: SourceForm,
  manualKeySelection: Record<number, number[]>,
  autoFetch: boolean,
): { keys: SourceAPIKey[] } | { error: string } {
  const keys = form.apiKeys.filter((k) => k.value.trim())
  if (autoFetch || keys.length <= 1) return { keys }
  const allManual = form.manualModels
  for (const [index, model] of allManual.entries()) {
    if (!model.id.trim()) continue
    // 显式空勾选才报错；从未用过勾选面板的模型不参与编译——否则
    // KeyModelsPanel 里手工维护的 per-key allowedModels 会被整体覆盖掉。
    const selection = manualKeySelection[index]
    if (selection !== undefined && selection.length === 0) {
      return { error: `模型「${model.id}」没有任何可用 Key` }
    }
  }
  // 有效 key 在原数组中的下标（manualKeySelection 记录的是原数组下标）。
  const keyOriginalIndexes = form.apiKeys
    .map((k, i) => (k.value.trim() ? i : -1))
    .filter((i) => i >= 0)
  return {
    keys: keyOriginalIndexes.map((originalIndex) => ({
      ...form.apiKeys[originalIndex],
      allowedModels: allManual
        .filter((m, i) => m.id.trim() && (manualKeySelection[i] ?? []).includes(originalIndex))
        .map((m) => m.id.trim()),
    })),
  }
}

/** 组装提交后端的源 payload:平台规范化、手动模型裁剪与拉取地址开关联动。 */
export function buildSourcePayload(
  form: SourceForm,
  resolved: { protocolID: string; autoFetch: boolean; fetchUrlEnabled: boolean; apiKeys: SourceAPIKey[] },
): ModelSource {
  return {
    ...form,
    platform: resolved.protocolID
      ? (customPlatformValue(resolved.protocolID) as ModelSource['platform'])
      : form.platform,
    autoFetchModels: resolved.autoFetch,
    manualModels: resolved.autoFetch ? [] : form.manualModels.filter((m) => m.id || m.name),
    // 关闭「自定义模型拉取地址」时不提交地址（后端空值 = 跟随 baseUrl）。
    fetchBaseUrl: resolved.fetchUrlEnabled ? form.fetchBaseUrl?.trim() ?? '' : '',
    // key 始终走列表（配一个 key 即单 key）；空列表 = 无鉴权源。
    apiKeys: resolved.apiKeys,
  }
}
