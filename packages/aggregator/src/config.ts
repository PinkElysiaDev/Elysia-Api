import { Schema } from 'koishi'
import { PlatformType } from '@elysia-api/shared'

export interface ManualSourceModel {
  id: string
  name: string
}

export type SourcePlatformType = PlatformType | 'openai-compatible'

export interface AggregatorSourceConfigBase {
  name: string
  baseUrl: string
  apiKey: string
  platform: SourcePlatformType
  enabled: boolean
}

export interface AutoFetchAggregatorSource extends AggregatorSourceConfigBase {
  autoFetchModels: true
}

export interface ManualAggregatorSource extends AggregatorSourceConfigBase {
  autoFetchModels: false
  manualModels: ManualSourceModel[]
}

export type AggregatorSourceConfig =
  | AutoFetchAggregatorSource
  | ManualAggregatorSource

export interface Config {
  // Unified source configs
  sources: AggregatorSourceConfig[]

  // Debug mode
  debugMode?: boolean
  verboseLog?: boolean
}

const manualSourceModelSchema: Schema<ManualSourceModel> = Schema.object({
  id: Schema.string().required().description('模型 ID'),
  name: Schema.string().required().description('模型名称'),
})

const sourceBaseSchema = Schema.object({
  name: Schema.string().required().description('源名称'),
  baseUrl: Schema.string().required().description('API 端点'),
  apiKey: Schema.string().required().role('secret').description('API Key'),
  platform: Schema.union([
    Schema.const('openai' as const).description('OpenAI'),
    Schema.const('claude' as const).description('Claude'),
    Schema.const('gemini' as const).description('Gemini'),
    Schema.const('openai-compatible' as const).description('OpenAI 兼容'),
  ]).description('平台类型') as Schema<SourcePlatformType>,
  enabled: Schema.boolean().default(true).description('启用'),
  autoFetchModels: Schema.boolean()
    .default(true)
    .description('是否自动拉取模型'),
})

const sourceSchema = Schema.intersect([
  sourceBaseSchema,
  Schema.union([
    Schema.object({
      autoFetchModels: Schema.const(false).required(),
      manualModels: Schema.array(manualSourceModelSchema)
        .default([])
        .role('table')
        .description('手动添加模型'),
    }),
    Schema.object({}),
  ]),
])

export const Config: Schema<Config> = Schema.intersect([
  Schema.object({
    sources: Schema.array(sourceSchema)
      .role('list')
      .default([])
      .description('添加源'),
  }).description('模型源配置'),

  // Debug options
  Schema.object({
    debugMode: Schema.boolean().default(false).description('启用调试日志'),
    verboseLog: Schema.boolean().default(false).description('启用详细日志（输出配置摘要、hash 与重载判断过程）'),
  }).description('调试选项'),
]) as unknown as Schema<Config>

export const name = 'elysia-api-aggregator'
