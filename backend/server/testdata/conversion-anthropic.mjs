import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { join } from 'node:path'

const [root, base, scenario] = process.argv.slice(2)
const require = createRequire(join(root, 'package.json'))
for (const name of ['@ai-sdk/anthropic-v2', '@ai-sdk/anthropic']) {
  const { createAnthropic } = require(name)
  const model = createAnthropic({ apiKey: 'gateway-test-token', baseURL: `${base}/gateway/anthropic-messages/v1` })('group')
  const result = await model.doStream({ prompt: [{ role: 'user', content: [{ type: 'text', text: 'hello' }] }], maxOutputTokens: 100 })
  let text = '', finish
  for await (const event of result.stream) {
    assert.notEqual(event.type, 'error', JSON.stringify(event.error))
    if (event.type === 'text-delta') text += event.delta ?? event.textDelta ?? ''
    if (event.type === 'finish') finish = event
  }
  assert.equal(text, 'hello')
  assert.ok(finish)
  const input = typeof finish.usage.inputTokens === 'object' ? finish.usage.inputTokens.total : finish.usage.inputTokens
  if (scenario === 'first') assert.equal(input, 3)
  if (scenario === 'late') assert.equal(input, name.endsWith('-v2') ? 0 : 3)
  console.log(JSON.stringify({ name, scenario, input, finish: true }))
}
