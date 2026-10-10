export const protocols = ['chat', 'responses', 'anthropic', 'gemini']
export const scenarios = ['models', 'text', 'multiturn', 'tools', 'tools_auto', 'system', 'chinese', 'history', 'image']

export class AuditError extends Error {
  constructor(code, message, path = '') { super(message); this.code = code; this.path = path }
}

const schema = { type: 'object', properties: { value: { type: 'integer' } }, required: ['value'], additionalProperties: false }
const tool = { name: 'audit_echo', description: 'Echo a test integer. The test client supplies the result.', parameters: schema }

export function endpoint(baseUrl, protocol, model, stream, models = false) {
  const url = new URL(baseUrl)
  const prefix = url.pathname.replace(/\/$/, '').replace(/\/(v1|v1beta)$/, '')
  const path = models ? (protocol === 'gemini' ? '/v1beta/models' : '/v1/models')
    : protocol === 'gemini' ? `/v1beta/models/${encodeURIComponent(model)}:${stream ? 'streamGenerateContent' : 'generateContent'}`
      : { chat: '/v1/chat/completions', responses: '/v1/responses', anthropic: '/v1/messages' }[protocol]
  url.pathname = prefix + path
  if (protocol === 'gemini' && stream && !models) url.searchParams.set('alt', 'sse')
  return url.toString()
}

export function requestBody(protocol, model, stream, scenario, marker, maxOutputTokens) {
	const automaticTools = scenario === 'tools_auto'
	if (automaticTools) scenario = 'tools'
  const prompt = scenario === 'system' ? 'Reply only with the code from system instructions.' : scenario === 'chinese' ? '请只回复：测试成功' : scenario === 'image' ? 'What solid color is this image? Reply with only the English color name.' : scenario === 'tools' ? 'Call audit_echo with value 7. After receiving the result, reply with its marker exactly.'
    : scenario === 'multiturn' ? `Remember this code: ${marker}. Reply with OK.` : 'Reply exactly OK.'
  let body
  if (protocol === 'chat') body = { model, messages: [{ role: 'user', content: prompt }], max_completion_tokens: maxOutputTokens, stream, ...(stream ? { stream_options: { include_usage: true } } : {}) }
  if (protocol === 'responses') body = { model, input: [{ role: 'user', content: prompt }], max_output_tokens: maxOutputTokens, store: false, include: ['reasoning.encrypted_content'], stream }
  if (protocol === 'anthropic') body = { model, messages: [{ role: 'user', content: prompt }], max_tokens: maxOutputTokens, stream }
  if (protocol === 'gemini') body = { contents: [{ role: 'user', parts: [{ text: prompt }] }], generationConfig: { maxOutputTokens } }
  if (scenario === 'system') {
    const system = `The code is ${marker}. Always reply with only this code.`
    if (protocol === 'chat') body.messages.unshift({ role: 'system', content: system })
    if (protocol === 'responses') body.instructions = system
    if (protocol === 'anthropic') body.system = system
    if (protocol === 'gemini') body.systemInstruction = { role: 'user', parts: [{ text: system }] }
  }
  if (scenario === 'history') {
    const history = [{ role: 'user', content: `Remember this code: ${marker}.` }, { role: 'assistant', content: 'Noted.' }]
    for (let i = 0; i < 6; i++) history.push({ role: 'user', content: `Routine turn ${i}: reply OK.` }, { role: 'assistant', content: 'OK' })
    history.push({ role: 'user', content: 'What code did I ask you to remember? Reply only with that code.' })
    if (protocol === 'gemini') body.contents = history.map(m => ({ role: m.role === 'assistant' ? 'model' : m.role, parts: [{ text: m.content }] }))
    else body[protocol === 'responses' ? 'input' : 'messages'] = history
  }
  if (scenario === 'image') {
    const png = 'iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAIAAAD8GO2jAAAAKElEQVR4nO3NsQ0AAAzCMP5/un0CNkuZ41wybXsHAAAAAAAAAAAAxR4yw/wuPL6QkAAAAABJRU5ErkJggg=='
    const url = `data:image/png;base64,${png}`
    if (protocol === 'chat') body.messages[0].content = [{ type: 'text', text: prompt }, { type: 'image_url', image_url: { url } }]
    if (protocol === 'responses') body.input[0].content = [{ type: 'input_text', text: prompt }, { type: 'input_image', image_url: url, detail: 'auto' }]
    if (protocol === 'anthropic') body.messages[0].content = [{ type: 'text', text: prompt }, { type: 'image', source: { type: 'base64', media_type: 'image/png', data: png } }]
    if (protocol === 'gemini') body.contents[0].parts.push({ inlineData: { mimeType: 'image/png', data: png } })
  }
  if (scenario === 'tools') {
    if (protocol === 'chat') Object.assign(body, { tools: [{ type: 'function', function: tool }], tool_choice: { type: 'function', function: { name: tool.name } } })
    if (protocol === 'responses') Object.assign(body, { tools: [{ type: 'function', ...tool }], tool_choice: { type: 'function', name: tool.name } })
    if (protocol === 'anthropic') Object.assign(body, { tools: [{ name: tool.name, description: tool.description, input_schema: schema }], tool_choice: { type: 'tool', name: tool.name } })
    if (protocol === 'gemini') Object.assign(body, { tools: [{ functionDeclarations: [{ ...tool, parameters: { type: schema.type, properties: schema.properties, required: schema.required } }] }], toolConfig: { functionCallingConfig: { mode: 'ANY', allowedFunctionNames: [tool.name] } } })
  }
  if (automaticTools) {
    if (protocol === 'gemini') body.toolConfig = { functionCallingConfig: { mode: 'AUTO' } }
    else body.tool_choice = protocol === 'anthropic' ? { type: 'auto' } : 'auto'
  }
  return body
}

export function followupBody(protocol, firstBody, reply, scenario, marker) {
  const body = structuredClone(firstBody)
  const key = protocol === 'responses' ? 'input' : protocol === 'gemini' ? 'contents' : 'messages'
  body[key].push(...reply.history)
  delete body.tool_choice
  delete body.toolConfig
  if (scenario === 'multiturn') {
    const prompt = 'What code did I ask you to remember? Reply only with that code.'
    body[key].push(protocol === 'gemini' ? { role: 'user', parts: [{ text: prompt }] } : { role: 'user', content: prompt })
  } else {
    const result = { value: 7, marker }
    if (protocol === 'chat') body.messages.push(...reply.calls.map(call => ({ role: 'tool', tool_call_id: call.id, content: JSON.stringify(result) })))
    if (protocol === 'responses') body.input.push(...reply.calls.map(call => ({ type: 'function_call_output', call_id: call.id, output: JSON.stringify(result) })))
    if (protocol === 'anthropic') body.messages.push({ role: 'user', content: reply.calls.map(call => ({ type: 'tool_result', tool_use_id: call.id, content: JSON.stringify(result) })) })
    if (protocol === 'gemini') body.contents.push({ role: 'user', parts: reply.calls.map(call => ({ functionResponse: { ...(call.id ? { id: call.id } : {}), name: call.name, response: result } })) })
  }
  return body
}

// Parse after bounded transport collection; framing is independent of TCP chunks.
export function parseSSE(raw) {
  const frames = []
  let type = '', data = []
  const flush = () => {
    if (data.length) {
      const text = data.join('\n')
      if (text === '[DONE]') frames.push({ type: 'done', value: null })
      else {
        try { frames.push({ type, value: JSON.parse(text) }) }
        catch { throw new AuditError('invalid_sse_json', 'SSE data is not valid JSON', `/frames/${frames.length}`) }
      }
    }
    type = ''; data = []
  }
  const lines = raw.replace(/^\uFEFF/, '').replace(/\r\n|\r/g, '\n').split('\n')
  if (lines.at(-1) === '') lines.pop()
  for (const line of lines) {
    if (!line) { flush(); continue }
    if (line.startsWith(':')) continue
    const colon = line.indexOf(':')
    const field = colon < 0 ? line : line.slice(0, colon)
    let value = colon < 0 ? '' : line.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') type = value
    if (field === 'data') data.push(value)
  }
  if (data.length) throw new AuditError('truncated_stream', 'SSE ended with an undispatched frame (missing blank line)')
  if (!frames.length) throw new AuditError('empty_stream', 'No SSE data frames received')
  return frames
}

function checks(result) {
  const require = (ok, path, message, code = 'schema_error') => {
    if (!ok && result.issues.length < 100) result.issues.push({ code, path, message })
    return !!ok
  }
  return {
    require,
    string: (value, path) => require(typeof value === 'string' && value.length > 0, path, 'Expected a nonempty string'),
    count: (value, path) => require(Number.isSafeInteger(value) && value >= 0, path, 'Expected a nonnegative safe integer'),
    array: (value, path) => require(Array.isArray(value), path, 'Expected an array'),
    object: (value, path) => require(value && typeof value === 'object' && !Array.isArray(value), path, 'Expected an object'),
  }
}

function inspectObject(protocol, obj, result, { requireUsage, initial = false } = {}) {
  const c = checks(result)
  if (!c.require(obj && typeof obj === 'object' && !Array.isArray(obj), '/', 'Expected a response object')) return
  if (obj.error) { c.require(false, '/error', typeof obj.error.message === 'string' ? obj.error.message : JSON.stringify(obj.error), 'api_error'); return }
  if (protocol === 'chat' || protocol === 'responses' || protocol === 'anthropic') {
    c.string(obj.id, '/id'); c.string(obj.model, '/model')
  }
  if (protocol === 'chat') {
    c.require(obj.object === 'chat.completion', '/object', 'Expected chat.completion')
    c.count(obj.created, '/created')
    if (!c.array(obj.choices, '/choices') || !c.require(obj.choices.length > 0, '/choices', 'No completion choices')) return
    const choice = obj.choices.find(x => x?.index === 0)
    if (!c.require(!!choice, '/choices', 'Missing choice index 0')) return
    c.string(choice.finish_reason, '/choices/0/finish_reason')
    if (choice.finish_reason === 'length') c.require(false, '/choices/0/finish_reason', 'Output reached its token limit', 'output_truncated')
    const message = choice.message
    if (!c.require(message?.role === 'assistant', '/choices/0/message/role', 'Expected assistant')) return
    result.history = [structuredClone(message)]
    result.text = typeof message.content === 'string' ? message.content : ''
    if (message.tool_calls !== undefined && message.tool_calls !== null && c.array(message.tool_calls, '/choices/0/message/tool_calls')) {
      for (const [i, call] of message.tool_calls.entries()) {
        const path = `/choices/0/message/tool_calls/${i}`
        if (!c.object(call, path)) continue
        c.require(call.type === 'function', path, 'Expected a function tool call')
        addCall(result, call.id, call.function?.name, call.function?.arguments, path, { encoded: true, argsField: 'function/arguments', nameField: 'function/name' })
      }
    }
  }
  if (protocol === 'responses') {
    c.require(obj.object === 'response', '/object', 'Expected response')
    c.count(obj.created_at, '/created_at')
    c.require(obj.status === (initial ? 'in_progress' : 'completed'), '/status', initial ? 'Expected in_progress' : `Expected completed; received ${obj.status}`, obj.status === 'incomplete' ? 'output_truncated' : 'schema_error')
    if (!c.array(obj.output, '/output')) return
    if (!initial) result.history = structuredClone(obj.output)
    for (const [i, item] of obj.output.entries()) {
      const path = `/output/${i}`
      if (!c.object(item, path)) continue
      if (item.type === 'message') {
        c.string(item.id, `${path}/id`)
        c.require(item.status === (initial ? 'in_progress' : 'completed'), `${path}/status`, 'Invalid message status')
        c.require(item.role === 'assistant', `${path}/role`, 'Expected assistant')
        if (c.array(item.content, `${path}/content`)) for (const [j, content] of item.content.entries()) {
          if (!c.object(content, `${path}/content/${j}`)) continue
          if (content.type === 'output_text') {
            c.require(typeof content.text === 'string', `${path}/content/${j}/text`, 'Expected text string')
            c.array(content.annotations, `${path}/content/${j}/annotations`)
            if (!initial) result.text += content.text || ''
          }
        }
      }
      if (!initial && item.type === 'function_call') {
        // Function-call id/status are optional in the Responses output type; call_id is required.
        if (item.id !== undefined) c.string(item.id, `${path}/id`)
        if (item.status !== undefined) c.require(item.status === 'completed', `${path}/status`, 'Expected completed tool item')
        addCall(result, item.call_id, item.name, item.arguments, path, { encoded: true, idField: 'call_id' })
      }
    }
  }
  if (protocol === 'anthropic') {
    c.require(obj.type === 'message', '/type', 'Expected message')
    c.require(obj.role === 'assistant', '/role', 'Expected assistant')
    if (!initial) c.string(obj.stop_reason, '/stop_reason')
    if (obj.stop_reason === 'max_tokens') c.require(false, '/stop_reason', 'Output reached its token limit', 'output_truncated')
    if (!c.array(obj.content, '/content')) return
    result.history = [{ role: 'assistant', content: structuredClone(obj.content) }]
    for (const [i, part] of obj.content.entries()) {
      if (!c.object(part, `/content/${i}`)) continue
      if (part.type === 'text') { c.require(typeof part.text === 'string', `/content/${i}/text`, 'Expected text string'); result.text += part.text || '' }
      if (part.type === 'tool_use') addCall(result, part.id, part.name, part.input, `/content/${i}`, { argsField: 'input' })
    }
  }
  if (protocol === 'gemini') {
    if (!c.array(obj.candidates, '/candidates') || !c.require(obj.candidates.length > 0, '/candidates', 'No candidates; inspect promptFeedback', 'empty_response')) return
    const candidate = obj.candidates.find(x => x?.index === 0) || obj.candidates[0]
    if (!c.object(candidate, '/candidates/0')) return
    c.string(candidate.finishReason, '/candidates/0/finishReason')
    if (candidate.finishReason !== 'STOP') c.require(false, '/candidates/0/finishReason', `Generation ended with ${candidate.finishReason}`, 'generation_stopped')
    if (!c.array(candidate.content?.parts, '/candidates/0/content/parts')) return
    const parts = []
    for (const [i, part] of candidate.content.parts.entries()) {
      const path = `/candidates/0/content/parts/${i}`
      if (!c.object(part, path)) continue
      if (Object.keys(part).every(key => key === 'thoughtSignature' || key === 'thought' || (key === 'text' && part.text === ''))) {
        if (part.thoughtSignature !== undefined) {
          const previous = parts.at(-1)
          // A trailing signature can arrive with empty text; never overwrite the preceding data.
          if (c.string(part.thoughtSignature, `${path}/thoughtSignature`) && c.require(previous && !previous.thoughtSignature && (part.thought === undefined || !!part.thought === !!previous.thought), path, 'Cannot associate signature with a unique part', 'unassociated_signature')) previous.thoughtSignature = part.thoughtSignature
        } else c.require(part.text === '', path, 'Part has no data')
        continue
      }
      parts.push(structuredClone(part))
      if (typeof part.text === 'string' && !part.thought) result.text += part.text
      if (part.functionCall) addCall(result, part.functionCall.id, part.functionCall.name, part.functionCall.args, `/candidates/0/content/parts/${i}/functionCall`, { requireId: false, argsField: 'args' })
    }
    result.history = [{ ...structuredClone(candidate.content), role: 'model', parts }]
  }
  const usage = protocol === 'gemini' ? obj.usageMetadata : obj.usage
  result.usage = usage ?? null
  if (usage != null || requireUsage || protocol === 'anthropic') {
    if (!c.require(usage && typeof usage === 'object' && !Array.isArray(usage), '/usage', 'Missing usage counters', 'usage_missing')) return
    const names = { chat: ['prompt_tokens', 'completion_tokens', 'total_tokens'], responses: ['input_tokens', 'output_tokens', 'total_tokens'], anthropic: ['input_tokens', 'output_tokens'], gemini: ['promptTokenCount', 'candidatesTokenCount', 'totalTokenCount', 'thoughtsTokenCount', 'cachedContentTokenCount', 'toolUsePromptTokenCount'] }[protocol]
    if (protocol === 'gemini' && requireUsage) c.require(names.some(name => usage[name] !== undefined), '/usage', 'Missing usage counters', 'usage_missing')
    for (const name of names) if (protocol !== 'gemini' || usage[name] !== undefined) c.count(usage[name], `/usage/${name}`)
  }
}

function addCall(result, id, name, args, path, { encoded = false, requireId = true, idField = 'id', argsField = 'arguments', nameField = 'name' } = {}) {
  const c = checks(result)
  if (requireId) c.string(id, `${path}/${idField}`)
  c.string(name, `${path}/${nameField}`)
  if (encoded) {
    if (!c.require(typeof args === 'string', `${path}/${argsField}`, 'Expected JSON argument string')) return
    try { args = JSON.parse(args) } catch { c.require(false, `${path}/${argsField}`, 'Malformed tool argument JSON'); return }
  }
  c.require(args && typeof args === 'object' && !Array.isArray(args), `${path}/${argsField}`, 'Expected tool argument object')
  result.calls.push({ id, name, args })
}

export function inspectReply(protocol, raw, stream, requireUsage = true) {
  const result = { issues: [], warnings: [], text: '', history: [], calls: [], usage: null }
  if (!stream) {
    let object
    try { object = JSON.parse(raw) } catch { throw new AuditError('invalid_json', 'Response is not valid JSON') }
    inspectObject(protocol, object, result, { requireUsage })
    return result
  }
  const frames = parseSSE(raw)
  result.eventCount = frames.length
  const c = checks(result)
  for (const [i, frame] of frames.entries()) {
    const value = frame.value
    if (frame.type === 'error' || value?.type === 'error' || value?.error) c.require(false, `/frames/${i}`, value?.error?.message || value?.message || 'Upstream stream error', 'stream_error')
    if (value && typeof value.type === 'string' && frame.type && frame.type !== 'data' && frame.type !== value.type) c.require(false, `/frames/${i}/type`, 'SSE event name and JSON type disagree')
  }
  if (result.issues.length) return result
  if (protocol === 'chat') {
    const message = { role: '', content: '' }, tools = new Map()
    let id, model, created, finish, usage, done = false
    for (const [i, { type, value }] of frames.entries()) {
      const path = `/frames/${i}`
      if (done) { c.require(false, path, 'Data after [DONE]', 'stream_lifecycle'); continue }
      if (type === 'done') { done = true; continue }
      if (!c.object(value, path)) continue
      c.require(value.object === 'chat.completion.chunk', `${path}/object`, 'Expected chat.completion.chunk')
      c.string(value.id, `${path}/id`); c.string(value.model, `${path}/model`); c.count(value.created, `${path}/created`)
      if (id !== undefined) c.require(value.id === id && value.model === model && value.created === created, path, 'Response identity or timestamp changed', 'stream_lifecycle')
      id ??= value.id; model ??= value.model; created ??= value.created
      if (value.usage != null) usage = value.usage
      if (!c.array(value.choices, `${path}/choices`)) continue
      for (const choice of value.choices.filter(x => x?.index === 0)) {
        const delta = choice.delta || {}
        if (!c.object(delta, `${path}/delta`)) continue
        if (finish && (delta.content || delta.tool_calls?.length)) c.require(false, path, 'Content after finish_reason', 'stream_lifecycle')
        if (delta.role) message.role = delta.role
        if (typeof delta.content === 'string') message.content += delta.content
        for (const [key, val] of Object.entries(delta)) if (!['role', 'content', 'tool_calls'].includes(key)) {
          if (['reasoning_content', 'reasoning', 'refusal'].includes(key) && typeof val === 'string') message[key] = (message[key] || '') + val
          else message[key] = structuredClone(val)
        }
        const toolParts = delta.tool_calls ?? []
        if (!c.array(toolParts, `${path}/delta/tool_calls`)) continue
        for (const part of toolParts) {
          if (!c.object(part, `${path}/delta/tool_calls`)) continue
          if (!c.count(part.index, `${path}/delta/tool_calls/index`)) continue
          const call = tools.get(part.index) || { id: '', type: 'function', function: { name: '', arguments: '' } }
          if (part.id) { if (call.id) c.require(call.id === part.id, path, 'Tool ID changed', 'stream_lifecycle'); call.id = part.id }
          if (part.type) call.type = part.type
          if (part.function?.name) call.function.name += part.function.name
          if (typeof part.function?.arguments === 'string') call.function.arguments += part.function.arguments
          for (const [key, val] of Object.entries(part)) if (!['index', 'id', 'type', 'function'].includes(key)) call[key] = structuredClone(val)
          tools.set(part.index, call)
        }
        if (choice.finish_reason != null) finish = choice.finish_reason
      }
    }
    c.require(done && !!finish, '/', 'Stream ended without finish_reason and [DONE]', 'truncated_stream')
    if (tools.size) message.tool_calls = [...tools.values()]
    inspectObject(protocol, { id, model, created, object: 'chat.completion', choices: [{ index: 0, message, finish_reason: finish }], usage }, result, { requireUsage })
  }
  if (protocol === 'responses') {
    let start, final, sequence = -1, text = ''
    const items = new Set()
    for (const [i, frame] of frames.entries()) {
      const value = frame.value, type = value?.type || frame.type, path = `/frames/${i}`
      if (frame.type === 'done') continue
      if (!c.object(value, path)) continue
      if (final) c.require(false, path, 'Event after response terminal', 'stream_lifecycle')
      if (!start && type !== 'response.created') c.require(false, path, 'Event before response.created', 'stream_lifecycle')
      c.count(value.sequence_number, `${path}/sequence_number`)
      c.require(value.sequence_number > sequence, `${path}/sequence_number`, 'Event sequence did not increase', 'stream_lifecycle')
      sequence = value.sequence_number
      if (type === 'response.created') {
        c.require(!start, path, 'Duplicate response.created', 'stream_lifecycle')
        start = value.response
        const check = { issues: [], text: '', calls: [], history: [] }
        inspectObject(protocol, start, check, { initial: true, requireUsage: false })
        result.issues.push(...check.issues.map(issue => ({ ...issue, path: `${path}/response${issue.path}` })))
      }
      if (type === 'response.output_item.added') { c.string(value.item?.id, `${path}/item/id`); items.add(value.item?.id) }
      if (type === 'response.output_text.delta') {
        c.require(items.has(value.item_id), `${path}/item_id`, 'Delta references an unopened item', 'stream_lifecycle')
        c.require(typeof value.delta === 'string', `${path}/delta`, 'Expected text delta')
        text += value.delta || ''
      }
      if (['response.completed', 'response.incomplete', 'response.failed', 'response.cancelled'].includes(type)) final = value.response
    }
    c.require(!!start && !!final, '/', 'Missing response.created or terminal response', 'truncated_stream')
    if (final) {
      if (start) c.require(start.id === final.id && start.created_at === final.created_at, '/', 'Response identity or timestamp changed', 'stream_lifecycle')
      inspectObject(protocol, final, result, { requireUsage })
      if (text) c.require(text === result.text, '/output', 'Text deltas disagree with final output', 'stream_content_mismatch')
    }
  }
  if (protocol === 'anthropic') {
    let message, stop = false, deltaSeen = false
    const blocks = new Map(), open = new Set(), args = new Map()
    for (const [i, frame] of frames.entries()) {
      const v = frame.value, type = v?.type || frame.type, path = `/frames/${i}`
      if (!c.object(v, path)) continue
      if (type === 'ping') continue
      if (stop) { c.require(false, path, 'Data after message_stop', 'stream_lifecycle'); continue }
      if (type === 'message_start') {
        c.require(!message, path, 'Duplicate message_start', 'stream_lifecycle')
        message = structuredClone(v.message)
        const check = { issues: [], text: '', calls: [], history: [] }
        inspectObject(protocol, message, check, { initial: true, requireUsage: true })
        result.issues.push(...check.issues.map(issue => ({ ...issue, path: `${path}/message${issue.path}` })))
      } else if (!message) { c.require(false, path, 'Event before message_start', 'stream_lifecycle'); continue }
      else if (type === 'content_block_start') {
        if (!c.object(v.content_block, `${path}/content_block`)) continue
        c.count(v.index, `${path}/index`)
        c.require(!blocks.has(v.index), path, 'Duplicate content block', 'stream_lifecycle')
        blocks.set(v.index, structuredClone(v.content_block)); open.add(v.index)
      } else if (type === 'content_block_delta') {
        const block = blocks.get(v.index), delta = v.delta
        if (!c.require(open.has(v.index) && block && delta, path, 'Delta references a closed or missing block', 'stream_lifecycle')) continue
        const field = { input_json_delta: 'partial_json', text_delta: 'text', thinking_delta: 'thinking', signature_delta: 'signature' }[delta.type]
        if (field && !c.require(typeof delta[field] === 'string', `${path}/delta/${field}`, 'Expected a string delta')) continue
        if (delta.type === 'input_json_delta') args.set(v.index, (args.get(v.index) || '') + delta.partial_json)
        else if (field) block[field] = (block[field] || '') + delta[field]
        else if (delta.type === 'citations_delta') (block.citations ||= []).push(delta.citation)
        else c.require(false, path, `Unsupported delta type: ${delta.type}`, 'unsupported_event')
      } else if (type === 'content_block_stop') {
        c.require(open.delete(v.index), path, 'Stop references a closed or missing block', 'stream_lifecycle')
      } else if (type === 'message_delta') {
        deltaSeen = true
        if (c.object(v.usage, `${path}/usage`)) c.count(v.usage.output_tokens, `${path}/usage/output_tokens`)
        if (c.object(v.delta, `${path}/delta`)) {
          c.string(v.delta.stop_reason, `${path}/delta/stop_reason`)
          c.require(v.delta.stop_sequence === null || typeof v.delta.stop_sequence === 'string', `${path}/delta/stop_sequence`, 'Expected string or null')
        }
        Object.assign(message, v.delta); message.usage = { ...message.usage, ...v.usage }
      } else if (type === 'message_stop') stop = true
      else c.require(false, path, `Unsupported event type: ${type}`, 'unsupported_event')
    }
    c.require(stop && deltaSeen && open.size === 0, '/', 'Stream ended without a complete message/block lifecycle', 'truncated_stream')
    if (message) {
      for (const [index, rawArgs] of args) {
        try { blocks.get(index).input = JSON.parse(rawArgs) }
        catch { c.require(false, `/content/${index}/input`, 'Malformed tool argument JSON') }
      }
      message.content = [...blocks.entries()].sort((a, b) => a[0] - b[0]).map(([, block]) => block)
      inspectObject(protocol, message, result, { requireUsage })
    }
  }
  if (protocol === 'gemini') {
    const parts = []
    let finish, usage
    for (const [i, { value }] of frames.entries()) {
      if (!c.object(value, `/frames/${i}`)) continue
      if (value.usageMetadata) usage = value.usageMetadata
      if (value.candidates !== undefined && !c.array(value.candidates, `/frames/${i}/candidates`)) continue
      const candidate = value.candidates?.find(x => x?.index === 0) || value.candidates?.[0]
      if (!candidate) continue
      if (finish && candidate.content?.parts?.length) c.require(false, `/frames/${i}`, 'Content after finishReason', 'stream_lifecycle')
      const candidateParts = candidate.content?.parts ?? []
      if (!c.array(candidateParts, `/frames/${i}/parts`)) continue
      for (const part of candidateParts) parts.push(part)
      if (candidate.finishReason) finish = candidate.finishReason
    }
    c.require(!!finish, '/', 'Missing Gemini finishReason', 'truncated_stream')
    inspectObject(protocol, { candidates: [{ content: { role: 'model', parts }, finishReason: finish }], usageMetadata: usage }, result, { requireUsage })
  }
  return result
}

export function inspectModels(protocol, raw, model) {
  let obj
  try { obj = JSON.parse(raw) } catch { throw new AuditError('invalid_json', 'Model listing is not valid JSON') }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) throw new AuditError('schema_error', 'Expected model list object')
  if (obj.error) throw new AuditError('api_error', obj.error.message || 'Model listing returned an error')
  const list = protocol === 'gemini' ? obj.models : obj.data
  if (!Array.isArray(list)) throw new AuditError('schema_error', 'Expected model list array', protocol === 'gemini' ? '/models' : '/data')
  const found = list.some(item => (item?.id || (typeof item?.name === 'string' ? item.name.replace(/^models\//, '') : '')) === model)
  return { issues: [], warnings: found ? [] : ['Requested model is not on this page; generation cases test its availability separately.'], modelCount: list.length, modelFound: found }
}
