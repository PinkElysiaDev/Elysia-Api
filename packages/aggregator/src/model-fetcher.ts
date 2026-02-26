import { Model, AutoFetchSource, ModelSource, ModelType } from '@elysia-api/shared'

export class ModelFetcher {
  constructor(private ctx: import('koishi').Context) {}

  async fetchModels(source: AutoFetchSource): Promise<Model[]> {
    this.ctx.logger.info(`Fetching models from ${source.name} (${source.platform})`)

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

  private async fetchOpenAIModels(source: AutoFetchSource): Promise<Model[]> {
    const response = await fetch(`${source.baseUrl}/models`, {
      headers: { 'Authorization': `Bearer ${source.apiKey}` }
    })

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`)
    }

    const data = await response.json()
    const models = data.data || []

    return models.map((model: any) => ({
      id: `${source.name}:${model.id}`,
      name: model.id,
      source: 'auto' as ModelSource,
      sourceName: source.name,
      baseUrl: source.baseUrl,
      apiKey: source.apiKey,
      platform: 'openai' as const,
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

  private async fetchClaudeModels(source: AutoFetchSource): Promise<Model[]> {
    // Claude doesn't have a models endpoint, return known models
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

  private async fetchGeminiModels(source: AutoFetchSource): Promise<Model[]> {
    // Gemini models endpoint
    const response = await fetch(
      `${source.baseUrl}/v1beta/models?key=${source.apiKey}`
    )

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`)
    }

    const data = await response.json()
    const models = data.models || []

    return models
      .filter((m: any) => m.supportedGenerationMethods?.includes('generateContent'))
      .map((model: any) => ({
        id: `${source.name}:${model.name}`,
        name: model.name,
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
      }))
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
    return model.topK?.outputTokenLimit || 128000
  }
}
