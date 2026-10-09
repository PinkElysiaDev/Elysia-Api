import { createRequire } from 'node:module'
import { defaults } from './audit.mjs'
import { readFileSync, existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import assert from 'node:assert/strict'
import OpenAI from 'openai'
import Anthropic from '@anthropic-ai/sdk'
import { GoogleGenAI } from '@google/genai'
import { createOpenAI } from '@ai-sdk/openai'
import { createAnthropic } from '@ai-sdk/anthropic'

const require = createRequire(import.meta.url)
function packageVersion(name) {
  let directory = dirname(require.resolve(name))
  while (dirname(directory) !== directory) {
    const path = join(directory, 'package.json')
    if (existsSync(path)) { const pkg = JSON.parse(readFileSync(path, 'utf8')); if (pkg.name === name) return pkg.version }
    directory = dirname(directory)
  }
  throw new Error(`Cannot determine SDK version: ${name}`)
}

export async function consume(protocol, { baseUrl, apiKey, model, timeoutMs = defaults.timeoutMs, maxOutputTokens = defaults.maxOutputTokens, maxResponseBytes = defaults.maxResponseBytes, signal, onExchange = async () => {} }, stream) {
  const used = [], pending = [], transportErrors = []
  const version = name => { used.push({ name, version: packageVersion(name) }) }
  const capture = async (input, init) => {
    // Keep each case to one in-flight HTTP request until the previous body is fully read.
    await Promise.all(pending)
    if (transportErrors.length) throw transportErrors[0]
    const request = new Request(input, init), controller = new AbortController(), started = performance.now()
    const detail = { request: { url: request.url, method: request.method, headers: Object.fromEntries(request.headers), body: await request.clone().text() } }
    const timer = setTimeout(() => controller.abort(new Error(`SDK request exceeded ${timeoutMs} ms`)), timeoutMs)
    const combined = AbortSignal.any([controller.signal, request.signal, ...(signal ? [signal] : [])])
    try {
      const response = await fetch(request, { signal: combined, redirect: 'error' })
      detail.response = { status: response.status, headers: Object.fromEntries(response.headers), body: '', bytes: 0 }
      // Read a clone so SDKs receive the real streaming response as it arrives.
      pending.push((async () => {
        const chunks = [], reader = response.clone().body?.getReader()
        try {
          if (reader) for (;;) {
            const { done, value } = await reader.read(); if (done) break
            detail.firstByteMs ??= Math.round(performance.now() - started)
            const room = maxResponseBytes - detail.response.bytes
            if (room > 0) chunks.push(Buffer.from(value.subarray(0, room)))
            detail.response.bytes += value.length
            if (detail.response.bytes > maxResponseBytes) { controller.abort(); throw new Error('SDK response exceeded maxResponseBytes') }
          }
        } catch (error) { detail.error = error.message; transportErrors.push(error) }
        finally { clearTimeout(timer); reader?.releaseLock(); detail.response.body = Buffer.concat(chunks).toString('utf8'); detail.elapsedMs = Math.round(performance.now() - started); await onExchange(detail) }
      })().catch(error => { transportErrors.push(error) }))
      return response
    } catch (error) {
      clearTimeout(timer); detail.error = error.message; detail.elapsedMs = Math.round(performance.now() - started); await onExchange(detail); throw error
    }
  }
  let failure
  try {
    const options = { prompt: [{ role: 'user', content: [{ type: 'text', text: 'Reply exactly OK.' }] }], maxOutputTokens, abortSignal: signal }
    if (protocol === 'chat' || protocol === 'responses') {
      version('openai')
      const client = new OpenAI({ apiKey, baseURL: `${baseUrl}/v1`, maxRetries: 0, timeout: timeoutMs, fetch: capture })
      if (protocol === 'chat') {
        const params = { model, messages: [{ role: 'user', content: 'Reply exactly OK.' }], max_completion_tokens: maxOutputTokens }
        const result = stream ? await client.chat.completions.stream(params, { signal }).finalChatCompletion() : await client.chat.completions.create(params, { signal })
        assert.ok(result.choices.some(c => c.message?.content?.includes('OK')))
      } else {
        const params = { model, input: 'Reply exactly OK.', max_output_tokens: maxOutputTokens, store: false }
        const result = stream ? await client.responses.stream(params, { signal }).finalResponse() : await client.responses.create(params, { signal })
        assert.ok(result.output.some(o => o.content?.some(c => c.text?.includes('OK'))))
      }
      version('@ai-sdk/openai')
      const provider = createOpenAI({ apiKey, baseURL: `${baseUrl}/v1`, fetch: capture })
      await checkAI(protocol === 'chat' ? provider.chat(model) : provider.responses(model), stream, options)
    } else if (protocol === 'anthropic') {
      version('@anthropic-ai/sdk')
      const client = new Anthropic({ apiKey, baseURL: baseUrl, maxRetries: 0, timeout: timeoutMs, fetch: capture })
      const params = { model, max_tokens: maxOutputTokens, messages: [{ role: 'user', content: 'Reply exactly OK.' }] }
      const result = stream ? await client.messages.stream(params, { signal }).finalMessage() : await client.messages.create(params, { signal })
      assert.ok(result.content.some(c => c.text?.includes('OK')))
      version('@ai-sdk/anthropic')
      await checkAI(createAnthropic({ apiKey, baseURL: `${baseUrl}/v1`, fetch: capture })(model), stream, options)
    } else {
      version('@google/genai')
      const client = new GoogleGenAI({ apiKey, httpOptions: { baseUrl, timeout: timeoutMs, fetch: capture, retryOptions: { attempts: 1 } } })
      const params = { model, contents: 'Reply exactly OK.', config: { maxOutputTokens, abortSignal: signal } }
      let text = ''
      if (stream) for await (const part of await client.models.generateContentStream(params)) { assert.ok(!part.error); text += part.text || '' }
      else text = (await client.models.generateContent(params)).text || ''
      assert.ok(text.includes('OK'))
    }
  } catch (error) { failure = error }
  const captured = await Promise.allSettled(pending)
  failure ||= captured.find(r => r.status === 'rejected')?.reason || transportErrors[0]
  if (failure) { failure.sdk = used; throw failure }
  return used
}

async function checkAI(model, stream, options) {
  if (!stream) { assert.ok((await model.doGenerate(options)).content.some(c => c.text?.includes('OK'))); return }
  let finished = false, text = ''
  for await (const event of (await model.doStream(options)).stream) {
    if (event.type === 'error') throw event.error
    if (event.type === 'text-delta') text += event.delta
    if (event.type === 'finish') finished = true
  }
  assert.ok(finished && text.includes('OK'), 'SDK did not receive expected text and finish event')
}
