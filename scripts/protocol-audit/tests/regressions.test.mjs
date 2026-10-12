import test from 'node:test'
import assert from 'node:assert/strict'
import { regressionCases, geminiDisabledSafetySettings, assertNativeSystemRequest } from '../src/regressions.mjs'

test('reported Gemini safety controls are diagnosed while blocking requirements reject locally', () => {
  for (const protocol of ['chat', 'responses', 'anthropic']) {
    const cases = regressionCases({ protocol }, 'model', 32768)
    for (const stream of [false, true]) {
      const suffix = stream ? 'sse' : 'json'
      const accepted = cases.find(c => c.name === `original-gemini-safety-${suffix}`)
      assert.deepEqual(accepted.body.safetySettings, geminiDisabledSafetySettings)
      assert.equal(accepted.body.systemInstruction, undefined)
      assert.equal(accepted.diagnostic, 'gemini-safety-settings')
      assert.deepEqual(accepted.absentUpstream, ['safetySettings', 'gemini_safety_settings'])
      const rejected = cases.find(c => c.name === `unsupported-gemini-threshold-${suffix}`)
      assert.equal(rejected.noUpstream, true)
      assert.equal(rejected.rejectPath, '/safetySettings/0/threshold')
      assert.equal(rejected.body.safetySettings[0].threshold, 'BLOCK_LOW_AND_ABOVE')
    }
  }
})

test('mixed tool history checks a synthetic second round without forced tool selection', () => {
  const cases = regressionCases({ protocol: 'chat' }, 'model', 32768).filter(c => c.mixedHistory)
  assert.deepEqual(cases.map(c => c.stream), [false, true])
  for (const c of cases) {
    const items = c.body.input
    assert.equal(items[2].type, 'function_call')
    assert.equal(items[3].content, 'between parallel calls')
    assert.equal(items[4].type, 'function_call')
    assert.deepEqual(items.slice(5).map(i => i.call_id), [items[2].call_id, items[4].call_id])
    assert.equal(c.body.tool_choice, undefined)
    assert.equal(c.diagnostic, 'chat-assistant-history')
    assert.equal(c.expectedText, items[6].output)
  }
})

test('visible thinking fragment regressions preserve the full synthetic history', () => {
  const cases = regressionCases({ protocol: 'chat' }, 'model', 32768).filter(c => c.thinkingFragments)
  assert.deepEqual(cases.map(c => c.stream), [false, true])
  for (const c of cases) {
    assert.equal(c.protocol, 'gemini')
    assert.deepEqual(c.body.contents.map(m => m.role), ['user', 'model', 'user'])
    assert.equal(c.body.contents[1].parts.length, 3)
    assert.equal(c.expectedText, 'FRAGMENTS_OK')
    assert.equal(c.diagnostic, 'chat-assistant-history')
  }
})

test('reminders remain user text while mid-system cases deliberately exercise role conversion', () => {
  for (const protocol of ['chat', 'responses', 'anthropic', 'gemini']) {
    const cases = regressionCases({ protocol }, 'model', 32768)
    for (const stream of [false, true]) {
      const reminder = cases.find(c => c.name === `reminder-text-${stream ? 'sse' : 'json'}`)
      assert.equal(reminder.body.messages[0].role, 'user')
      assert.match(reminder.body.messages[0].content[0].text, /<system-reminder>/)
    }
    for (const c of cases.filter(c => c.name.includes('-middle-'))) {
      assert.ok(['anthropic', 'gemini'].includes(protocol))
      const messages = c.body.messages || c.body.input
      assert.ok(['system', 'developer'].includes(messages[2].role))
      assert.equal(c.diagnostic, 'system-instruction-hoist')
    }
  }
})

test('cached system scope and original include/store faults have distinct expected outcomes', () => {
  const native = regressionCases({ protocol: 'anthropic' }, 'model', 32768)
  assert.equal(native.filter(c => c.preserveSystem).length, 6)
  assert.equal(native.filter(c => c.preserveMessages).length, 4)
  assert.ok(native.filter(c => c.preserveSystem).every(c => !c.rejectPath && !c.noUpstream))
  const gemini = regressionCases({ protocol: 'gemini' }, 'model', 32768)
  assert.equal(gemini.filter(c => c.originalUsage).length, 2)
  for (const stream of [false, true]) {
    const cases = gemini.filter(c => c.stream === stream)
    const includes = cases.filter(c => c.name.startsWith('original-include-'))
    assert.equal(includes.length, 4)
    assert.equal(Object.hasOwn(includes[0].body, 'include'), false)
    assert.equal(includes[1].body.include, null)
    assert.deepEqual(includes[2].body.include, [])
    assert.deepEqual(includes[3].body.include, ['reasoning.encrypted_content'])
    assert.ok(includes.every(c => c.body.store === false && c.stateless))
    assert.equal(cases.filter(c => c.name.startsWith('original-store-')).length, 3)
    assert.equal(cases.filter(c => c.rejectPath && c.noUpstream).length, 3)
    assert.ok(cases.every(c => (c.body.max_tokens ?? c.body.max_output_tokens ?? c.body.max_completion_tokens) === 32768))
  }
})

test('native Anthropic regressions require positions, block cache and message output_config to survive', () => {
  const cases = regressionCases({ protocol: 'anthropic' }, 'model', 32768).filter(c => c.preserveMessages)
  for (const spec of cases) {
    assert.deepEqual(spec.body.messages.map(message => message.role), ['user', 'assistant', 'system', 'user'])
    assertNativeSystemRequest(spec, structuredClone(spec.body))
    const moved = structuredClone(spec.body)
    moved.messages.splice(2, 1)
    assert.throws(() => assertNativeSystemRequest(spec, moved), /Native message order or metadata changed/)
    assert.throws(() => assertNativeSystemRequest(spec, spec.body, [{ ruleId: 'system-instruction-hoist' }]), /were hoisted/)
    if (!spec.name.includes('output-config')) continue
    const system = spec.body.messages[2]
    assert.equal(system.output_config.effort, 'medium')
    assert.deepEqual(system.content[0].cache_control, { type: 'ephemeral' })
    const lostConfig = structuredClone(spec.body)
    delete lostConfig.messages[2].output_config
    assert.throws(() => assertNativeSystemRequest(spec, lostConfig), /Native message order or metadata changed/)
    const lostCache = structuredClone(spec.body)
    delete lostCache.messages[2].content[0].cache_control
    assert.throws(() => assertNativeSystemRequest(spec, lostCache), /Native message order or metadata changed/)
  }
})
