// Shared types for Elysia-API plugins

export type PlatformType = 'openai' | 'claude' | 'gemini'
export type SourcePlatformType = PlatformType | 'openai-compatible'
export type ModelType = 'llm' | 'embedding' | 'reranker'
export type ThinkingMode = 'both' | 'non-thinking-only' | 'thinking-only'
export type ModelSource = 'auto' | 'manual'

export interface Model {
  // Identification
  id: string
  name: string

  // Source info
  source: ModelSource
  sourceName?: string  // For auto-fetched models

  // Connection
  baseUrl: string
  apiKey: string
  platform: PlatformType

  // Properties
  type: ModelType
  maxTokens: number

  // LLM-specific capabilities
  visionCapable: boolean
  toolsCapable: boolean
  structuredOutput: boolean
  thinkingMode: ThinkingMode

  // Status
  available: boolean
  lastChecked: Date
}

export interface ManualSourceModel {
  id: string
  name: string
}

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

// legacy aliases kept for aggregator internal migration/refactor convenience
export type AutoFetchSource = AutoFetchAggregatorSource

export interface ManualModel {
  id: string
  name: string
  baseUrl: string
  apiKey: string
  platform: PlatformType
  sourceName: string  // 源名称，用于标识模型来源
}
