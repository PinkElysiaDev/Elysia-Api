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
export function providerFailures(raw, httpStatus) {
  let frames
  try { const value = typeof raw === 'string' ? JSON.parse(raw) : raw; frames = Array.isArray(value) ? value : [value] }
  catch { try { frames = parseSSE(raw).map(frame => frame.value) } catch { return [] } }
  const failures = []
  for (const [frame, value] of frames.entries()) {
    if (httpStatus === 524 && value?.cloudflare_error === true && value.status === 524 && value.error_name === 'origin_response_timeout') {
      failures.push({ frame, type: 'upstream_http_timeout', code: 'origin_response_timeout', httpStatus, message: '上游源站响应超时（HTTP 524）' })
      continue
    }
    const payload = value?.response?.error ?? value?.error ?? (value?.type === 'error' ? value : undefined)
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) continue
    const fields = Object.fromEntries(['type', 'code', 'message', 'param', 'status'].filter(key => typeof payload[key] === 'string' || (key === 'code' && Number.isInteger(payload[key]))).map(key => [key, payload[key]]))
    if (!Object.keys(fields).length) continue
    failures.push({ frame, eventType: value?.type, ...fields })
  }
  return failures
}

// Only the typed failure message is suitable for summaries. Opaque CDN/HTML
// bodies remain in redacted evidence, rather than being echoed as the cause.
export function describeHTTPFailure(status, raw) {
  const failures = providerFailures(raw, status)
  const message = failures[0]?.message || `上游请求失败（HTTP ${status}）`
  const prefix = `HTTP ${status}: `
  let detail = message
  while (detail.startsWith(prefix)) detail = detail.slice(prefix.length)
  return { message: `${prefix}${detail}`, failures }
}

export function failureAnalysis({ kind, issues = [], httpStatuses = [], record, providerErrors = [], providerError, error } = {}) {
  const upstream = record?.providerFailures ?? (providerErrors.length ? providerErrors : providerError ? [{ ...providerError }] : [])
  const secondary = record?.conversionIssues?.filter(issue => issue.severity === 'error') || []
  const transport = record?.transportFailure
  const secondaryCategories = [...new Set(secondary.map(issue => issue.code === 'upstream_contract_violation' ? 'upstream_contract_failure' : 'gateway_conversion_failure'))]
  const upstreamHttpStatuses = kind === 'direct' ? httpStatuses : record?.upstreamHttpStatus ? [record.upstreamHttpStatus] : []
  const isHTTPTimeout = upstreamHttpStatuses.includes(524)
  const isRateLimit = httpStatuses.includes(429) || record?.errorKind === 'rate_limit' || upstream.some(failure =>
    ['gateway_concurrency_limit', 'rate_limit_exceeded', 'rate_limit_error', 'RESOURCE_EXHAUSTED'].includes(failure.code) ||
    ['rate_limit_error', 'rate_limit'].includes(failure.type) || failure.status === 'RESOURCE_EXHAUSTED' || failure.code === 429)
  return {
    primary: isHTTPTimeout ? { ...upstream[0], category: 'upstream_http_timeout', httpStatus: 524, message: upstream[0]?.message || '上游请求失败（HTTP 524）' } : upstream[0] || (record?.error ? { category: record.errorKind, message: record.error } : error),
    providerFailures: upstream, secondaryIssues: secondary, issues,
    transportFailure: transport,
    secondaryCategories: [...secondaryCategories, ...(transport && (isHTTPTimeout || upstream.length || upstreamHttpStatuses.some(status => status >= 400)) ? [transport.kind] : [])],
    isRateLimit, isHTTPTimeout,
    clientHttpStatuses: httpStatuses,
    upstreamHttpStatuses,
    logicalStatus: record?.statusCode,
  }
}

export function failureCategory(input = {}) {
  const { kind, issues = [], sdk, error, record } = input
  const analysis = failureAnalysis(input)
  if (analysis.isHTTPTimeout) return 'upstream_http_timeout'
  if (analysis.isRateLimit) return 'rate_limited'
  if (error?.code === 'timeout' && input.source === 'source-code') return 'verification_incomplete'
  if (issues.some(i => i.code === 'request_budget') || error?.code === 'request_budget') return 'budget_exhausted'
  if (record?.errorKind === 'client_canceled') return 'client_canceled'
  if (analysis.upstreamHttpStatuses.some(status => status >= 400)) return 'upstream_http_failure'
  if (analysis.providerFailures.length) return 'upstream_generation_failure'
  if (analysis.transportFailure?.kind === 'upstream_idle_timeout') return 'upstream_idle_timeout'
  if (analysis.transportFailure) return 'upstream_transport_failure'
  const failures = record?.conversionIssues?.filter(i => i.severity === 'error') || []
  // Explicit provider/transport failures take precedence. Mixed conversion
  // defects remain visible in secondaryCategories and their own report totals.
  if (failures.some(i => i.code !== 'upstream_contract_violation')) return 'gateway_conversion_failure'
  if (failures.length) return 'upstream_contract_failure'
  if (issues.length && issues.every(i => ['unexpected_text', 'tool_call_missing', 'tool_call_mismatch'].includes(i.code))) {
    // A gateway can corrupt a correct upstream tool call. Attribute instruction
    // failure to the model only on the direct baseline; relay traces need review.
    return kind === 'direct' ? 'model_instruction_mismatch' : 'needs_trace_review'
  }
  if (kind === 'direct') return 'upstream_direct_failure'
  if ([sdk, error?.sdk].some(entries => Array.isArray(entries) && entries.some(entry => entry.status === 'failed'))) return 'sdk_consumption_failure'
  // An HTTP error alone cannot distinguish provider rejection from gateway failure.
  return 'needs_trace_review'
}

/** Counts secondary defects separately; totals can overlap primary categories. */
export function failureCounts(cases) {
  const primary = {}, secondary = {}
  for (const item of cases.filter(item => !item.summary && ['failed', 'incomplete'].includes(item.status))) {
    if (item.failureCategory) primary[item.failureCategory] = (primary[item.failureCategory] || 0) + 1
    for (const category of item.failure?.secondaryCategories || []) secondary[category] = (secondary[category] || 0) + 1
  }
  return { primary, secondary }
}

/** Records observation separately from rule execution; never includes state values. */
export function compatibilityObservations(raw, record) {
  let frames
  try { const value = typeof raw === 'string' ? JSON.parse(raw) : raw; frames = Array.isArray(value) ? value : [value] }
  catch { try { frames = parseSSE(raw).map(frame => frame.value) } catch { frames = [] } }
  const hasEvidence = frames.some(frame => frame && typeof frame === 'object')
  const observed = {
    'responses-turn-state': frames.some(frame => frame?.type === 'response.metadata' && Object.keys(frame.headers || {}).some(key => key.toLowerCase() === 'x-codex-turn-state')),
    'chat-response-model': frames.some(frame => frame?.object === 'chat.completion' && !Object.hasOwn(frame, 'model')),
    'chat-stream-identity': frames.length >= 2 && frames.slice(0, 2).every(frame => frame?.object === 'chat.completion.chunk') && frames[0].id !== frames[1].id,
    'responses-heartbeat': frames.some(frame => frame?.['SSE-Keep-Alive'] === true),
  }
  return Object.entries(observed).map(([ruleId, extensionObserved]) => ({ ruleId, extensionObserved: hasEvidence ? extensionObserved : null, ruleTriggered: !!record?.conversionIssues?.some(issue => issue.ruleId === ruleId && issue.code === 'conversion_degraded') }))
}

/** Full consumption is recorded only after protocol or SDK acceptance finishes. */
export function finishCompatibilityCoverage(item) {
  for (const exchange of [...(item.compatibilityCoverage || []), ...(item.calls || [])]) {
    for (const entry of exchange.compatibility || []) entry.fullConsumptionSucceeded = item.status === 'passed' && !exchange.record?.error && !exchange.transportError
  }
}

/** Summarizes observed compatibility paths, without treating ordinary success as coverage. */
export function compatibilityCounts(cases) {
  const result = {}
  for (const item of cases.filter(item => !item.summary)) {
    const exchanges = item.calls?.length ? item.calls : item.compatibilityCoverage || []
    for (const exchange of exchanges) for (const entry of exchange.compatibility || []) {
      const counts = result[entry.ruleId] ||= { observed: 0, triggered: 0, consumed: 0, triggeredAndConsumed: 0 }
      if (entry.extensionObserved) counts.observed++
      if (entry.ruleTriggered) counts.triggered++
      if (entry.extensionObserved && entry.fullConsumptionSucceeded) counts.consumed++
      if (entry.ruleTriggered && entry.fullConsumptionSucceeded) counts.triggeredAndConsumed++
    }
  }
  return result
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
