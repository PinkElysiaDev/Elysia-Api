import assert from 'node:assert/strict'

const successfulReasons = new Set(['stop', 'tool-calls'])
const responseLifecycleEvents = new Set(['response.created', 'response.in_progress', 'response.content_part.added', 'response.content_part.done', 'response.output_text.done', 'response.output_item.done', 'response.function_call_arguments.done'])
const geminiUsageEnvelopeFields = new Set(['candidates', 'createTime', 'modelVersion', 'responseId', 'usageMetadata', 'sdkHttpResponse'])
const geminiUsageCounters = new Set(['promptTokenCount', 'candidatesTokenCount', 'totalTokenCount', 'cachedContentTokenCount', 'thoughtsTokenCount', 'toolUsePromptTokenCount'])

function readString(value, label) {
  assert.equal(typeof value, 'string', `${label} must be a string`)
  return value
}

function readIdentifier(value, label) {
  assert(readString(value, label).length > 0, `${label} is missing`)
  return value
}

/** Parse tool arguments without rounding integers that exceed JavaScript's safe range. */
export function parseArguments(value) {
  return JSON.parse(readString(value, 'tool arguments'), (_key, entry, context) => {
    if (typeof entry === 'number' && Number.isInteger(entry) && !Number.isSafeInteger(entry)) {
      assert(context?.source && /^-?\d+$/.test(context.source), 'unsafe numeric argument needs an exact integer token')
      return BigInt(context.source)
    }
    return entry
  })
}

function buildToolCall(id, name, args) {
  readIdentifier(id, 'tool ID')
  readIdentifier(name, 'tool name')
  assert(args && typeof args === 'object' && !Array.isArray(args), 'tool arguments must be an object')
  return { id, name, args }
}

function buildObservation(text, tools, reason) {
  assert(successfulReasons.has(reason), `missing or unsuccessful finish reason: ${reason}`)
  assert.equal(new Set(tools.map(tool => tool.id)).size, tools.length, 'duplicate tool ID')
  return { text, tools: [...tools].sort((left, right) => left.id.localeCompare(right.id)), reason }
}

function checkEvent(event) {
  assert(event && typeof event === 'object' && !Array.isArray(event), 'invalid SDK event')
  assert(!event.error && event.type !== 'error', 'SDK returned an error event')
}

function observeChat(reply, isStream) {
  let text = '', finish
  const tools = new Map()
  for (const frame of isStream ? reply : [reply]) {
    checkEvent(frame)
    assert.equal(frame.object, isStream ? 'chat.completion.chunk' : 'chat.completion', 'unexpected Chat object')
    assert(Array.isArray(frame.choices) && frame.choices.length <= 1, 'audit requires one Chat choice')
    for (const choice of frame.choices) {
      assert.equal(choice.index, 0, 'unexpected Chat choice index')
      const message = isStream ? choice.delta : choice.message
      assert(message && typeof message === 'object', 'missing Chat message')
      assert(!message.refusal, 'Chat refused the fixture')
      if (message.content != null) text += readString(message.content, 'Chat text')
      for (const [position, call] of (message.tool_calls ?? []).entries()) {
        const index = isStream ? call.index : position
        assert(Number.isInteger(index) && index >= 0, 'missing Chat tool index')
        let pending = tools.get(index)
        if (!pending) {
          pending = { id: readIdentifier(call.id, 'tool ID'), name: '', arguments: '' }
          tools.set(index, pending)
        } else if (call.id !== undefined) assert.equal(call.id, pending.id, 'Chat tool ID changed')
        if (call.type !== undefined) assert.equal(call.type, 'function', 'unsupported Chat tool type')
        assert(call.function && typeof call.function === 'object', 'missing Chat function')
        if (call.function.name !== undefined) pending.name += readString(call.function.name, 'tool name')
        if (call.function.arguments !== undefined) pending.arguments += readString(call.function.arguments, 'tool arguments')
      }
      if (choice.finish_reason != null) {
        assert.equal(finish, undefined, 'duplicate Chat finish')
        finish = choice.finish_reason === 'tool_calls' ? 'tool-calls' : choice.finish_reason
      }
    }
  }
  return buildObservation(text, [...tools.values()].map(call => buildToolCall(call.id, call.name, parseArguments(call.arguments))), finish)
}

function observeResponse(reply) {
  checkEvent(reply)
  assert.equal(reply.status, 'completed', 'Responses did not complete successfully')
  let text = ''
  const tools = []
  assert(Array.isArray(reply.output), 'Responses output is missing')
  for (const item of reply.output) {
    if (item.type === 'function_call') {
      tools.push(buildToolCall(item.call_id, item.name, parseArguments(item.arguments)))
    } else {
      assert.equal(item.type, 'message', `unexpected Responses item: ${item.type}`)
      assert(Array.isArray(item.content), 'Responses message content is missing')
      for (const part of item.content) {
        assert.equal(part.type, 'output_text', `unexpected Responses content: ${part.type}`)
        text += readString(part.text, 'Responses text')
      }
    }
  }
  return buildObservation(text, tools, tools.length ? 'tool-calls' : 'stop')
}

function observeResponseStream(events) {
  let text = '', terminal
  const tools = new Map()
  for (const event of events) {
    checkEvent(event)
    assert.equal(terminal, undefined, 'event after Responses completion')
    if (event.type === 'response.output_text.delta') text += readString(event.delta, 'Responses text delta')
    else if (event.type === 'response.output_item.added') {
      if (event.item.type === 'function_call') {
        assert(!tools.has(event.item.id), 'duplicate Responses item ID')
        tools.set(readIdentifier(event.item.id, 'Responses item ID'), { id: event.item.call_id, name: event.item.name, arguments: readString(event.item.arguments, 'initial tool arguments') })
      } else assert.equal(event.item.type, 'message', `unexpected Responses item: ${event.item.type}`)
    } else if (event.type === 'response.function_call_arguments.delta') {
      const call = tools.get(event.item_id)
      assert(call, 'Responses argument delta has no tool owner')
      call.arguments += readString(event.delta, 'tool argument delta')
    } else if (event.type === 'response.completed') terminal = observeResponse(event.response)
    else assert(responseLifecycleEvents.has(event.type), `unexpected Responses event: ${event.type}`)
  }
  assert(terminal, 'missing Responses completion')
  const streamed = buildObservation(text, [...tools.values()].map(call => buildToolCall(call.id, call.name, parseArguments(call.arguments))), terminal.reason)
  assert.deepEqual(streamed, terminal, 'Responses deltas differ from completed output')
  return streamed
}

function observeAnthropic(reply) {
  checkEvent(reply)
  assert.equal(reply.type, 'message', 'unexpected Anthropic response type')
  let text = ''
  const tools = []
  assert(Array.isArray(reply.content), 'Anthropic content is missing')
  for (const block of reply.content) {
    if (block.type === 'text') text += readString(block.text, 'Anthropic text')
    else {
      assert.equal(block.type, 'tool_use', `unexpected Anthropic block: ${block.type}`)
      tools.push(buildToolCall(block.id, block.name, block.input))
    }
  }
  return buildObservation(text, tools, reply.stop_reason === 'tool_use' ? 'tool-calls' : reply.stop_reason === 'end_turn' ? 'stop' : reply.stop_reason)
}

function observeAnthropicStream(events) {
  const blocks = new Map()
  let finish, hasStarted = false, hasStopped = false
  for (const event of events) {
    checkEvent(event)
    assert(!hasStopped, 'event after Anthropic message_stop')
    switch (event.type) {
      case 'message_start':
        assert(!hasStarted, 'duplicate Anthropic message_start')
        assert.deepEqual(event.message.content, [], 'unexpected initial Anthropic content')
        hasStarted = true
        break
      case 'content_block_start': {
        assert(hasStarted && !blocks.has(event.index), 'invalid Anthropic block start')
        const block = event.content_block
        assert(['text', 'tool_use'].includes(block.type), `unexpected Anthropic block: ${block.type}`)
        if (block.type === 'text') readString(block.text, 'initial Anthropic text')
        blocks.set(event.index, { ...block, arguments: '', hasArguments: false, hasStopped: false })
        break
      }
      case 'content_block_delta': {
        const block = blocks.get(event.index)
        assert(block && !block.hasStopped, 'Anthropic delta has no open block')
        if (event.delta.type === 'text_delta') {
          assert.equal(block.type, 'text', 'text delta changed its block kind')
          block.text += readString(event.delta.text, 'Anthropic text delta')
        } else {
          assert.equal(event.delta.type, 'input_json_delta', 'unexpected Anthropic delta')
          assert.equal(block.type, 'tool_use', 'argument delta changed its block kind')
          block.arguments += readString(event.delta.partial_json, 'Anthropic argument delta')
          block.hasArguments = true
        }
        break
      }
      case 'content_block_stop': {
        const block = blocks.get(event.index)
        assert(block && !block.hasStopped, 'invalid Anthropic block stop')
        block.hasStopped = true
        if (block.type === 'tool_use' && block.hasArguments) block.input = parseArguments(block.arguments)
        break
      }
      case 'message_delta':
        if (event.delta.stop_reason != null) {
          assert.equal(finish, undefined, 'duplicate Anthropic finish')
          finish = event.delta.stop_reason
        }
        break
      case 'message_stop': hasStopped = true; break
      case 'ping': break
      default: assert.fail(`unexpected Anthropic event: ${event.type}`)
    }
  }
  assert(hasStarted && hasStopped && [...blocks.values()].every(block => block.hasStopped), 'incomplete Anthropic stream')
  return observeAnthropic({ type: 'message', content: [...blocks.values()], stop_reason: finish })
}

function checkGeminiUsageFrame(frame) {
  for (const field of Object.keys(frame)) assert(geminiUsageEnvelopeFields.has(field), `unexpected Gemini usage frame field: ${field}`)
  const usage = frame.usageMetadata
  assert(usage && typeof usage === 'object' && !Array.isArray(usage) && Object.keys(usage).length > 0, 'candidate-less Gemini frame requires usage counters')
  for (const [field, count] of Object.entries(usage)) {
    assert(geminiUsageCounters.has(field), `unexpected Gemini usage counter: ${field}`)
    assert(Number.isInteger(count) && count >= 0, `invalid Gemini usage counter: ${field}`)
  }
  for (const field of ['responseId', 'modelVersion']) {
    if (frame[field] !== undefined) readIdentifier(frame[field], `Gemini ${field}`)
  }
  if (frame.createTime != null) readString(frame.createTime, 'Gemini creation time')
}

function observeGemini(reply, isStream) {
  let text = '', finish
  const tools = []
  for (const frame of isStream ? reply : [reply]) {
    checkEvent(frame)
    assert(!frame.promptFeedback?.blockReason, 'Gemini blocked the fixture')
    if (frame.candidates === undefined || Array.isArray(frame.candidates) && frame.candidates.length === 0) {
      assert(isStream, 'audit requires one Gemini candidate in a complete response')
      checkGeminiUsageFrame(frame)
      continue
    }
    assert(Array.isArray(frame.candidates) && frame.candidates.length === 1, 'audit requires one Gemini candidate')
    assert.equal(finish, undefined, 'candidate after Gemini finish')
    const candidate = frame.candidates[0]
    if (candidate.index !== undefined) assert.equal(candidate.index, 0, 'unexpected Gemini candidate index')
    for (const part of candidate.content?.parts ?? []) {
      assert(!part.thought, 'unexpected reasoning in visible-text fixture')
      if (part.text !== undefined) text += readString(part.text, 'Gemini text')
      else {
        assert(part.functionCall, 'unexpected Gemini content part')
        const call = part.functionCall
        tools.push(buildToolCall(call.id, call.name, call.args))
      }
    }
    if (candidate.finishReason !== undefined) {
      assert.equal(candidate.finishReason, 'STOP', 'Gemini did not finish successfully')
      finish = 'stop'
    }
  }
  return buildObservation(text, tools, finish === 'stop' && tools.length ? 'tool-calls' : finish)
}

/** Observe visible text, complete tools and successful termination from a pinned native SDK. */
export function observeNative(protocol, reply, isStream) {
  if (isStream) assert(Array.isArray(reply) && reply.length > 0, 'native SDK returned no events')
  switch (protocol) {
    case 'openai-chat-completions': return observeChat(reply, isStream)
    case 'openai-responses': return isStream ? observeResponseStream(reply) : observeResponse(reply)
    case 'anthropic-messages': return isStream ? observeAnthropicStream(reply) : observeAnthropic(reply)
    case 'google-generate-content': return observeGemini(reply, isStream)
    default: assert.fail(`unsupported native protocol: ${protocol}`)
  }
}

function readVercelFinish(reason, version) {
  return version === 'v2' ? reason : reason?.unified
}

/** Observe Vercel v2/v4 content using their explicit model specification version. */
export function observeVercel(reply, isStream, version) {
  assert(['v2', 'v4'].includes(version), `unsupported Vercel specification: ${version}`)
  let text = '', finish
  const tools = [], inputs = new Map(), textBlocks = new Set()
  const parts = isStream ? reply : reply.content
  assert(Array.isArray(parts), 'Vercel content is missing')
  for (const part of parts) {
    checkEvent(part)
    assert.equal(finish, undefined, 'event after Vercel finish')
    switch (part.type) {
      case 'text':
        assert(!isStream, 'unexpected complete text in Vercel stream')
        text += readString(part.text, 'Vercel text')
        break
      case 'text-start':
        assert(!textBlocks.has(part.id), 'duplicate Vercel text start')
        textBlocks.add(readIdentifier(part.id, 'text ID'))
        break
      case 'text-delta':
        assert(textBlocks.has(part.id), 'Vercel delta has no text owner')
        text += readString(part.delta, 'Vercel text delta')
        break
      case 'text-end': assert(textBlocks.delete(part.id), 'Vercel text end has no owner'); break
      case 'tool-input-start':
        assert(!inputs.has(part.id), 'duplicate Vercel tool start')
        inputs.set(readIdentifier(part.id, 'tool ID'), { name: readIdentifier(part.toolName, 'tool name'), arguments: '', hasEnded: false })
        break
      case 'tool-input-delta': {
        const input = inputs.get(part.id)
        assert(input && !input.hasEnded, 'Vercel tool delta has no open owner')
        input.arguments += readString(part.delta, 'Vercel tool delta')
        break
      }
      case 'tool-input-end': {
        const input = inputs.get(part.id)
        assert(input && !input.hasEnded, 'Vercel tool end has no open owner')
        input.hasEnded = true
        break
      }
      case 'tool-call': {
        const call = buildToolCall(part.toolCallId, part.toolName, parseArguments(part.input))
        if (isStream) {
          const input = inputs.get(call.id)
          assert(input?.hasEnded, 'Vercel completed call has no finished input')
          assert.equal(call.name, input.name, 'Vercel tool name changed')
          assert.deepEqual(call.args, parseArguments(input.arguments), 'Vercel tool arguments changed')
        }
        tools.push(call)
        break
      }
      case 'finish':
        finish = readVercelFinish(part.finishReason, version)
        break
      case 'custom':
        assert(isStream && version === 'v4' && part.kind === 'anthropic.message_start', `unexpected Vercel custom content: ${part.kind}`)
        break
      case 'stream-start': case 'response-metadata': break
      default: assert.fail(`unexpected Vercel content: ${part.type}`)
    }
  }
  assert.equal(textBlocks.size, 0, 'unfinished Vercel text')
  if (isStream) assert.equal(inputs.size, tools.length, 'unfinished Vercel tool calls')
  return buildObservation(text, tools, isStream ? finish : readVercelFinish(reply.finishReason, version))
}

/** Compare semantic content with an independent producer expectation, including tool associations. */
export function assertObservation(actual, expected, label) {
  assert.deepEqual(actual, buildObservation(expected.text, expected.tools, expected.tools.length ? 'tool-calls' : 'stop'), label)
}
