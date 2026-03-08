import { Context, Schema, Service } from 'koishi'
import { Model, ModelType, AutoFetchSource, ManualModel, ModelSource } from '@elysia-api/shared'
import { ModelFetcher } from './model-fetcher'
import { ModelValidator } from './model-validator'
import { Config, name } from './config'

export { Config, name }

export const usage = `---

## 使用说明

本插件用于自动获取和管理可用的 AI 模型，支持 OpenAI、Claude、Gemini 等平台。

### 配置步骤

1. **自动获取**: 在「自动拉取源」中添加 API 端点和 API Key
2. **手动添加**: 在「手动添加的模型」中直接配置模型
3. **重新加载**: 配置完成后使用 \`elysia-api.models.reload\` 命令生效

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
      autoFetchSources: config.autoFetchSources,
      manualModels: config.manualModels,
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

      // Fetch auto sources
      const fetchedModels: Model[] = []
      for (const source of config.autoFetchSources) {
        if (!source.enabled) continue

        const sourceStartedAt = Date.now()
        const sourceModels = await fetcher.fetchModels(source)
        fetchedModels.push(...sourceModels)

        const sourceCostMs = Date.now() - sourceStartedAt
        ctx.logger.info(`[source] ${source.name}: ${sourceModels.length} models (${sourceCostMs}ms)`)
      }

      // Add manual models
      const manualModels: Model[] = config.manualModels.map(m => {
        return {
          id: m.id,
          name: m.name,
          source: 'manual' as ModelSource,
          sourceName: m.sourceName,
          baseUrl: m.baseUrl,
          apiKey: m.apiKey,
          platform: m.platform,
          // 使用默认值
          type: 'llm' as ModelType,
          maxTokens: 128000,
          visionCapable: false,
          toolsCapable: false,
          structuredOutput: false,
          thinkingMode: 'both' as const,
          available: true,
          lastChecked: new Date(),
        }
      })

      // Combine all models
      const allModels = [...fetchedModels, ...manualModels]

      // Update service (使用保存的 service 引用)
      service.updateModels(allModels)

      const totalCostMs = Date.now() - loadStartedAt
      ctx.logger.info(`[source] manual: ${manualModels.length} models`)

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
