import { useState } from 'react'
import { newConversionPolicy, type ConversionPolicy, type ConversionRule } from '@/lib/conversion-policy'
import {
  appendConversionRule,
  moveConversionRule,
  parseConversionDocument,
  patchConversionFields,
  RULE_ORDER_STEP,
} from '@/lib/conversion-document'

/** Keep exact source text as the only editable policy state. */
export function useConversionDocument() {
  const [text, setText] = useState(() => JSON.stringify(newConversionPolicy(), null, 2))
  const parsed = parseConversionDocument(text)
  const policy = parsed.ok ? parsed.policy : undefined
  const parseError = parsed.ok ? '' : parsed.error
  const update = (change: Partial<ConversionPolicy>) => {
    if (policy) setText(patchConversionFields(text, '', change))
  }
  const updateRule = (index: number, change: Partial<ConversionRule>) => {
    if (policy) setText(patchConversionFields(text, `/rules/${index}`, change))
  }
  const updateFields = (pointer: string, change: Record<string, unknown>) => {
    if (policy) setText(patchConversionFields(text, pointer, change))
  }
  const addRule = () => {
    if (!policy) return
    setText(
      appendConversionRule(text, {
        id: `rule-${policy.rules.length + 1}`,
        order: (policy.rules.length + 1) * RULE_ORDER_STEP,
        enabled: true,
        phase: 'request',
        match: {},
        action: 'warn',
        reason: '自定义转换规则',
      }),
    )
  }
  const move = (index: number, offset: number) => {
    if (policy) setText(moveConversionRule(text, index, offset))
  }
  return { text, setText, policy, parseError, update, updateRule, updateFields, addRule, move }
}
