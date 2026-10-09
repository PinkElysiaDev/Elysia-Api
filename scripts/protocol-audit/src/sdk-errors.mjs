// Local-only negative replay: a schema/transport exception is not evidence that
// the client received the gateway's intended error. Assert its exact cause and
// preceding text separately for both SDKs, with one HTTP request each.
import assert from 'node:assert/strict'
import OpenAI from 'openai'
import { createOpenAI } from '@ai-sdk/openai'
import { packageVersion } from './sdk.mjs'

export const consumeResponsesFailure = options => consumeFailure('responses', options)
export const consumeChatFailure = options => consumeFailure('chat', options)

async function consumeFailure(protocol, { baseUrl, cause, expectedText = 'hi', timeoutMs = 10000 }) {
  assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(baseUrl).hostname), 'Error replay requires a loopback endpoint')
  assert.ok(typeof cause === 'string' && cause.length > 0)
  const results = []
  for (const name of ['openai', '@ai-sdk/openai']) {
    let calls = 0, text = '', errors = [], contentEvents = ''
    const signal = AbortSignal.timeout(timeoutMs)
    const config = {
      apiKey: 'synthetic-error-replay', baseURL: `${baseUrl}/v1`,
      fetch: (input, init) => { calls++; return fetch(input, { ...init, signal, redirect: 'error' }) },
    }
    if (name === 'openai') {
      const client = new OpenAI({ ...config, maxRetries: 0, timeout: timeoutMs })
      if (protocol === 'responses') {
        const stream = client.responses.stream({ model: 'm', input: 'synthetic test', store: false }, { signal })
        stream.on('response.output_text.delta', event => { text += event.delta })
        try { await stream.finalResponse() } catch (error) { errors.push(error) }
      } else {
        const stream = client.chat.completions.stream({ model: 'm', messages: [{ role: 'user', content: 'synthetic test' }] }, { signal })
        // The official helper suppresses `content` callbacks until a role is
        // known. Check the chunks actually consumed as well as that limitation.
        stream.on('chunk', chunk => { text += chunk.choices?.find(choice => choice.index === 0)?.delta?.content || '' })
        stream.on('content', delta => { contentEvents += delta })
        try { await stream.finalChatCompletion() } catch (error) { errors.push(error) }
      }
    } else {
      const provider = createOpenAI(config)
      const model = protocol === 'responses' ? provider.responses('m') : provider.chat('m')
      const result = await model.doStream({
        prompt: [{ role: 'user', content: [{ type: 'text', text: 'synthetic test' }] }], abortSignal: signal,
      })
      for await (const event of result.stream) {
        if (event.type === 'text-delta') text += event.delta
        if (event.type === 'error') errors.push(event.error)
      }
    }
    assert.equal(calls, 1, `${name} retried the request`)
    assert.equal(text, expectedText, `${name} lost text before the error`)
    assert.equal(errors.length, 1, `${name} did not report exactly one failure`)
    assert.ok(errors[0]?.message?.includes(cause), `${name} did not receive the intended cause: ${errors[0]?.message}`)
    assert.notEqual(errors[0]?.name, 'AI_TypeValidationError', `${name} rejected the wire schema`)
    results.push({ name, version: packageVersion(name), calls, errors: errors.length, text, ...(protocol === 'chat' && name === 'openai' ? { contentEvents } : {}) })
  }
  return results
}
