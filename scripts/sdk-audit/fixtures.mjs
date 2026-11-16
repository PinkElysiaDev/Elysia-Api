import assert from 'node:assert/strict'

const protocols = ['openai-chat-completions', 'openai-responses', 'anthropic-messages', 'google-generate-content']
// Expectations come from the single-tool Mapping samples and auditParallelToolFixture.
const singleTools = [{ id: 'call', name: 'lookup', args: { n: 9007199254740993n } }]
const parallelTools = [{ id: 'c1', name: 'lookup', args: { n: 1 } }, { id: 'c2', name: 'lookup', args: { n: 2 } }]

function buildCaptureName(upstream, ingress, stream, kind, second) {
  const pair = `${upstream}-${ingress}-${stream}`
  return kind === 'text' ? `${pair}.json` : `${kind}-${pair}-${second}.json`
}

/** Validate capture identity and derive semantics from the independent fixture producer. */
export function describeCapture(file, fixture) {
  assert(protocols.includes(fixture.upstream) && protocols.includes(fixture.ingress), 'unknown capture protocol')
  assert.equal(typeof fixture.stream, 'boolean', 'capture transport is missing')
  assert.equal(typeof fixture.body, 'string', 'capture body is missing')
  let kind = 'text'
  if (file.startsWith('startup-parallel-tool-')) kind = 'startup-parallel-tool'
  else if (file.startsWith('parallel-tool-')) kind = 'parallel-tool'
  else if (file.startsWith('tool-')) kind = 'tool'
  if (kind !== 'text') assert.equal(typeof fixture.tool, 'boolean', 'tool round is missing')
  else assert.equal(fixture.tool, undefined, 'text fixture cannot declare a tool round')
  assert.equal(file, buildCaptureName(fixture.upstream, fixture.ingress, fixture.stream, kind, !fixture.tool), 'capture filename disagrees with its identity')
  const tools = fixture.tool ? kind === 'tool' ? singleTools : parallelTools : []
  return { file, kind, round: kind === 'text' ? 0 : fixture.tool ? 1 : 2, expected: { text: fixture.tool && kind === 'tool' ? '' : 'hi', tools } }
}

/** Require the complete text/tool Cartesian matrix; partial captures stay explicit. */
export function summarizeCoverage(captures) {
  const actual = new Set(captures.map(capture => capture.file))
  assert.equal(actual.size, captures.length, 'duplicate capture identity')
  const expected = new Set()
  for (const upstream of protocols) {
    for (const ingress of protocols) {
      for (const stream of [false, true]) {
        const text = buildCaptureName(upstream, ingress, stream, 'text')
        expected.add(text)
        for (const kind of ['tool', 'parallel-tool']) {
          for (const second of [false, true]) {
            const name = buildCaptureName(upstream, ingress, stream, kind, second)
            expected.add(name)
          }
        }
      }
    }
  }
  const difference = (left, right) => [...left].filter(name => !right.has(name)).sort()
  const missing = difference(expected, actual)
  return {
    producer: 'TestGatewayAuditTextMatrix + TestGatewayAuditToolRoundTrips + TestGatewayAuditParallelToolRoundTrips',
    present: [...actual].sort(), missingCurrent: missing, outsideCurrent: difference(actual, expected),
    missingCartesian: missing, completeCurrent: missing.length === 0, completeCartesian: missing.length === 0,
  }
}
