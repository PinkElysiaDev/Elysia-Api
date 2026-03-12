import { Model, ModelSource, ModelType, PlatformType } from '@elysia-api/shared'
import { AutoFetchAggregatorSource } from './config'

interface ClaudeModelInfo {
  id: string
  created_at?: string
  display_name?: string
  type?: 'model'
}

interface ClaudeListModelsResponse {
  data: ClaudeModelInfo[]
  first_id?: string
  has_more?: boolean
  last_id?: string
}

export class ModelFetcher {
  constructor(private ctx: import('koishi').Context) {}

  async fetchModels(source: AutoFetchAggregatorSource): Promise<Model[]> {
    try {
      switch (source.platform) {
        case 'openai':
        case 'openai-compatible':
          return await this.fetchOpenAIModels(source)
        case 'claude':
          return await this.fetchClaudeModels(source)
        case 'gemini':
          return await this.fetchGeminiModels(source)
        default:
          return []
      }
    } catch (error) {
      this.ctx.logger.error(`Failed to fetch models from ${source.name}: ${error}`)
      return []
    }
  }

  private normalizeBaseUrl(baseUrl: string): string {
    return baseUrl.replace(/\/+$/, '')
  }

  private buildUrl(baseUrl: string, path: string): string {
    const normalizedBase = this.normalizeBaseUrl(baseUrl)
    const normalizedPath = path.startsWith('/') ? path : `/${path}`
    return `${normalizedBase}${normalizedPath}`
  }

  private getModelPlatform(source: AutoFetchAggregatorSource): PlatformType {
    return source.platform === 'openai-compatible' ? 'openai' : source.platform
  }

  private async fetchOpenAIModels(source: AutoFetchAggregatorSource): Promise<Model[]> {
    const response = await fetch(this.buildUrl(source.baseUrl, '/models'), {
      headers: { Authorization: `Bearer ${source.apiKey}` },
    })

    if (!response.ok) {
      const raw = await response.text().catch(() => '')
      throw new Error(`HTTP ${response.status}: ${response.statusText}${raw ? ` | ${raw}` : ''}`)
    }

    const data = await response.json()
    const models = Array.isArray(data?.data) ? data.data : []

    return models
      .filter((model: any) => typeof model?.id === 'string' && model.id.length > 0)
      .map((model: any) => ({
        id: `${source.name}:${model.id}`,
        name: model.id,
        source: 'auto' as ModelSource,
        sourceName: source.name,
        baseUrl: source.baseUrl,
        apiKey: source.apiKey,
        platform: this.getModelPlatform(source),
        type: this.inferModelType(model.id),
        maxTokens: this.inferMaxTokens(model.id),
        visionCapable: this.hasVisionCapability(model.id),
        toolsCapable: this.hasToolsCapability(model.id),
        structuredOutput: this.hasStructuredOutput(model.id),
        thinkingMode: 'both' as const,
        available: true,
        lastChecked: new Date(),
      }))
  }

  private async fetchClaudeModels(source: AutoFetchAggregatorSource): Promise<Model[]> {
    try {
      const response = await fetch(this.buildUrl(source.baseUrl, '/models'), {
        headers: {
          'x-api-key': source.apiKey,
          'anthropic-version': '2023-06-01',
          'Content-Type': 'application/json',
        },
      })

      if (!response.ok) {
        const raw = await response.text().catch(() => '')
        throw new Error(`HTTP ${response.status}: ${response.statusText}${raw ? ` | ${raw}` : ''}`)
      }

      const data = (await response.json()) as ClaudeListModelsResponse
      const models = Array.isArray(data?.data) ? data.data : []

      if (!models.length) {
        throw new Error('Claude models API returned empty list')
      }

      return models
        .filter(model => typeof model?.id === 'string' && model.id.length > 0)
        .map(model => ({
          id: `${source.name}:${model.id}`,
          name: model.display_name?.trim() || model.id,
          source: 'auto' as ModelSource,
          sourceName: source.name,
          baseUrl: source.baseUrl,
          apiKey: source.apiKey,
          platform: 'claude' as const,
          type: 'llm' as const,
          maxTokens: this.inferClaudeMaxTokens(model.id),
          visionCapable: true,
          toolsCapable: true,
          structuredOutput: true,
          thinkingMode: 'both' as const,
          available: true,
          lastChecked: new Date(),
        }))
    } catch (error) {
      this.ctx.logger.warn(
        `[Claude Fetch Fallback] Failed to fetch models from source "${source.name}" (${source.baseUrl}), fallback to built-in Claude model list: ${error}`
      )
      return this.getBuiltInClaudeModels(source)
    }
  }

  private getBuiltInClaudeModels(source: AutoFetchAggregatorSource): Model[] {
    const knownModels = [
      { id: 'claude-3-7-sonnet-20250219', maxTokens: 200000 },
      { id: 'claude-3-5-sonnet-20241022', maxTokens: 200000 },
      { id: 'claude-3-5-haiku-20241022', maxTokens: 200000 },
      { id: 'claude-3-opus-20240229', maxTokens: 200000 },
    ]

    return knownModels.map(model => ({
      id: `${source.name}:${model.id}`,
      name: model.id,
      source: 'auto' as ModelSource,
      sourceName: source.name,
      baseUrl: source.baseUrl,
      apiKey: source.apiKey,
      platform: 'claude' as const,
      type: 'llm' as const,
      maxTokens: model.maxTokens,
      visionCapable: true,
      toolsCapable: true,
      structuredOutput: true,
      thinkingMode: 'both' as const,
      available: true,
      lastChecked: new Date(),
    }))
  }

  private async fetchGeminiModels(source: AutoFetchAggregatorSource): Promise<Model[]> {
    const response = await fetch(
      this.buildUrl(source.baseUrl, `/v1beta/models?key=${encodeURIComponent(source.apiKey)}`)
    )

    if (!response.ok) {
      const raw = await response.text().catch(() => '')
      throw new Error(`HTTP ${response.status}: ${response.statusText}${raw ? ` | ${raw}` : ''}`)
    }

    const data = await response.json()
    const models = Array.isArray(data?.models) ? data.models : []

    return models
      .filter((m: any) => m?.supportedGenerationMethods?.includes('generateContent'))
      .map((model: any) => {
        const rawName = typeof model.name === 'string' ? model.name : ''
        const displayName = rawName.replace(/^models\//, '') || rawName

        return {
          id: `${source.name}:${rawName}`,
          name: displayName,
          source: 'auto' as ModelSource,
          sourceName: source.name,
          baseUrl: source.baseUrl,
          apiKey: source.apiKey,
          platform: 'gemini' as const,
          type: 'llm' as const,
          maxTokens: this.parseGeminiMaxTokens(model),
          visionCapable: true,
          toolsCapable: true,
          structuredOutput: false,
          thinkingMode: 'both' as const,
          available: true,
          lastChecked: new Date(),
        }
      })
  }

  private inferModelType(modelId: string): ModelType {
    const id = modelId.toLowerCase()
    if (id.includes('embed') || id.includes('text-embedding')) {
      return 'embedding'
    }
    if (id.includes('rerank')) {
      return 'reranker'
    }
    return 'llm'
  }

  private inferMaxTokens(modelId: string): number {
    // Known model limits
    const limits: Record<string, number> = {
      'gpt-4o': 128000,
      'gpt-4o-mini': 128000,
      'gpt-4-turbo': 128000,
      'gpt-4': 8192,
      'gpt-3.5-turbo': 16385,
      'text-embedding-3-small': 8191,
      'text-embedding-3-large': 8191,
      'text-embedding-ada-002': 8191,
    }

    for (const [key, value] of Object.entries(limits)) {
      if (modelId.toLowerCase().includes(key)) {
        return value
      }
    }

    return 128000 // Default
  }

  private inferClaudeMaxTokens(modelId: string): number {
    const id = modelId.toLowerCase()
    if (id.includes('claude-3') || id.includes('claude-sonnet') || id.includes('claude-opus') || id.includes('claude-haiku')) {
      return 200000
    }
    return 200000
  }

  private hasVisionCapability(modelId: string): boolean {
    const id = modelId.toLowerCase()
    return id.includes('vision') || id.includes('gpt-4o') || id.includes('gpt-4-turbo')
  }

  private hasToolsCapability(modelId: string): boolean {
    const id = modelId.toLowerCase()
    return !id.includes('gpt-3.5')
  }

  private hasStructuredOutput(modelId: string): boolean {
    const id = modelId.toLowerCase()
    return id.includes('gpt-4o') || id.includes('gpt-4-turbo')
  }

  private parseGeminiMaxTokens(model: any): number {
    return Number(model?.outputTokenLimit)
      || Number(model?.inputTokenLimit)
      || 128000
  }
}
