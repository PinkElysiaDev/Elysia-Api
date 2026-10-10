import test from 'node:test'
import assert from 'node:assert/strict'
import { protocols, endpoint, parseSSE, inspectReply, requestBody, followupBody } from '../src/protocols.mjs'
import { fixtureSignature, replyWire } from './fixtures.mjs'

for (const protocol of protocols) test(`${protocol} automatic tools retain forced-tool coverage as a separate scenario`, () => {
  const forced = requestBody(protocol, 'test', false, 'tools', 'marker', 32768)
  const auto = requestBody(protocol, 'test', false, 'tools_auto', 'marker', 32768)
  assert.deepEqual(auto.tools, forced.tools)
  assert.notDeepEqual(auto.tool_choice ?? auto.toolConfig, forced.tool_choice ?? forced.toolConfig)
  assert.deepEqual(auto.tool_choice ?? auto.toolConfig, protocol === 'gemini' ? { functionCallingConfig: { mode: 'AUTO' } } : protocol === 'anthropic' ? { type: 'auto' } : 'auto')
  const reply = inspectReply(protocol, replyWire(protocol, '', true, false), false)
  const second = followupBody(protocol, auto, reply, 'tools_auto', 'RESULT_ONLY')
  assert.ok(JSON.stringify(second).includes('RESULT_ONLY'))
  assert.ok(JSON.stringify(second).includes('call_1'))
  assert.ok(JSON.stringify(second).includes(fixtureSignature))
})

for (const protocol of protocols) for (const stream of [false, true]) {
  test(`${protocol} ${stream ? 'SSE' : 'JSON'} preserves tool IDs and opaque continuation fields`, () => {
    const reply = inspectReply(protocol, replyWire(protocol, '', true, stream), stream)
    assert.deepEqual(reply.issues, [])
    assert.deepEqual(reply.calls, [{ id: 'call_1', name: 'audit_echo', args: { value: 7 } }])
    const first = requestBody(protocol, 'test', stream, 'tools', 'RESULT_ONLY', 512)
    assert.ok(!JSON.stringify(first).includes('RESULT_ONLY'))
    const second = followupBody(protocol, first, reply, 'tools', 'RESULT_ONLY')
    assert.ok(JSON.stringify(second).includes(fixtureSignature))
    assert.ok(JSON.stringify(second).includes('call_1'))
    assert.ok(JSON.stringify(second).includes('RESULT_ONLY'))
    assert.ok(protocol === 'gemini' ? first.toolConfig : first.tool_choice)
    assert.equal(second.tool_choice, undefined)
    assert.equal(second.toolConfig, undefined)
    const text = inspectReply(protocol, replyWire(protocol, 'OK 测试', false, stream), stream)
    assert.deepEqual(text.issues, [])
    assert.equal(text.text, 'OK 测试')
  })
}

test('missing Responses SDK envelope fields are reported even with valid text', () => {
  const body = JSON.parse(replyWire('responses'))
  delete body.created_at
  delete body.output[0].id
  delete body.output[0].content[0].annotations
  const result = inspectReply('responses', JSON.stringify(body), false)
  assert.deepEqual(result.issues.map(i => i.path), ['/created_at', '/output/0/id', '/output/0/content/0/annotations'])
})

test('Responses function_call optional fields may be absent but call_id is required', () => {
  const body = JSON.parse(replyWire('responses', '', true))
  const call = body.output.find(item => item.type === 'function_call')
  delete call.id; delete call.status
  assert.deepEqual(inspectReply('responses', JSON.stringify(body), false).issues, [])
  delete call.call_id
  assert.equal(inspectReply('responses', JSON.stringify(body), false).issues[0].path, '/output/1/call_id')
})

test('SSE supports comments, CRLF, multiline data and rejects undispatched or malformed frames', () => {
  assert.deepEqual(parseSSE('\uFEFF: keepalive\r\nevent: x\r\ndata: {"a":\r\ndata: 1}\r\n\r\n'), [{ type: 'x', value: { a: 1 } }])
  for (const raw of ['data: {}', 'data: {}\n']) assert.throws(() => parseSSE(raw), { code: 'truncated_stream' })
  assert.throws(() => parseSSE('data: {oops}\n\n'), { code: 'invalid_sse_json' })
})

test('HTTP 200 stream errors and missing terminal events are failures', () => {
  const raw = 'event: error\ndata: {"type":"error","error":{"message":"unsupported_native at /unmapped"}}\n\n'
  assert.equal(inspectReply('anthropic', raw, true).issues[0].code, 'stream_error')
  for (const protocol of protocols) {
    const wire = replyWire(protocol, 'OK', false, true)
    const partial = protocol === 'chat' ? wire.replace('data: [DONE]\n\n', '') : wire.slice(0, wire.trimEnd().lastIndexOf('\n\n') + 2)
    assert.ok(inspectReply(protocol, partial, true).issues.some(i => i.code === 'truncated_stream'), protocol)
  }
})

test('Responses events cannot start before response.created', () => {
  const raw = 'event: response.in_progress\ndata: {"type":"response.in_progress","sequence_number":0}\n\n' + replyWire('responses', 'OK', false, true).replaceAll('"sequence_number":', '"sequence_number":1')
  assert.ok(inspectReply('responses', raw, true).issues.some(i => i.message === 'Event before response.created'))
})

test('malformed nested response values are schema findings, not runner crashes', () => {
  for (const [protocol, field] of [['responses', 'output'], ['anthropic', 'content'], ['gemini', 'candidates']]) {
    const obj = JSON.parse(replyWire(protocol)); obj[field] = [null]
    assert.ok(inspectReply(protocol, JSON.stringify(obj), false).issues.length > 0)
  }
})

test('endpoint roots accept version suffixes without doubling paths', () => {
  assert.equal(endpoint('https://example.com/proxy/v1/', 'chat', 'x', false), 'https://example.com/proxy/v1/chat/completions')
  assert.equal(endpoint('https://example.com/v1beta', 'gemini', 'a/b', true), 'https://example.com/v1beta/models/a%2Fb:streamGenerateContent?alt=sse')
})

const geminiWire = (parts, stream, usageMetadata = { promptTokenCount: 5, thoughtsTokenCount: 94, totalTokenCount: 99 }) => {
  const response = { candidates: [{ content: { role: 'model', parts }, finishReason: 'STOP' }], ...(usageMetadata === undefined ? {} : { usageMetadata }) }
  if (!stream) return JSON.stringify(response)
  return [...parts.map(part => ({ candidates: [{ content: { role: 'model', parts: [part] } }] })), { ...response, candidates: [{ finishReason: 'STOP' }] }].map(value => `data: ${JSON.stringify(value)}\n\n`).join('')
}

for (const stream of [false, true]) {
  test(`Gemini ${stream ? 'SSE' : 'JSON'} drops empty history parts and attaches trailing signatures without erasing data`, () => {
    const cases = [
      { scenario: 'multiturn', parts: [{ text: '' }, { text: 'O' }, { text: 'K' }, { text: '', thoughtSignature: fixtureSignature }], expected: [{ text: 'O' }, { text: 'K', thoughtSignature: fixtureSignature }] },
      { scenario: 'tools', parts: [{ functionCall: { id: 'call_1', name: 'audit_echo', args: { value: 7 } }, thoughtSignature: fixtureSignature }, { text: '' }], expected: [{ functionCall: { id: 'call_1', name: 'audit_echo', args: { value: 7 } }, thoughtSignature: fixtureSignature }] },
    ]
    for (const { scenario, parts, expected } of cases) {
      const before = structuredClone(parts)
      const reply = inspectReply('gemini', geminiWire(parts, stream), stream)
      assert.deepEqual(reply.issues, [])
      const next = followupBody('gemini', requestBody('gemini', 'test', stream, scenario, 'CODE', 32768), reply, scenario, 'CODE')
      assert.deepEqual(next.contents[1].parts, expected)
      assert.deepEqual(parts, before)
      assert.equal(reply.text, scenario === 'multiturn' ? 'OK' : '')
    }
  })

  test(`Gemini ${stream ? 'SSE' : 'JSON'} validates present counters without requiring optional breakdowns`, () => {
    const inspect = (usage, required = true) => inspectReply('gemini', geminiWire([{ text: 'OK' }], stream, usage), stream, required)
    assert.deepEqual(inspect({ promptTokenCount: 5, thoughtsTokenCount: 94, totalTokenCount: 99 }).issues, [])
    assert.deepEqual(inspect({ totalTokenCount: 0 }).issues, [])
    assert.deepEqual(inspect(null, false).issues, [])
    for (const usage of [null, {}]) assert.ok(inspect(usage).issues.some(i => i.code === 'usage_missing'))
    for (const [key, value] of [['candidatesTokenCount', -1], ['thoughtsTokenCount', '94'], ['totalTokenCount', null], ['cachedContentTokenCount', 0.5]]) {
      assert.ok(inspect({ totalTokenCount: 99, [key]: value }, false).issues.some(i => i.path === `/usage/${key}`))
    }
  })
}

test('Gemini signatures with no unique preceding part fail instead of being silently discarded', () => {
  for (const parts of [[{ text: '', thoughtSignature: fixtureSignature }], [{ text: 'OK', thoughtSignature: 'first' }, { thoughtSignature: 'second' }]]) {
    assert.ok(inspectReply('gemini', geminiWire(parts, true), true).issues.some(i => i.code === 'unassociated_signature'))
  }
})
