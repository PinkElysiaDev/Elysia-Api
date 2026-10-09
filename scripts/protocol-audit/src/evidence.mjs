import { setTimeout as delay } from 'node:timers/promises'
import { parseSSE } from './protocols.mjs'

export const presetIDs = ['openai-chat-completions', 'openai-responses', 'anthropic-messages', 'google-generate-content']

export function runtimeReadiness(state) {
  const issues = []
  if (state?.runtimeReady !== true) issues.push('runtimeReady is not true')
  for (const id of presetIDs) {
    const active = state?.active?.find(a => a.protocolId === id)?.revisionHash
    if (!active || state?.loaded?.[id] !== active) issues.push(`${id}: loaded revision does not match activation`)
    if (state?.runtimeFailures?.[id]) issues.push(`${id}: ${state.runtimeFailures[id]}`)
  }
  if (state?.startupFailure) issues.push(`${state.startupFailure.stage}: ${state.startupFailure.reason || state.startupFailure.message}`)
  return { ready: issues.length === 0, issues, state }
}

// A successful health response only establishes that the management server exists.
export async function waitForRuntime(baseUrl, panel, { signal, timeoutMs = 15000, intervalMs = 100, alive = () => true } = {}) {
  const deadline = Date.now() + timeoutMs
  let last = { ready: false, issues: ['Management API has not responded'] }
  do {
    signal?.throwIfAborted()
    if (!alive()) break
    try {
      const response = await fetch(`${baseUrl}/api/admin/protocols`, {
        headers: { authorization: `Bearer ${panel}` },
        signal: AbortSignal.any([AbortSignal.timeout(Math.max(1, Math.min(1000, deadline - Date.now()))), ...(signal ? [signal] : [])]),
      })
      const body = await response.json()
      last = response.ok && body.ok ? runtimeReadiness(body.data) : { ready: false, issues: [`Management HTTP ${response.status}`], response: body }
      if (last.ready) return last.state
      if (last.state?.startupFailure) break
    } catch (error) { last = { ...last, transportError: error.message } }
    await delay(intervalMs, undefined, { signal })
  } while (Date.now() < deadline)
  throw Object.assign(new Error(`Protocol runtime is not ready: ${last.issues.join('; ')}`), { code: 'runtime_not_ready', startupFailure: last })
}

export function eventSequence(raw, stream) {
  if (!stream) return []
  try {
    return parseSSE(raw).map(({ type, value }, index) => ({
      index, type: value?.type || type || 'data',
      ...(value?.sequence_number !== undefined ? { sequence: value.sequence_number } : {}),
      ...(value?.output_index !== undefined ? { outputIndex: value.output_index } : {}),
      ...(value?.index !== undefined ? { blockIndex: value.index } : {}),
    }))
  } catch (error) { return [{ error: error.code || 'invalid_stream', path: error.path }] }
}

export function failureCategory({ kind, issues = [], httpStatuses = [], sdk, error, record } = {}) {
  if (httpStatuses.includes(429)) return 'rate_limited'
  if (issues.some(i => i.code === 'request_budget') || error?.code === 'request_budget') return 'budget_exhausted'
  if (issues.length && issues.every(i => ['unexpected_text', 'tool_call_missing', 'tool_call_mismatch'].includes(i.code))) return 'model_instruction_mismatch'
  if (record?.conversionIssues?.some(i => i.severity === 'error')) return 'gateway_conversion_failure'
  if (kind === 'direct') return 'upstream_direct_failure'
  if (sdk || error?.sdk) return 'sdk_consumption_failure'
  // An HTTP error alone cannot distinguish provider rejection from gateway failure.
  return 'needs_trace_review'
}

export async function persistedCall(baseUrl, panel, requestId, { timeoutMs = 5000, intervalMs = 100 } = {}) {
  if (!requestId) return { available: false, reason: 'No gateway request ID; discovery/authentication may precede call recording' }
  if (!/^req_[0-9]+(?:_[a-f0-9]+)?$/.test(requestId)) throw new Error('Invalid gateway request ID')
  const deadline = Date.now() + timeoutMs
  do {
    const response = await fetch(`${baseUrl}/api/admin/usage/logs/${encodeURIComponent(requestId)}`, {
      headers: { authorization: `Bearer ${panel}` }, signal: AbortSignal.timeout(Math.max(1, Math.min(1000, deadline - Date.now()))),
    })
    const body = await response.json()
    if (response.ok && body.ok && body.data?.requestId === requestId) return { available: true, record: body.data }
    if (response.status !== 404) throw new Error(`Call evidence HTTP ${response.status}: ${body.error?.message || 'unexpected detail response'}`)
    await delay(intervalMs)
  } while (Date.now() < deadline)
  throw Object.assign(new Error(`Persisted call ${requestId} was not available before cleanup`), { code: 'missing_call_evidence' })
}
