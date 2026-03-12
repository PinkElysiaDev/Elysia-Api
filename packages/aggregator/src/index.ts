import { Context, Service } from 'koishi'
import { Model, ModelType, ModelSource } from '@elysia-api/shared'
import { ModelFetcher } from './model-fetcher'
import {
  Config,
  name,
  AggregatorSourceConfig,
  ManualAggregatorSource,
} from './config'

export { Config, name }

export const usage = `---

## 使用说明

本插件用于自动获取和管理可用的 AI 模型，支持 OpenAI、Claude、Gemini 等平台。

### 配置步骤

1. **添加源**: 在「添加源」中配置 API 端点、API Key 与平台
2. **自动/手动模式**: 保持「是否自动拉取模型」为是，或切换为否后手动填写模型
3. **重新加载**: 配置完成后使用 \`elysia-api.models.reload\` 命令生效

### 自动拉取说明

- OpenAI / OpenAI-compatible：通过模型列表接口自动拉取
- Gemini：通过 Google Gemini 模型列表接口自动拉取
- Claude：优先尝试远程拉取，失败时回退到内置模型列表，并打印警告日志

---

`

// 服务类 - 提供给其他插件使用
// 继承 Service 类以支持 Koishi 的依赖注入机制
export class AggregatorService extends Service {
  private models: Model[] = []

  constructor(public ctx: Context, public config: Config) {
    super(ctx, 'elysia-api-aggregator')
  }

  getAll(): Model[] {
    return this.models
  }

  getById(id: string): Model | undefined {
    return this.models.find(m => m.id === id)
  }

  getByType(type: ModelType): Model[] {
    return this.models.filter(m => m.type === type)
  }

  // 更新模型列表
  updateModels(newModels: Model[]) {
    this.models.length = 0
    this.models.push(...newModels)
  }
}

// Context extension declaration
declare module 'koishi' {
  interface Context {
    // 服务注入 - 使 orchestrator 的 inject 依赖能够正常工作
    'elysia-api-aggregator': AggregatorService
    // 向后兼容
    elysiaApi?: {
      models: {
        getAll(): Model[]
        getById(id: string): Model | undefined
        getByType(type: ModelType): Model[]
      }
    }
  }

  interface Events {
    'elysia-api/models-updated': (models: Model[]) => void
  }
}

export function apply(ctx: Context, config: Config) {
  // 注册服务 - 使用手动注册模式（参考 multi-bot-controller）
  // 创建服务实例并直接赋值给 context
  const service = new AggregatorService(ctx, config)
  ctx['elysia-api-aggregator'] = service

  // Initialize services
  const fetcher = new ModelFetcher(ctx)

  // Provide models service to context（保持向后兼容）
  ctx.elysiaApi = {
    models: {
      getAll: () => service.getAll(),
      getById: (id: string) => service.getById(id),
      getByType: (type: 'llm' | 'embedding' | 'reranker') =>
        service.getByType(type),
    },
  }

  let isLoading = false
  let pendingReload = false
  let lastModelsHash = ''
  let lastConfigHash = ''

  const buildConfigHash = () => {
    return JSON.stringify({
      sources: config.sources,
    })
  }

  const buildModelsHash = (models: Model[]) => {
    const normalized = [...models]
      .map(m => ({
        id: m.id,
        name: m.name,
        source: m.source,
        sourceName: m.sourceName,
        baseUrl: m.baseUrl,
        platform: m.platform,
        type: m.type,
        maxTokens: m.maxTokens,
        visionCapable: m.visionCapable,
        toolsCapable: m.toolsCapable,
        structuredOutput: m.structuredOutput,
        thinkingMode: m.thinkingMode,
        available: m.available,
      }))
      .sort((a, b) => a.id.localeCompare(b.id))

    return JSON.stringify(normalized)
  }

  const mapManualSourceModels = (source: ManualAggregatorSource): Model[] => {
    return source.manualModels.map(m => ({
      id: `${source.name}:${m.id}`,
      name: m.name,
      source: 'manual' as ModelSource,
      sourceName: source.name,
      baseUrl: source.baseUrl,
      apiKey: source.apiKey,
      platform: source.platform === 'openai-compatible' ? 'openai' : source.platform,
      // 使用默认值
      type: 'llm' as ModelType,
      maxTokens: 128000,
      visionCapable: false,
      toolsCapable: false,
      structuredOutput: false,
      thinkingMode: 'both' as const,
      available: true,
      lastChecked: new Date(),
    }))
  }

  // Initial model load
  async function loadModels(trigger: 'ready' | 'config' | 'command' | 'pending' = 'config') {
    if (isLoading) {
      pendingReload = true
      if (config.debugMode) {
        ctx.logger.info(`loadModels skipped (already running), queued pending reload (trigger=${trigger})`)
      }
      return
    }

    isLoading = true
    const loadStartedAt = Date.now()

    try {
      ctx.logger.info('Loading models...')

      const allModels: Model[] = []

      for (const source of config.sources) {
        if (!source.enabled) continue

        const sourceStartedAt = Date.now()
        let sourceModels: Model[] = []

        if (source.autoFetchModels) {
          sourceModels = await fetcher.fetchModels(source)
        } else {
          sourceModels = mapManualSourceModels(source)
        }

        allModels.push(...sourceModels)

        const sourceCostMs = Date.now() - sourceStartedAt
        ctx.logger.info(
          `[source] ${source.name}: ${sourceModels.length} models (${sourceCostMs}ms${source.autoFetchModels ? ', auto' : ', manual'})`
        )
      }

      // Update service
      service.updateModels(allModels)

      const totalCostMs = Date.now() - loadStartedAt

      const modelsHash = buildModelsHash(allModels)
      if (modelsHash === lastModelsHash) {
        ctx.logger.info(`Total models loaded: ${allModels.length} (${totalCostMs}ms, unchanged)`)
        return
      }

      lastModelsHash = modelsHash
      ctx.logger.info(`Total models loaded: ${allModels.length} (${totalCostMs}ms)`)

      // Emit update event
      ctx.emit('elysia-api/models-updated', [...allModels])
    } finally {
      isLoading = false

      if (pendingReload) {
        pendingReload = false
        void loadModels('pending')
      }
    }
  }

  lastConfigHash = buildConfigHash()

  // Load models on ready
  ctx.on('ready', () => {
    void loadModels('ready')
  })

  // Reload on config change
  ctx.on('config', () => {
    const configHash = buildConfigHash()
    if (configHash === lastConfigHash) {
      if (config.debugMode) {
        ctx.logger.info('aggregator: config event ignored (aggregator config unchanged)')
      }
      return
    }

    lastConfigHash = configHash
    void loadModels('config')
  })

  // CLI command for manual reload
  ctx.command('elysia-api.models.reload', '重新加载模型列表').action(async () => {
    await loadModels('command')
    const count = service.getAll().length
    return `已加载 ${count} 个模型`
  })

  // CLI command to list models
  ctx.command('elysia-api.models.list', '列出所有模型').action(() => {
    const all = ctx.elysiaApi?.models.getAll() ?? []
    return `可用模型列表 (${all.length}):\n` +
      all.map(m => `- ${m.name} (${m.type})`).join('\n')
  })
}
