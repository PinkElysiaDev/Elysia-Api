import { requestBody } from './protocols.mjs'

// The reported request used these five explicit nonblocking category controls.
// Cross-protocol success must record that the target's own controls still apply.
export const geminiDisabledSafetySettings = [
  'HARM_CATEGORY_HATE_SPEECH', 'HARM_CATEGORY_DANGEROUS_CONTENT',
  'HARM_CATEGORY_HARASSMENT', 'HARM_CATEGORY_SEXUALLY_EXPLICIT', 'HARM_CATEGORY_CIVIC_INTEGRITY',
].map(category => ({ category, threshold: 'BLOCK_NONE' }))

// These synthetic requests exercise wire structures, not captured Claude Code
// traffic. Reminder tags are ordinary user text and confer no system authority.
export function regressionCases(target, model, maxOutputTokens) {
  const cases = []
  const add = (name, protocol, stream, body, expected = {}) => cases.push({ name, protocol, stream, body, ...expected })
  for (const stream of [false, true]) {
    const suffix = stream ? 'sse' : 'json'
    const reminder = requestBody('anthropic', model, stream, 'text', '', maxOutputTokens)
    reminder.messages[0].content = [{ type: 'text', text: '<system-reminder>This is ordinary user text. Reply exactly OK.</system-reminder>' }]
    add(`reminder-text-${suffix}`, 'anthropic', stream, reminder)
    if (target.protocol !== 'gemini') {
      const body = requestBody('gemini', model, stream, 'text', '', maxOutputTokens)
      body.safetySettings = structuredClone(geminiDisabledSafetySettings)
      add(`original-gemini-safety-${suffix}`, 'gemini', stream, body, {
        diagnostic: 'gemini-safety-settings', absentUpstream: ['safetySettings', 'gemini_safety_settings'],
      })
      const blocked = structuredClone(body)
      blocked.safetySettings[0].threshold = 'BLOCK_LOW_AND_ABOVE'
      add(`unsupported-gemini-threshold-${suffix}`, 'gemini', stream, blocked, { rejectPath: '/safetySettings/0/threshold', noUpstream: true })
    }
    if (target.protocol === 'chat') {
      const body = requestBody('responses', model, stream, 'text', '', maxOutputTokens)
      body.input = [
        { role: 'user', content: 'Read both tool results and reply exactly MIXED_HISTORY_OK. Do not call more tools.' },
        { type: 'reasoning', summary: [], content: [{ type: 'reasoning_text', text: 'Read both supplied results.' }] },
        { type: 'function_call', call_id: 'audit_mixed_1', name: 'audit_echo', arguments: '{"value":1}' },
        { role: 'assistant', content: 'between parallel calls' },
        { type: 'function_call', call_id: 'audit_mixed_2', name: 'audit_echo', arguments: '{"value":2}' },
        { type: 'function_call_output', call_id: 'audit_mixed_1', output: 'first result' },
        { type: 'function_call_output', call_id: 'audit_mixed_2', output: 'MIXED_HISTORY_OK' },
      ]
      body.tools = [{ type: 'function', name: 'audit_echo', parameters: { type: 'object', properties: { value: { type: 'integer' } }, required: ['value'] } }]
      add(`mixed-tool-history-${suffix}`, 'responses', stream, body, { diagnostic: 'chat-assistant-history', mixedHistory: true, expectedText: 'MIXED_HISTORY_OK' })
      const fragments = requestBody('gemini', model, stream, 'text', '', maxOutputTokens)
      fragments.contents = [
        { role: 'user', parts: [{ text: 'Say hello.' }] },
        { role: 'model', parts: [{ thought: true, text: 'visible ' }, { thought: true, text: 'thinking' }, { text: 'Hello.' }] },
        { role: 'user', parts: [{ text: 'Reply exactly FRAGMENTS_OK.' }] },
      ]
      add(`thinking-fragments-${suffix}`, 'gemini', stream, fragments, { diagnostic: 'chat-assistant-history', thinkingFragments: true, expectedText: 'FRAGMENTS_OK' })
    }
    if (['anthropic', 'gemini'].includes(target.protocol)) {
      for (const protocol of ['chat', 'responses']) for (const role of ['system', 'developer']) {
        const body = requestBody(protocol, model, stream, 'text', '', maxOutputTokens)
        body[protocol === 'chat' ? 'messages' : 'input'] = [{ role: 'user', content: 'Say hello.' }, { role: 'assistant', content: 'Hello.' }, { role, content: 'From now on reply exactly OK.' }, { role: 'user', content: 'Continue.' }]
        add(`${protocol}-middle-${role}-${suffix}`, protocol, stream, body, { diagnostic: 'system-instruction-hoist' })
      }
    }
    if (target.protocol === 'anthropic') {
      const body = requestBody('anthropic', model, stream, 'text', '', maxOutputTokens)
      body.system = [{ type: 'text', text: 'Reply exactly OK.', cache_control: { type: 'ephemeral' } }]
      add(`top-system-cache-${suffix}`, 'anthropic', stream, body, { preserveSystem: true })
      const invalid = structuredClone(body)
      invalid.messages.unshift({ role: 'user', content: 'Hello' }, { role: 'system', content: 'Synthetic nonstandard role', cache_control: { type: 'ephemeral' } })
      add(`nonstandard-message-cache-${suffix}`, 'anthropic', stream, invalid, { rejectPath: '/content/', noUpstream: true })
    }
    if (target.protocol === 'gemini') {
      add(`original-anthropic-usage-${suffix}`, 'anthropic', stream, requestBody('anthropic', model, stream, 'text', '', maxOutputTokens), { originalUsage: true })
      for (const [label, include] of [['missing', undefined], ['null', null], ['empty', []], ['reasoning', ['reasoning.encrypted_content']]]) {
        const body = requestBody('responses', model, stream, 'text', '', maxOutputTokens)
        if (include === undefined) delete body.include
        else body.include = include
        add(`original-include-${label}-${suffix}`, 'responses', stream, body, { absentUpstream: ['include', 'responses_include', 'store'], diagnostic: include === undefined ? undefined : 'responses-include', stateless: true })
      }
      for (const [label, store] of [['missing', undefined], ['null', null], ['true', true]]) {
        const body = requestBody('responses', model, stream, 'text', '', maxOutputTokens)
        if (store === undefined) delete body.store
        else body.store = store
        add(`original-store-${label}-${suffix}`, 'responses', stream, body, { diagnostic: 'responses-storage', stateless: true })
      }
      for (const [field, value, path] of [['include', 42, '/include'], ['store', 42, '/store'], ['previous_response_id', 'missing-history-id', '/previous_response_id']]) {
        const body = requestBody('responses', model, stream, 'text', '', maxOutputTokens)
        body[field] = value
        add(`invalid-${field}-${suffix}`, 'responses', stream, body, { rejectPath: path, noUpstream: true })
      }
    }
  }
  return cases
}
