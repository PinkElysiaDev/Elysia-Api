import { AuditError, parseSSE } from './protocols.mjs'

/** Observe complete SSE frames before cancelling; TCP bytes alone may be an error. */
export function cancellationProbe(protocol, abort) {
  const decoder = new TextDecoder()
  let pending = '', hasContent = false, hasTerminal = false
  return {
    get hasContent() { return hasContent },
    get hasTerminal() { return hasTerminal },
    push(chunk) {
      pending += decoder.decode(chunk, { stream: true })
      let boundary
      while ((boundary = /\r\n\r\n|\n\n|\r\r/.exec(pending))) {
        const raw = pending.slice(0, boundary.index + boundary[0].length)
        pending = pending.slice(boundary.index + boundary[0].length)
        let frames
        try { frames = parseSSE(raw) } catch (error) { if (error.code === 'empty_stream') continue; throw error }
        for (const { type, value } of frames) {
          if (type === 'error' || value?.type === 'error' || value?.error || value?.response?.status === 'failed') {
            throw new AuditError('stream_error', value?.response?.error?.message || value?.error?.message || value?.message || 'Stream failed before cancellation')
          }
          if (type === 'done' || ['response.completed', 'response.incomplete', 'response.cancelled', 'message_stop'].includes(value?.type) || value?.choices?.some(c => c.finish_reason != null) || value?.candidates?.some(c => c.finishReason)) hasTerminal = true
          const text = protocol === 'chat' ? value?.choices?.find(c => c.index === 0)?.delta?.content
            : protocol === 'responses' ? (value?.type === 'response.output_text.delta' ? value.delta : undefined)
              : protocol === 'anthropic' ? (value?.type === 'content_block_delta' && value.delta?.type === 'text_delta' ? value.delta.text : undefined)
                : value?.candidates?.[0]?.content?.parts?.filter(p => !p.thought).map(p => p.text || '').join('')
          hasContent ||= typeof text === 'string' && text.length > 0
        }
      }
      // Inspect every complete frame in this read before aborting: an error may
      // follow content in the same TCP chunk. Partial errors remain in evidence.
      if (hasContent && !hasTerminal) abort()
    },
  }
}

/** A persisted provider/contract failure must not count as successful cancellation. */
export function assertCancellationRecord(trace) {
  if (!trace?.available || !trace.record) throw new AuditError('cancellation_unverified', 'Cancellation call record unavailable')
  const record = trace.record
  const isClientCancellation = record.errorKind === 'client_canceled' && record.statusCode === 499
  const issue = record.conversionIssues?.find(issue => issue.severity === 'error' && !(
    isClientCancellation && issue.code === 'invalid_input' && issue.stage === 'runtime' &&
    ['decode_event', 'encode_event'].includes(issue.direction) && issue.reason === 'context canceled'
  ))
  if (issue || record.providerFailures?.length) throw new AuditError('cancellation_failed', issue?.reason || 'Provider failed before cancellation')
  if (!isClientCancellation) {
    if (record.error) throw new AuditError('cancellation_failed', record.error)
    throw new AuditError('cancellation_unverified', 'Gateway did not record client cancellation')
  }
}
