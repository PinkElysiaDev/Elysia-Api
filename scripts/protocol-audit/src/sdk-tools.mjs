import assert from 'node:assert/strict'
import OpenAI from 'openai'
import { toResponseInputItems } from 'openai/lib/responses/ResponseInputItems'
import Anthropic from '@anthropic-ai/sdk'
import { GoogleGenAI } from '@google/genai'
import { createOpenAI } from '@ai-sdk/openai'
import { createAnthropic } from '@ai-sdk/anthropic'
import { packageVersion } from './sdk.mjs'

const schema = { type: 'object', properties: { n: { type: 'integer' } }, required: ['n'], additionalProperties: false }
const prompt = 'Call lookup twice with n=1 and n=2, then use both results.'
const resultFor = id => ({ for: id, answer: `result-${id}` })

function checkCalls(calls, expected) {
  assert.equal(calls.length, expected.length, 'SDK lost or duplicated a tool call')
  assert.equal(new Set(calls.map(c => c.id)).size, calls.length, 'SDK duplicated a tool call ID')
  for (const want of expected) {
    const call = calls.find(c => c.id === want.id)
    assert.ok(call, `SDK lost tool call ${want.id}`)
    assert.equal(call.name, 'lookup', 'SDK changed the tool name')
    assert.deepEqual(call.input, want.input, `SDK changed arguments for ${want.id}`)
  }
}

// Receives SDK-owned results and uses those exact identities for the next turn.
// No tools execute, and no configuration or credentials are loaded from disk.
export async function consumeToolRoundTrip(protocol, {
  baseUrl, apiKey = 'synthetic-tool-sdk', model = 'group', timeoutMs = 10000,
  maxResponseBytes = 8 * 1024 * 1024, expectedText = 'hi',
  expectedCalls = [{ id: 'c1', input: { n: 1 } }, { id: 'c2', input: { n: 2 } }],
  onExchange = async () => {}, signal,
}, stream) {
  const packages = protocol === 'gemini' ? ['@google/genai'] : protocol === 'anthropic'
    ? ['@anthropic-ai/sdk', '@ai-sdk/anthropic'] : ['openai', '@ai-sdk/openai']
  const used = []
  for (const name of packages) {
    const evidence = { name, version: packageVersion(name), requests: 0, rounds: 0 }
    used.push(evidence)
    const pending = [], errors = []
    const capture = async (input, init) => {
      await Promise.all(pending)
      if (errors.length) throw errors[0]
      assert.ok(++evidence.requests <= 2, 'SDK retried or made an unexpected third request')
      const request = new Request(input, init), controller = new AbortController()
      const detail = { sdk: name, round: evidence.requests, request: { body: await request.clone().text() } }
      const timer = setTimeout(() => controller.abort(new Error('Tool SDK request timed out')), timeoutMs)
      try {
        const response = await fetch(request, { redirect: 'error', signal: AbortSignal.any([controller.signal, request.signal, ...(signal ? [signal] : [])]) })
        pending.push((async () => {
          const reader = response.clone().body?.getReader(), chunks = []
          let bytes = 0
          try {
            if (reader) for (;;) {
              const part = await reader.read()
              if (part.done) break
              bytes += part.value.length
              if (bytes > maxResponseBytes) { controller.abort(); throw new Error('Tool SDK response exceeded maxResponseBytes') }
              chunks.push(Buffer.from(part.value))
            }
            detail.response = { status: response.status, headers: Object.fromEntries(response.headers), body: Buffer.concat(chunks).toString('utf8') }
            await onExchange(detail)
          } finally { clearTimeout(timer); reader?.releaseLock() }
        })().catch(error => { errors.push(error) }))
        return response
      } catch (error) { clearTimeout(timer); throw error }
    }
    const options = { baseUrl, apiKey, model, stream, timeoutMs, fetch: capture, signal }
    let failure
    try {
      let calls, text
      if (name === 'openai') ({ calls, text } = await openAITurns(protocol, options, expectedCalls))
      else if (name === '@anthropic-ai/sdk') ({ calls, text } = await anthropicTurns(options, expectedCalls))
      else if (name === '@google/genai') ({ calls, text } = await geminiTurns(options, expectedCalls))
      else ({ calls, text } = await aiTurns(protocol, options, expectedCalls))
      assert.ok(text.includes(expectedText), 'SDK did not receive the second answer')
      evidence.rounds = 2
      evidence.calls = calls
    } catch (error) { failure = error }
    await Promise.all(pending)
    failure ||= errors[0]
    if (failure) { failure.sdk = used; throw failure }
    assert.equal(evidence.requests, 2, 'SDK must complete exactly two HTTP requests')
  }
  return used
}

async function openAITurns(protocol, o, expected) {
  const client = new OpenAI({ apiKey: o.apiKey, baseURL: `${o.baseUrl}/v1`, maxRetries: 0, timeout: o.timeoutMs, fetch: o.fetch })
  if (protocol === 'chat') {
    const params = { model: o.model, max_completion_tokens: 64, tools: [{ type: 'function', function: { name: 'lookup', parameters: schema } }], messages: [{ role: 'user', content: prompt }] }
    const call = () => o.stream ? client.chat.completions.stream(params, { signal: o.signal }).finalChatCompletion() : client.chat.completions.create(params, { signal: o.signal })
    const first = await call(), message = first.choices[0].message
    const calls = (message.tool_calls || []).map(c => ({ id: c.id, name: c.function.name, input: JSON.parse(c.function.arguments) }))
    checkCalls(calls, expected)
    const { parsed: _parsed, ...history } = message
    history.tool_calls = (message.tool_calls || []).map(c => {
      const { parsed_arguments: _arguments, ...fn } = c.function
      return { ...c, function: fn }
    })
    params.messages.push(history, ...calls.map(c => ({ role: 'tool', tool_call_id: c.id, content: JSON.stringify(resultFor(c.id)) })))
    const second = await call()
    assert.equal(second.choices[0].finish_reason, 'stop')
    return { calls, text: second.choices[0].message.content || '' }
  }
  const params = { model: o.model, max_output_tokens: 64, store: false, tools: [{ type: 'function', name: 'lookup', parameters: schema }], input: [{ role: 'user', content: prompt }] }
  const call = () => o.stream ? client.responses.stream(params, { signal: o.signal }).finalResponse() : client.responses.create(params, { signal: o.signal })
  const first = await call()
  const calls = first.output.filter(c => c.type === 'function_call').map(c => ({ id: c.call_id, name: c.name, input: JSON.parse(c.arguments) }))
  checkCalls(calls, expected)
  // finalResponse() adds SDK-only parsed/parsed_arguments properties. Use the
  // pinned SDK's history normalizer, and reject any omitted output item here.
  const history = toResponseInputItems(first.output)
  assert.equal(history.length, first.output.length, 'SDK history normalization omitted an output item')
  params.input.push(...history, ...calls.map(c => ({ type: 'function_call_output', call_id: c.id, output: JSON.stringify(resultFor(c.id)) })))
  const second = await call()
  assert.equal(second.status, 'completed')
  return { calls, text: second.output.filter(p => p.type === 'message').flatMap(p => p.content).filter(p => p.type === 'output_text').map(p => p.text).join('') }
}

async function anthropicTurns(o, expected) {
  const client = new Anthropic({ apiKey: o.apiKey, baseURL: o.baseUrl, maxRetries: 0, timeout: o.timeoutMs, fetch: o.fetch })
  const params = { model: o.model, max_tokens: 64, tools: [{ name: 'lookup', input_schema: schema }], messages: [{ role: 'user', content: prompt }] }
  const call = () => o.stream ? client.messages.stream(params, { signal: o.signal }).finalMessage() : client.messages.create(params, { signal: o.signal })
  const first = await call()
  const calls = first.content.filter(p => p.type === 'tool_use').map(p => ({ id: p.id, name: p.name, input: p.input }))
  checkCalls(calls, expected)
  params.messages.push({ role: 'assistant', content: first.content }, { role: 'user', content: calls.map(c => ({ type: 'tool_result', tool_use_id: c.id, content: JSON.stringify(resultFor(c.id)) })) })
  const second = await call()
  assert.equal(second.stop_reason, 'end_turn')
  return { calls, text: second.content.filter(p => p.type === 'text').map(p => p.text).join('') }
}

async function geminiTurns(o, expected) {
  const client = new GoogleGenAI({ apiKey: o.apiKey, httpOptions: { baseUrl: o.baseUrl, fetch: o.fetch, timeout: o.timeoutMs, retryOptions: { attempts: 1 } } })
  const params = { model: o.model, contents: [{ role: 'user', parts: [{ text: prompt }] }], config: { tools: [{ functionDeclarations: [{ name: 'lookup', parameters: schema }] }], maxOutputTokens: 64, abortSignal: o.signal } }
  const call = async () => {
    if (!o.stream) return (await client.models.generateContent(params)).candidates[0].content.parts
    const parts = []
    for await (const event of await client.models.generateContentStream(params)) {
      assert.ok(!event.error, 'Gemini SDK returned an error event')
      parts.push(...(event.candidates?.[0]?.content?.parts || []))
    }
    return parts
  }
  const first = await call()
  const calls = first.filter(p => p.functionCall).map(p => ({ id: p.functionCall.id, name: p.functionCall.name, input: p.functionCall.args }))
  checkCalls(calls, expected)
  params.contents.push({ role: 'model', parts: first }, { role: 'user', parts: calls.map(c => ({ functionResponse: { id: c.id, name: c.name, response: resultFor(c.id) } })) })
  return { calls, text: (await call()).filter(p => !p.thought).map(p => p.text || '').join('') }
}

async function aiTurns(protocol, o, expected) {
  const provider = protocol === 'anthropic' ? createAnthropic({ apiKey: o.apiKey, baseURL: `${o.baseUrl}/v1`, fetch: o.fetch }) : createOpenAI({ apiKey: o.apiKey, baseURL: `${o.baseUrl}/v1`, fetch: o.fetch })
  const model = protocol === 'anthropic' ? provider(o.model) : protocol === 'chat' ? provider.chat(o.model) : provider.responses(o.model)
  const params = { maxOutputTokens: 64, abortSignal: o.signal, tools: [{ type: 'function', name: 'lookup', inputSchema: schema }], prompt: [{ role: 'user', content: [{ type: 'text', text: prompt }] }], providerOptions: protocol === 'responses' ? { openai: { store: false } } : undefined }
  const call = async () => {
    if (!o.stream) return (await model.doGenerate(params)).content
    const content = [], text = new Map(), inputs = new Map()
    let finished = false
    for await (const event of (await model.doStream(params)).stream) {
      if (event.type === 'error') throw event.error
      if (event.type === 'text-start') { const part = { type: 'text', text: '' }; text.set(event.id, part); content.push(part) }
      if (event.type === 'text-delta') { assert.ok(text.has(event.id), 'SDK text delta has no start'); text.get(event.id).text += event.delta }
      if (event.type === 'tool-input-start') { assert.ok(!inputs.has(event.id), 'SDK repeated a tool start'); inputs.set(event.id, { name: event.toolName, input: '' }) }
      if (event.type === 'tool-input-delta') { assert.ok(inputs.has(event.id), 'SDK tool delta has no start'); inputs.get(event.id).input += event.delta }
      if (event.type === 'tool-call') {
        assert.ok(inputs.has(event.toolCallId), 'SDK tool call has no input start')
        assert.equal(inputs.get(event.toolCallId).name, event.toolName)
        assert.deepEqual(JSON.parse(inputs.get(event.toolCallId).input), JSON.parse(event.input), 'SDK tool deltas disagree with final call')
        content.push(event)
      }
      if (event.type === 'finish') finished = true
    }
    assert.ok(finished, 'SDK stream did not finish')
    return content
  }
  const first = await call()
  const calls = first.filter(p => p.type === 'tool-call').map(p => ({ id: p.toolCallId, name: p.toolName, input: JSON.parse(p.input) }))
  checkCalls(calls, expected)
  params.prompt.push({ role: 'assistant', content: first.map(p => p.type === 'tool-call' ? { ...p, input: JSON.parse(p.input) } : p) }, { role: 'tool', content: calls.map(c => ({ type: 'tool-result', toolCallId: c.id, toolName: c.name, output: { type: 'json', value: resultFor(c.id) } })) })
  const second = await call()
  assert.equal(second.filter(p => p.type === 'tool-call').length, 0, 'SDK returned more tools instead of the second answer')
  return { calls, text: second.filter(p => p.type === 'text').map(p => p.text).join('') }
}
