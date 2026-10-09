// Synthetic wire fixtures: no provider credentials or captured user data.
import './network-guard.mjs'
import { createServer } from 'node:http'
export const fixtureSignature = 'synthetic-signature-keep-in-memory'
const frame = value => `data: ${JSON.stringify(value)}\n\n`
const event = value => `event: ${value.type}\n${frame(value)}`

export const exampleConfig = () => ({
  concurrency: 32, maxOutputTokens: 32768, timeoutMs: 180000,
  targets: ['chat', 'responses', 'anthropic', 'gemini'].map(protocol => ({
    protocol, baseUrl: `https://${protocol}.provider.example`, apiKey: `YOUR_${protocol.toUpperCase()}_KEY`, model: `YOUR_${protocol.toUpperCase()}_MODEL`,
  })),
})

export function replyWire(protocol, text = 'OK', tool = false, stream = false) {
  if (protocol === 'chat') {
    const message = tool
      ? { role: 'assistant', content: null, tool_calls: [{ id: 'call_1', type: 'function', function: { name: 'audit_echo', arguments: '{"value":7}' }, extra_content: { google: { thought_signature: fixtureSignature } } }] }
      : { role: 'assistant', content: text }
    const base = { id: 'chat_1', model: 'test', created: 1700000000 }, usage = { prompt_tokens: 10, completion_tokens: 2, total_tokens: 12 }
    const finish_reason = tool ? 'tool_calls' : 'stop'
    if (!stream) return JSON.stringify({ ...base, object: 'chat.completion', choices: [{ index: 0, message, finish_reason }], usage })
    const chunk = (delta, finish = null, counters) => frame({ ...base, object: 'chat.completion.chunk', choices: [{ index: 0, delta, finish_reason: finish }], ...(counters ? { usage: counters } : {}) })
    return chunk({ role: 'assistant', content: '' }) + (tool
      ? chunk({ tool_calls: [{ ...message.tool_calls[0], index: 0, function: { name: 'audit_echo', arguments: '{"value":' } }] }) + chunk({ tool_calls: [{ index: 0, function: { arguments: '7}' } }] })
      : chunk({ content: text })) + chunk({}, finish_reason) + frame({ ...base, object: 'chat.completion.chunk', choices: [], usage }) + 'data: [DONE]\n\n'
  }
  if (protocol === 'responses') {
    const output = tool
      ? [{ id: 'rs_1', type: 'reasoning', summary: [], encrypted_content: fixtureSignature }, { id: 'fc_1', type: 'function_call', status: 'completed', call_id: 'call_1', name: 'audit_echo', arguments: '{"value":7}' }]
      : [{ id: 'msg_1', type: 'message', status: 'completed', role: 'assistant', content: [{ type: 'output_text', text, annotations: [] }] }]
    const base = { id: 'resp_1', object: 'response', model: 'test', created_at: 1700000000 }
    const final = { ...base, status: 'completed', output, usage: { input_tokens: 10, output_tokens: 2, total_tokens: 12 } }
    if (!stream) return JSON.stringify(final)
    const events = [{ type: 'response.created', response: { ...base, status: 'in_progress', output: [], usage: null } }]
    for (const [output_index, item] of output.entries()) {
      const initial = { ...item, status: 'in_progress', ...(item.type === 'message' ? { content: [] } : item.type === 'function_call' ? { arguments: '' } : {}) }
      events.push({ type: 'response.output_item.added', output_index, item: initial })
      if (item.type === 'message') events.push(
        { type: 'response.content_part.added', item_id: item.id, output_index, content_index: 0, part: { type: 'output_text', text: '', annotations: [] } },
        { type: 'response.output_text.delta', item_id: item.id, output_index, content_index: 0, delta: text },
        { type: 'response.output_text.done', item_id: item.id, output_index, content_index: 0, text },
        { type: 'response.content_part.done', item_id: item.id, output_index, content_index: 0, part: item.content[0] },
      )
      if (item.type === 'function_call') events.push(
        { type: 'response.function_call_arguments.delta', item_id: item.id, output_index, delta: item.arguments },
        { type: 'response.function_call_arguments.done', item_id: item.id, output_index, name: item.name, arguments: item.arguments },
      )
      events.push({ type: 'response.output_item.done', output_index, item })
    }
    events.push({ type: 'response.completed', response: final })
    return events.map((value, sequence_number) => event({ ...value, sequence_number })).join('')
  }
  if (protocol === 'anthropic') {
    const content = tool
      ? [{ type: 'thinking', thinking: 'Synthetic reasoning.', signature: fixtureSignature }, { type: 'tool_use', id: 'call_1', name: 'audit_echo', input: { value: 7 } }]
      : [{ type: 'text', text }]
    const base = { id: 'msg_1', type: 'message', model: 'test', role: 'assistant', stop_sequence: null }
    const stop_reason = tool ? 'tool_use' : 'end_turn', usage = { input_tokens: 10, output_tokens: 2 }
    if (!stream) return JSON.stringify({ ...base, content, stop_reason, usage })
    const events = [{ type: 'message_start', message: { ...base, content: [], stop_reason: null, usage: { ...usage, output_tokens: 0 } } }]
    for (const [index, block] of content.entries()) {
      events.push({ type: 'content_block_start', index, content_block: block.type === 'text' ? { type: 'text', text: '' } : block.type === 'thinking' ? { type: 'thinking', thinking: '', signature: '' } : { ...block, input: {} } })
      if (block.type === 'text') events.push({ type: 'content_block_delta', index, delta: { type: 'text_delta', text } })
      if (block.type === 'thinking') events.push({ type: 'content_block_delta', index, delta: { type: 'thinking_delta', thinking: block.thinking } }, { type: 'content_block_delta', index, delta: { type: 'signature_delta', signature: block.signature } })
      if (block.type === 'tool_use') events.push({ type: 'content_block_delta', index, delta: { type: 'input_json_delta', partial_json: '{"value":' } }, { type: 'content_block_delta', index, delta: { type: 'input_json_delta', partial_json: '7}' } })
      events.push({ type: 'content_block_stop', index })
    }
    events.push({ type: 'message_delta', delta: { stop_reason, stop_sequence: null }, usage: { output_tokens: 2 } }, { type: 'message_stop' })
    return events.map(event).join('')
  }
  const parts = tool ? [{ functionCall: { id: 'call_1', name: 'audit_echo', args: { value: 7 } }, thoughtSignature: fixtureSignature }] : [{ text }]
  const usageMetadata = { promptTokenCount: 10, candidatesTokenCount: 2, totalTokenCount: 12 }
  if (!stream) return JSON.stringify({ candidates: [{ index: 0, content: { role: 'model', parts }, finishReason: 'STOP' }], usageMetadata })
  return frame({ candidates: [{ index: 0, content: { role: 'model', parts } }] }) + frame({ candidates: [{ index: 0, finishReason: 'STOP' }], usageMetadata })
}

// Used only by script self-tests; the audit runner never imports this module.
export async function mockUpstream() {
  const observations = []
  const server = createServer(async (req, res) => {
    try {
      const protocol = req.url.split('/')[1]
      observations.push({ protocol, path: req.url, method: req.method })
      if (req.method === 'GET') { res.end(JSON.stringify(protocol === 'gemini' ? { models: [{ name: `models/mock-${protocol}`, supportedGenerationMethods: ['generateContent'] }] } : { data: [{ id: `mock-${protocol}`, object: 'model', created: 1 }], has_more: false })); return }
      const chunks = []; for await (const chunk of req) chunks.push(chunk)
      const body = JSON.parse(Buffer.concat(chunks).toString()), text = JSON.stringify(body)
      const history = body.messages || body.input || body.contents || []
      const marker = text.match(/AUDIT_[a-z0-9]{12}/)?.[0], last = JSON.stringify(history.at(-1) || {})
      const answer = /What code did I|code from system|tool_result|functionResponse|function_call_output|"role":"tool"/.test(last) ? marker || 'MISSING_MARKER' : text.includes('请只回复：测试成功') ? '测试成功' : text.includes('solid color') ? 'RED' : 'OK'
      const tool = !!body.tools && !/tool_result|functionResponse|function_call_output|"role":"tool"/.test(text)
      const stream = body.stream === true || req.url.includes(':streamGenerateContent')
      res.setHeader('content-type', stream ? 'text/event-stream' : 'application/json')
      res.end(replyWire(protocol, answer, tool, stream))
    } catch (error) { res.writeHead(500); res.end(error.message) }
  })
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  return { baseUrl: `http://127.0.0.1:${server.address().port}`, observations, close: async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) } }
}
