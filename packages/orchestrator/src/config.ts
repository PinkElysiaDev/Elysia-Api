import { Context, Schema } from 'koishi'
import { Model, ModelType, ThinkingMode } from '@elysia-api/shared'

export interface ServerConfig {
  host: string
  port: number
}

export interface AccessToken {
  token: string
  enabled: boolean
}

export type Capability = 'visionCapable' | 'toolsCapable' | 'structuredOutput'

export interface ModelItem {
  model: string
}

export interface ModelGroupConfig {
  id: string
  name: string
  enabled: boolean
  models: ModelItem[]
  strategy: 'round-robin' | 'sequential' | 'random'
  maxRetries: number
  retryInterval: number
  enableRateLimit: boolean
  maxConcurrency?: number
  dailyLimitMaxRequests?: number
  dailyLimitMaxTokens?: number
  maxTokens?: number
  type?: ModelType
  capabilities?: Capability[]
  thinkingMode?: ThinkingMode
}

export interface Config {
  server: ServerConfig
  heartbeatTimeout?: number
  heartbeatInterval?: number
  httpTimeout?: number
  tokens: Record<string, AccessToken>
  modelGroups: ModelGroupConfig[]
  debugMode?: boolean
  verboseLog?: boolean
}

// 模型组配置 Schema
const modelGroupSchema = Schema.intersect([
  // 基础字段
  Schema.object({
    id: Schema.string().required().description('模型组 ID'),
    name: Schema.string().required().description('模型组名称'),
    enabled: Schema.boolean().default(true).description('模型组启用'),
  }),

  // 添加模型（table 类型）
  Schema.object({
    models: Schema.array(Schema.object({
      model: Schema.dynamic('elysia-api-orchestrator.models'),
    })).description('添加模型').default([]),
  }),

  // 轮询策略
  Schema.object({
    strategy: Schema.union([
      Schema.const('round-robin' as const).description('轮询'),
      Schema.const('sequential' as const).description('顺序'),
      Schema.const('random' as const).description('随机'),
    ]).default('round-robin' as const).description('轮询策略'),
  }),

  // 重试配置
  Schema.object({
    maxRetries: Schema.number().default(3).description('重试次数'),
    retryInterval: Schema.number().default(1000).description('重试间隔（毫秒）'),
  }),

  // 最大上下文
  Schema.object({
    maxTokens: Schema.number().description('最大上下文'),
  }),

  // 模型组类型（条件分支）- 按照示例模式
  Schema.object({
    type: Schema.union(['llm', 'embedding', 'reranker'] as const)
      .description('模型组类型'),
  }),

  Schema.union([
    // LLM 分支
    Schema.object({
      type: Schema.const('llm' as const).required(),
      capabilities: Schema.array(
        Schema.union([
          Schema.const('visionCapable' as const).description('支持视觉'),
          Schema.const('toolsCapable' as const).description('支持工具调用'),
          Schema.const('structuredOutput' as const).description('支持结构化输出'),
        ])
      ).role('select').description('模型能力'),
      thinkingMode: Schema.union([
        Schema.const('both' as const).description('同时支持'),
        Schema.const('non-thinking-only' as const).description('仅非思考'),
        Schema.const('thinking-only' as const).description('仅思考'),
      ]).description('思考模式'),
    }),
    // Embedding 分支
    Schema.object({
      type: Schema.const('embedding' as const).required(),
    }),
    // Reranker 分支
    Schema.object({
      type: Schema.const('reranker' as const).required(),
    }),
  ]),

  // 流量限制（条件分支）- 按照布尔型模式
  Schema.object({
    enableRateLimit: Schema.boolean().default(false).description('启用流量限制'),
  }),

  Schema.union([
    // true 分支
    Schema.object({
      enableRateLimit: Schema.const(true).required(),
      maxConcurrency: Schema.number().default(10).description('最大并发数'),
      dailyLimitMaxRequests: Schema.number().description('单日最大请求数'),
      dailyLimitMaxTokens: Schema.number().description('单日最大 token 消耗'),
    }),
    // false 分支
    Schema.object({}),
  ]),
])

// 创建插件配置 Schema
// 参考 mbc-satori-ai-charon 的模式，在创建配置时立即设置初始动态 Schema
export const createConfig = (ctx: Context): Schema<Config> => {
  // 立即设置初始 Schema（空选项，只有"无"）
  // 这确保配置界面加载时就能看到选项
  updateModelSchema(ctx, [])

  return Schema.intersect([
    // 基础配置
    Schema.object({
      server: Schema.object({
        host: Schema.string().default('127.0.0.1').description('监听地址'),
        port: Schema.number().default(8765).description('监听端口'),
      }),
      heartbeatTimeout: Schema.number().default(300).description('后端心跳超时时间（秒）'),
      heartbeatInterval: Schema.number().default(60).description('插件发送心跳间隔（秒）'),
      httpTimeout: Schema.number().default(120).description('HTTP 请求超时时间（秒），0 为不限制'),
    }).description('基础配置'),

    // 访问令牌配置（dict 类型，使用 table 外观）
    Schema.object({
      tokens: Schema.dict(
        Schema.object({
          token: Schema.string().role('secret').description('访问令牌'),
          enabled: Schema.boolean().default(true).description('启用'),
        })
      ).role('table').description('访问令牌列表'),
    }).description('访问令牌'),

    // 模型组配置
    Schema.object({
      modelGroups: Schema.array(modelGroupSchema)
        .role('list')
        .description('配置模型组'),
    }).description('模型组配置'),

    // Debug options
    Schema.object({
      debugMode: Schema.boolean().default(false).description('启用调试日志'),
      verboseLog: Schema.boolean().default(false).description('启用详细日志（包含路径、PID 等详细信息）'),
    }).description('调试选项'),
  ]) as unknown as Schema<Config>
}

// 静态导出（用于配置界面）- 独立定义，不调用 createConfig
// 这是配置界面的回退 Schema，不包含任何动态 Schema 注册
export const ConfigSchema: Schema<Config> = Schema.intersect([
  // 基础配置
  Schema.object({
    server: Schema.object({
      host: Schema.string().default('127.0.0.1').description('监听地址'),
      port: Schema.number().default(8765).description('监听端口'),
    }),
    heartbeatTimeout: Schema.number().default(300).description('后端心跳超时时间（秒）'),
    heartbeatInterval: Schema.number().default(60).description('插件发送心跳间隔（秒）'),
    httpTimeout: Schema.number().default(120).description('HTTP 请求超时时间（秒），0 为不限制'),
  }).description('基础配置'),

  // 访问令牌配置（dict 类型，使用 table 外观）
  Schema.object({
    tokens: Schema.dict(
      Schema.object({
        token: Schema.string().role('secret').description('访问令牌'),
        enabled: Schema.boolean().default(true).description('启用'),
      })
    ).role('table').description('访问令牌列表'),
  }).description('访问令牌'),

  // 模型组配置
  Schema.object({
    modelGroups: Schema.array(modelGroupSchema)
      .role('list')
      .description('配置模型组'),
  }).description('模型组配置'),

  // Debug options
  Schema.object({
    debugMode: Schema.boolean().default(false).description('启用调试日志'),
    verboseLog: Schema.boolean().default(false).description('启用详细日志'),
  }).description('调试选项'),
]) as unknown as Schema<Config>

export const name = 'elysia-api-orchestrator'

/**
 * 更新动态模型列表的 Schema
 * 参考 mbc-satori-ai-charon 的 updateBotIdOptions 模式
 *
 * @param ctx - Koishi context
 * @param models - 模型列表
 */
export function updateModelSchema(ctx: Context, models: Model[]) {
  // 占位符始终放在最前面，作为默认选项
  const placeholder = Schema.const('').description('无')

  if (models.length === 0) {
    ctx.schema.set('elysia-api-orchestrator.models', Schema.union([placeholder]))
    return
  }

  const options = [
    placeholder,
    ...models.map(m => {
      // 统一格式：[源名称] 模型名称
      const sourcePrefix = m.sourceName ?? '未知来源'
      const displayName = `[${sourcePrefix}] ${m.name}`
      return Schema.const(m.id).description(displayName)
    })
  ]

  ctx.schema.set('elysia-api-orchestrator.models', Schema.union(options))
}
