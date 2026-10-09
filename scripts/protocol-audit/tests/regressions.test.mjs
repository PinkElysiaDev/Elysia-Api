import test from 'node:test'
import assert from 'node:assert/strict'
import { regressionCases } from '../src/regressions.mjs'

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
  assert.equal(native.filter(c => c.preserveSystem).length, 2)
  assert.equal(native.filter(c => c.rejectPath && c.noUpstream).length, 2)
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
