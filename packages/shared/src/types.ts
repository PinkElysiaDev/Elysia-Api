// Shared types for Elysia-API plugins

export type PlatformType = 'openai' | 'claude' | 'gemini'
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

export interface AutoFetchSource {
  name: string
  baseUrl: string
  apiKey: string
  platform: PlatformType | 'openai-compatible'
  enabled: boolean
}

export interface ManualModel {
  id: string
  name: string
  baseUrl: string
  apiKey: string
  platform: PlatformType
  sourceName: string  // 源名称，用于标识模型来源
}
