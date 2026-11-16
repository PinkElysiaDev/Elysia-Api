import assert from 'node:assert/strict'
import test from 'node:test'
import { assertObservation, observeNative, observeVercel, parseArguments } from './observe.mjs'
import { describeCapture, summarizeCoverage } from './fixtures.mjs'

const textExpectation = { text: 'hi', tools: [] }
const toolExpectation = { text: '', tools: [{ id: 'call', name: 'lookup', args: { n: 1 } }] }
const chat = (delta, finish_reason = null) => ({ object: 'chat.completion.chunk', choices: [{ index: 0, delta, finish_reason }] })
const response = (output) => ({ status: 'completed', output })
const message = (text) => ({ type: 'message', content: [{ type: 'output_text', text }] })
const anthropicStart = { type: 'message_start', message: { content: [] } }
const anthropicEnd = [{ type: 'message_delta', delta: { stop_reason: 'end_turn' } }, { type: 'message_stop' }]
const gemini = (parts, finishReason) => ({ candidates: [{ content: { parts }, finishReason }] })
const splitStreams = new Map([
  ['openai-chat-completions', [chat({ content: 'h' }), chat({ content: 'i' }, 'stop')]],
  ['openai-responses', [{ type: 'response.output_text.delta', delta: 'h' }, { type: 'response.output_text.delta', delta: 'i' }, { type: 'response.completed', response: response([message('hi')]) }]],
  ['anthropic-messages', [anthropicStart, { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } }, { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'h' } }, { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'i' } }, { type: 'content_block_stop', index: 0 }, ...anthropicEnd]],
  ['google-generate-content', [gemini([{ text: 'h' }]), gemini([{ text: 'i' }], 'STOP')]],
])

for (const [protocol, events] of splitStreams) {
  test(`${protocol} assembles text and requires successful terminal`, () => {
    assertObservation(observeNative(protocol, events, true), textExpectation)
    assert.throws(() => observeNative(protocol, events.slice(0, -1), true), /finish|complet|terminal/)
    assert.throws(() => observeNative(protocol, [...events, { type: 'error', error: 'hi lookup' }], true), /error event/)
  })
}

test('Gemini usage-only frames preserve whole-stream content, tools and terminal checks', () => {
  const usage = { responseId: 'r', modelVersion: 'm', usageMetadata: { promptTokenCount: 0, candidatesTokenCount: 0, totalTokenCount: 0 }, sdkHttpResponse: { headers: { 'content-type': 'text/event-stream' } } }
  const text = [usage, gemini([{ text: 'h' }]), { ...usage, candidates: [] }, gemini([{ text: 'i' }], 'STOP'), usage]
  assertObservation(observeNative('google-generate-content', text, true), textExpectation)
  const tools = [gemini([{ functionCall: { id: 'call', name: 'lookup', args: { n: 1 } } }]), usage, gemini([], 'STOP')]
  assertObservation(observeNative('google-generate-content', tools, true), toolExpectation)
  assert.throws(() => observeNative('google-generate-content', [usage], true), /finish/)
  assert.throws(() => observeNative('google-generate-content', [gemini([{ text: 'hi' }]), usage], true), /finish/)
  assert.throws(() => observeNative('google-generate-content', usage, false), /complete response/)
  assert.throws(() => observeNative('google-generate-content', [...text, gemini([{ text: 'extra' }])], true), /after Gemini finish/)
})

test('Gemini candidate-less frames reject unrecognized payloads and invalid usage', () => {
  const complete = gemini([{ text: 'hi' }], 'STOP')
  for (const frame of [
    {}, { candidates: [] }, { candidates: null }, { usageMetadata: null }, { usageMetadata: {} }, { usageMetadata: [] },
    { usageMetadata: { futureTokens: 1 } }, { usageMetadata: { totalTokenCount: -1 } },
    { usageMetadata: { totalTokenCount: 0.5 } }, { usageMetadata: { totalTokenCount: '1' } },
    { usageMetadata: { totalTokenCount: 1 }, content: { parts: [{ text: 'hi' }] } },
    { usageMetadata: { totalTokenCount: 1 }, futureEvent: true },
    { usageMetadata: { totalTokenCount: 1 }, promptFeedback: { blockReason: 'SAFETY' } },
    { usageMetadata: { totalTokenCount: 1 }, error: { message: 'failed' } },
  ]) assert.throws(() => observeNative('google-generate-content', [frame, complete], true))
  assert.throws(() => observeNative('google-generate-content', [{ candidates: [...complete.candidates, ...complete.candidates] }], true), /one Gemini candidate/)
  assert.throws(() => observeNative('google-generate-content', [{ candidates: [{ ...complete.candidates[0], index: 1 }] }], true), /candidate index/)
})

test('native text ignores metadata bait in all JSON shapes', () => {
  const replies = [
    ['openai-chat-completions', { object: 'chat.completion', choices: [{ index: 0, message: { content: 'wrong' }, finish_reason: 'stop' }], metadata: 'hi lookup' }],
    ['openai-responses', { ...response([message('wrong')]), metadata: 'hi lookup', output_text: 'hi' }],
    ['anthropic-messages', { type: 'message', content: [{ type: 'text', text: 'wrong' }], stop_reason: 'end_turn', metadata: 'hi lookup' }],
    ['google-generate-content', { ...gemini([{ text: 'wrong' }], 'STOP'), metadata: 'hi lookup' }],
  ]
  for (const [protocol, reply] of replies) assert.throws(() => assertObservation(observeNative(protocol, reply, false), textExpectation), /deep-equal/)
})

test('native unknown events and unsuccessful terminal states remain failures', () => {
  assert.throws(() => observeNative('openai-chat-completions', [{ ...chat({ content: 'hi' }, 'stop'), object: 'future' }], true), /unexpected Chat object/)
  assert.throws(() => observeNative('openai-chat-completions', [chat({ content: 'hi' }, 'content_filter')], true), /unsuccessful/)
  assert.throws(() => observeNative('openai-responses', [{ type: 'response.future' }], true), /unexpected Responses event/)
  assert.throws(() => observeNative('openai-responses', { status: 'incomplete', output: [message('hi')] }, false), /complete successfully/)
  assert.throws(() => observeNative('anthropic-messages', [anthropicStart, { type: 'future' }], true), /unexpected Anthropic event/)
  assert.throws(() => observeNative('anthropic-messages', { type: 'message', stop_reason: 'max_tokens', content: [{ type: 'text', text: 'hi' }] }, false), /unsuccessful/)
  assert.throws(() => observeNative('google-generate-content', [gemini([{ unknown: 'hi' }], 'STOP')], true), /unexpected Gemini/)
  assert.throws(() => observeNative('google-generate-content', gemini([{ text: 'hi' }], 'SAFETY'), false), /finish successfully/)
})

test('native tools compare ID, name and parsed argument association', () => {
  const builders = new Map([
    ['openai-chat-completions', (call) => ({ object: 'chat.completion', choices: [{ index: 0, message: { content: null, tool_calls: [{ id: call.id, type: 'function', function: { name: call.name, arguments: JSON.stringify(call.args) } }] }, finish_reason: 'tool_calls' }] })],
    ['openai-responses', (call) => response([{ type: 'function_call', call_id: call.id, name: call.name, arguments: JSON.stringify(call.args) }])],
    ['anthropic-messages', (call) => ({ type: 'message', content: [{ type: 'tool_use', id: call.id, name: call.name, input: call.args }], stop_reason: 'tool_use' })],
    ['google-generate-content', (call) => gemini([{ functionCall: call }], 'STOP')],
  ])
  for (const [protocol, build] of builders) {
    assertObservation(observeNative(protocol, build(toolExpectation.tools[0]), false), toolExpectation)
    for (const change of [{ id: '' }, { id: 'other' }, { name: 'other' }, { args: { n: 2 } }, { args: undefined }]) {
      assert.throws(() => assertObservation(observeNative(protocol, build({ ...toolExpectation.tools[0], ...change }), false), toolExpectation))
    }
  }
})

test('Chat keeps reverse argument fragments attached to their own tool IDs', () => {
  const events = [chat({ tool_calls: [{ index: 0, id: 'c1', function: { name: 'lookup', arguments: '' } }, { index: 1, id: 'c2', function: { name: 'lookup', arguments: '' } }] }), chat({ tool_calls: [{ index: 1, function: { arguments: '{"n":2}' } }, { index: 0, function: { arguments: '{"n":1}' } }] }, 'tool_calls')]
  assertObservation(observeNative('openai-chat-completions', events, true), { text: '', tools: [{ id: 'c1', name: 'lookup', args: { n: 1 } }, { id: 'c2', name: 'lookup', args: { n: 2 } }] })
})

test('Responses final snapshot cannot conceal lost text or tool deltas', () => {
  assert.throws(() => observeNative('openai-responses', [{ type: 'response.completed', response: response([message('hi')]) }], true), /deltas differ/)
  const events = [{ type: 'response.output_item.added', item: { id: 'item', type: 'function_call', call_id: 'call', name: 'lookup', arguments: '' } }, { type: 'response.function_call_arguments.delta', item_id: 'item', delta: '{"n":1}' }, { type: 'response.completed', response: response([{ type: 'function_call', call_id: 'call', name: 'lookup', arguments: '{"n":1}' }]) }]
  assertObservation(observeNative('openai-responses', events, true), toolExpectation)
  events[1].delta = '{"n":2}'
  assert.throws(() => observeNative('openai-responses', events, true), /deltas differ/)
})

for (const version of ['v2', 'v4']) {
  const finishReason = version === 'v2' ? 'stop' : { unified: 'stop', raw: 'end_turn' }
  test(`Vercel ${version} aggregates text but rejects metadata bait and missing/error/unknown terminal`, () => {
    const events = [{ type: 'text-start', id: 'text' }, { type: 'text-delta', id: 'text', delta: 'h' }, { type: 'text-delta', id: 'text', delta: 'i' }, { type: 'text-end', id: 'text' }, { type: 'finish', finishReason }]
    assertObservation(observeVercel(events, true, version), textExpectation)
    assert.throws(() => observeVercel(events.slice(0, -1), true, version), /finish/)
    assert.throws(() => observeVercel([...events, { type: 'error', error: 'hi lookup' }], true, version), /error event/)
    assert.throws(() => observeVercel([{ type: 'future' }, ...events], true, version), /unexpected/)
    assert.throws(() => assertObservation(observeVercel({ content: [], finishReason, providerMetadata: { text: 'hi', toolName: 'lookup' } }, false, version), textExpectation), /deep-equal/)
  })
  test(`Vercel ${version} validates finished tools and their incremental inputs`, () => {
    const finish = version === 'v2' ? 'tool-calls' : { unified: 'tool-calls', raw: 'tool_use' }
    const events = [{ type: 'tool-input-start', id: 'call', toolName: 'lookup' }, { type: 'tool-input-delta', id: 'call', delta: '{"n":' }, { type: 'tool-input-delta', id: 'call', delta: '1}' }, { type: 'tool-input-end', id: 'call' }, { type: 'tool-call', toolCallId: 'call', toolName: 'lookup', input: '{"n":1}' }, { type: 'finish', finishReason: finish }]
    assertObservation(observeVercel(events, true, version), toolExpectation)
    assertObservation(observeVercel({ content: [events[4]], finishReason: finish }, false, version), toolExpectation)
    for (const change of [{ toolCallId: '' }, { toolCallId: 'wrong' }, { toolName: 'wrong' }, { input: '{"n":2}' }, { input: undefined }]) {
      assert.throws(() => observeVercel([...events.slice(0, 4), { ...events[4], ...change }, events[5]], true, version))
    }
  })
}

test('large integer arguments remain exact; an already rounded SDK object fails', () => {
  assert.deepEqual(parseArguments('{"n":9007199254740993}'), { n: 9007199254740993n })
  const expected = { text: '', tools: [{ id: 'call', name: 'lookup', args: { n: 9007199254740993n } }] }
  const reply = { type: 'message', stop_reason: 'tool_use', content: [{ type: 'tool_use', id: 'call', name: 'lookup', input: JSON.parse('{"n":9007199254740993}') }] }
  assert.throws(() => assertObservation(observeNative('anthropic-messages', reply, false), expected), /deep-equal/)
})

test('capture identity and exact subset coverage cannot masquerade as a full matrix', () => {
  const fixture = { upstream: 'openai-chat-completions', ingress: 'openai-responses', stream: true, tool: true, body: 'captured' }
  const capture = describeCapture('parallel-tool-openai-chat-completions-openai-responses-true-false.json', fixture)
  assert.equal(capture.expected.text, 'hi')
  assert.equal(capture.expected.tools.length, 2)
  const coverage = summarizeCoverage([capture])
  assert(!coverage.completeCurrent && coverage.missingCartesian.length === 159)
  assert.equal(coverage.missingCurrent.length, 159)
  assert.equal(coverage.completeCartesian, false)
  assert.throws(() => summarizeCoverage([capture, capture]), /duplicate/)
  assert.throws(() => describeCapture(capture.file, { ...fixture, tool: false }), /filename/)
})
