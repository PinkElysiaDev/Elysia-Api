import { Context } from 'koishi'
import { Model, ModelType } from '@elysia-api/shared'
import { BackendManager } from './backend-manager'
import { Config, ConfigSchema, createConfig, name, updateModelSchema } from './config'

export { ConfigSchema as Config, createConfig, name }

export const usage = `---

## 使用说明

本插件提供 API 网关和模型编排功能，支持负载均衡、流式响应、格式转换。

### 配置步骤

1. **创建模型组**: 在「模型组配置」中添加新组，选择要使用的模型
2. **访问令牌**: 在「访问令牌列表」中配置请求密钥
3. **测试**: 使用 \`elysia-api.backend.status\` 检查后端状态

### CLI 命令

- \`elysia-api.backend.status\` - 查看后端状态
- \`elysia-api.backend.reload\` - 重载配置
- \`elysia-api.models.list\` - 列出可用模型

---

`

// Context extension declaration
declare module 'koishi' {
  // 定义 AggregatorService 接口（与 aggregator 中的实现保持一致）
  interface AggregatorService {
    getAll(): Model[]
    getById(id: string): Model | undefined
    getByType(type: ModelType): Model[]
  }

  interface Context {
    // 通过 inject 注入的服务
    'elysia-api-aggregator': AggregatorService
  }

  interface Events {
    'elysia-api/models-updated': (models: Model[]) => void
  }
}

export const inject = ['elysia-api-aggregator']

export function apply(ctx: Context, config: Config) {
  ctx.logger.info('=== orchestrator: apply function called ===')
  ctx.logger.info(`orchestrator: debugMode = ${config.debugMode}, verboseLog = ${config.verboseLog}`)

  let backend: BackendManager
  let backendInitialized = false

  // 初始化后端（只执行一次）
  const initializeBackend = () => {
    if (backendInitialized) {
      if (config.debugMode) {
        ctx.logger.info('initializeBackend: Backend already initialized, skipping')
      }
      return
    }

    if (config.debugMode) {
      ctx.logger.info('=== initializeBackend: Starting ===')
    }

    backendInitialized = true

    // Initialize backend (只创建一个实例)
    backend = new BackendManager(
      ctx,
      config.server,
      config.tokens,
      config.modelGroups,
      config.heartbeatInterval ?? 60,  // 心跳发送间隔
      config.heartbeatTimeout,         // 后端心跳超时时间
      config.verboseLog ?? false       // 详细日志模式
    )

    // 启动后端（如果未运行）
    backend.start().then(() => {
      ctx.logger.info(`Backend started on ${config.server.host}:${config.server.port}`)
    }).catch((err) => {
      ctx.logger.error(`Failed to start backend: ${err.message}`)
    })
  }

  // 更新动态模型列表 schema
  const updateModels = () => {
    const models = ctx['elysia-api-aggregator']?.getAll() ?? []

    if (config.debugMode) {
      ctx.logger.info(`updateModels: Retrieved ${models.length} models from aggregator`)
      for (const model of models) {
        ctx.logger.info(`updateModels: Model - id="${model.id}", name="${model.name}"`)
      }
    }

    updateModelSchema(ctx, models)

    if (config.debugMode) {
      ctx.logger.info(`updateModels: Updated model schema: ${models.length} models available`)
    } else {
      ctx.logger.debug(`Updated model schema: ${models.length} models available`)
    }
  }

  // Listen for model updates from aggregator
  // 这个事件会在 aggregator 加载完模型后触发
  ctx.on('elysia-api/models-updated', (models) => {
    if (config.debugMode) {
      ctx.logger.info(`=== orchestrator: elysia-api/models-updated event received ===`)
      ctx.logger.info(`orchestrator: Event contains ${models.length} models`)
    }
    ctx.logger.info(`Models updated: ${models.length} models available`)

    updateModels()  // 更新动态 schema
    initializeBackend()  // 确保后端已初始化
    backend?.reloadConfig()  // 只更新配置文件，不重启
  })

  // Wait for aggregator to be ready
  ctx.on('ready', () => {
    if (config.debugMode) {
      ctx.logger.info('=== orchestrator: ready event fired ===')
      ctx.logger.info(`orchestrator: ctx['elysia-api-aggregator'] exists: ${ctx['elysia-api-aggregator'] != null}`)
    }

    updateModels()
    const models = ctx['elysia-api-aggregator']?.getAll() ?? []
    ctx.logger.info(`Loaded ${models.length} models from aggregator`)

    // 如果已经有模型了，初始化后端
    if (models.length > 0) {
      initializeBackend()
    }
  })

  // Reload on config change
  ctx.on('config', () => {
    // 更新现有 backend 实例的配置
    backend?.reloadConfig()
  })

  // Stop backend on dispose - 使用摇篮模式，不主动停止后端
  // 后端会通过心跳超时自动停止（300 秒无心跳）
  ctx.on('dispose', async () => {
    // 不再停止后端，让它通过心跳超时自动停止（摇篮模式）
  })

  // CLI commands
  ctx.command('elysia-api.backend.status', '查看后端状态').action(() => {
    const running = backend?.isRunning() ?? false
    return running ? '后端运行中' : '后端未运行'
  })

  ctx.command('elysia-api.backend.reload', '重载后端配置').action(async () => {
    await backend?.reloadConfig()
    return '后端配置已重载'
  })

  ctx.command('elysia-api.backend.restart', '重启后端').action(async () => {
    await backend?.stop()
    await backend?.start()
    return '后端已重启'
  })
}
