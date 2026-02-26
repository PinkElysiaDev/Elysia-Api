import { Context, Schema } from 'koishi'
import { Model, AutoFetchSource, ManualModel, PlatformType, ModelType } from '@elysia-api/shared'

export interface Config {
  // Auto-fetch sources
  autoFetchSources: AutoFetchSource[]

  // Manual models
  manualModels: ManualModel[]

  // Debug mode
  debugMode?: boolean
}

export const Config: Schema<Config> = Schema.intersect([
  // Auto-fetch sources configuration
  Schema.object({
    autoFetchSources: Schema.array(
      Schema.intersect([
        Schema.object({
          name: Schema.string().required().description('源名称'),
          baseUrl: Schema.string().required().description('API 端点'),
          apiKey: Schema.string().required().role('secret').description('API Key'),
          platform: Schema.union([
            Schema.const('openai' as const).description('OpenAI'),
            Schema.const('claude' as const).description('Claude'),
            Schema.const('gemini' as const).description('Gemini'),
            Schema.const('openai-compatible' as const).description('OpenAI 兼容'),
          ]).description('平台类型'),
          enabled: Schema.boolean().default(true).description('启用'),
        }),
      ])
    ).role('table').description('自动拉取源'),
  }),

  // Manual models
  Schema.object({
    manualModels: Schema.array(
      Schema.object({
        id: Schema.string().required().description('模型 ID'),
        name: Schema.string().required().description('模型名称'),
        sourceName: Schema.string().required().description('源名称'),
        baseUrl: Schema.string().required().description('API 端点'),
        apiKey: Schema.string().required().role('secret').description('API Key'),
        platform: Schema.union([
          Schema.const('openai' as const).description('OpenAI'),
          Schema.const('claude' as const).description('Claude'),
          Schema.const('gemini' as const).description('Gemini'),
        ]).description('平台类型'),
      })
    ).role('table').description('手动添加的模型'),
  }),

  // Debug options
  Schema.object({
    debugMode: Schema.boolean().default(false).description('启用调试日志'),
  }).description('调试选项'),
])

export const name = 'elysia-api-aggregator'
