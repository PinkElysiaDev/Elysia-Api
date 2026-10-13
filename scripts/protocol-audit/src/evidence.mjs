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

// Inspect only explicit error containers. Arbitrary generated text is never an
// error classifier, including text that happens to mention a quota or failure.
export function providerFailures(raw) {
  let frames
  try { const value = typeof raw === 'string' ? JSON.parse(raw) : raw; frames = Array.isArray(value) ? value : [value] }
  catch { try { frames = parseSSE(raw).map(frame => frame.value) } catch { return [] } }
  const failures = []
  for (const [frame, value] of frames.entries()) {
    const payload = value?.response?.error ?? value?.error ?? (value?.type === 'error' ? value : undefined)
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) continue
    const fields = Object.fromEntries(['type', 'code', 'message', 'param', 'status'].filter(key => typeof payload[key] === 'string' || (key === 'code' && Number.isInteger(payload[key]))).map(key => [key, payload[key]]))
    if (!Object.keys(fields).length) continue
    failures.push({ frame, eventType: value?.type, ...fields })
  }
  return failures
}

export function failureAnalysis({ kind, issues = [], httpStatuses = [], record, providerErrors = [], providerError, error } = {}) {
  const upstream = record?.providerFailures ?? (providerErrors.length ? providerErrors : providerError ? [{ ...providerError }] : [])
  const secondary = record?.conversionIssues?.filter(issue => issue.severity === 'error') || []
  const isRateLimit = httpStatuses.includes(429) || record?.errorKind === 'rate_limit' || upstream.some(failure =>
    ['gateway_concurrency_limit', 'rate_limit_exceeded', 'rate_limit_error', 'RESOURCE_EXHAUSTED'].includes(failure.code) ||
    ['rate_limit_error', 'rate_limit'].includes(failure.type) || failure.status === 'RESOURCE_EXHAUSTED' || failure.code === 429)
  return {
    primary: upstream[0] || (record?.error ? { category: record.errorKind, message: record.error } : error),
    providerFailures: upstream, secondaryIssues: secondary, issues,
    isRateLimit,
    clientHttpStatuses: httpStatuses,
    upstreamHttpStatuses: kind === 'direct' ? httpStatuses : record?.upstreamHttpStatus ? [record.upstreamHttpStatus] : [],
    logicalStatus: record?.statusCode,
  }
}

export function failureCategory(input = {}) {
  const { kind, issues = [], sdk, error, record } = input
  if (failureAnalysis(input).isRateLimit) return 'rate_limited'
  if (error?.code === 'timeout' && input.source === 'source-code') return 'verification_incomplete'
  if (issues.some(i => i.code === 'request_budget') || error?.code === 'request_budget') return 'budget_exhausted'
  const failures = record?.conversionIssues?.filter(i => i.severity === 'error') || []
  // The gateway also records contract violations raised while reading the
  // provider, including non-protocol CDN error bodies. They are failed calls,
  // but do not establish a defect in the downstream conversion. Keep a mixed
  // diagnosis classified as conversion failure so this cannot hide one.
  if (failures.some(i => i.code !== 'upstream_contract_violation')) return 'gateway_conversion_failure'
  if (failures.length) return 'upstream_contract_failure'
  if (issues.length && issues.every(i => ['unexpected_text', 'tool_call_missing', 'tool_call_mismatch'].includes(i.code))) {
    // A gateway can corrupt a correct upstream tool call. Attribute instruction
    // failure to the model only on the direct baseline; relay traces need review.
    return kind === 'direct' ? 'model_instruction_mismatch' : 'needs_trace_review'
  }
  if (kind === 'direct') return 'upstream_direct_failure'
  if (sdk || error?.sdk) return 'sdk_consumption_failure'
  // An HTTP error alone cannot distinguish provider rejection from gateway failure.
  return 'needs_trace_review'
}

export function requestSelection(body) {
  if (typeof body === 'string') {
    try { body = JSON.parse(body) } catch { return { unavailable: 'invalid_json' } }
  }
  return { include: body?.include, store: body?.store }
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
