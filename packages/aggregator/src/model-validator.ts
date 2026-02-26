import { Model } from '@elysia-api/shared'

export class ModelValidator {
  async validateModel(model: Model): Promise<boolean> {
    try {
      const response = await fetch(`${model.baseUrl}/chat/completions`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${model.apiKey}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          model: model.name,
          messages: [{ role: 'user', content: 'test' }],
          max_tokens: 1,
        }),
      })

      return response.ok
    } catch {
      return false
    }
  }

  async validateEmbeddingModel(model: Model): Promise<boolean> {
    try {
      const response = await fetch(`${model.baseUrl}/embeddings`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${model.apiKey}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          model: model.name,
          input: 'test',
        }),
      })

      return response.ok
    } catch {
      return false
    }
  }
}
