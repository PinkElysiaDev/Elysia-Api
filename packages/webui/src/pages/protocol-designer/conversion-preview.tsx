import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { conversionAPI, type ConversionPhase, type ConversionRegistry } from '@/lib/conversion-policy'
import { field, phases } from './conversion-fields'

type Props = {
  policyJSON: string
  isValid: boolean
  busy: boolean
  registry?: ConversionRegistry
  action: (run: () => Promise<void>) => Promise<void>
}

/** Preview a policy without rewriting its JSON numbers or persisting session state. */
export function ConversionPreview({ policyJSON, isValid, busy, registry, action }: Props) {
  const [phase, setPhase] = useState<ConversionPhase>('request')
  const [input, setInput] = useState(
    '{"schemaVersion":1,"source":{},"model":"example","content":[],"parameters":{"stream":false}}',
  )
  const [context, setContext] = useState(
    '{"source":{"definitionId":"openai-chat-completions","family":"openai_chat","wireVersion":"v2"},"target":{"definitionId":"google-generate-content","family":"gemini","wireVersion":"v2"},"model":"example","operation":"generate","transport":"http_json"}',
  )
  const inputKey = JSON.stringify([policyJSON, phase, context, input])
  const currentInput = useRef(inputKey)
  currentInput.current = inputKey
  const isMounted = useRef(true)
  useEffect(() => {
    isMounted.current = true
    return () => { isMounted.current = false }
  }, [])
  const [result, setResult] = useState<{
    inputKey: string
    output: { exactJSON: string; origins: Record<string, string> }
  }>()
  const preview = result?.inputKey === inputKey ? result.output : undefined
  const loadPreview = async () => {
    try {
      const output = await conversionAPI.preview(policyJSON, phase, context, input)
      if (isMounted.current && currentInput.current === inputKey) setResult({ inputKey, output })
    } catch (error) {
      if (isMounted.current && currentInput.current === inputKey) throw error
    }
  }
  return (
    <section className="space-y-4 border-t border-border/50 pt-5" aria-label="转换行为预览">
      <h2 className="text-sm font-semibold text-foreground">逐阶段预览</h2>
      <label className="block max-w-xs space-y-1.5 text-xs font-medium text-foreground">
        <span>预览阶段</span>
        <select
          aria-label="预览阶段"
          className={field}
          value={phase}
          onChange={(e) => {
            const next = e.target.value
            if (next in phases) setPhase(next as ConversionPhase)
          }}
        >
          {registry?.phases.map((p) => (
            <option key={p} value={p}>
              {phases[p]}
            </option>
          ))}
        </select>
      </label>
      {phase === 'source_event' && (
        <p className="text-xs leading-relaxed text-muted-foreground">
          输入按接收顺序排列的完整上游 JSON 帧数组；source 为上游协议，target
          为客户端协议。仅匹配空启动帧的身份规则，输出仍为帧数组。
        </p>
      )}
      {phase === 'response' && (
        <p className="text-xs leading-relaxed text-muted-foreground">
          预览 Chat 缺失模型补齐时，在上下文提供 responseModel，例如{' '}
          {`{"requestModel":"实际出站模型","sourcePresent":false}`}。源响应已有 model
          时，sourcePresent 为 true，并用 sourceModel 保留原值。补齐值不代表供应商确认的模型。
        </p>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        <label className="block space-y-1.5 text-xs font-medium text-foreground">
          <span>协议与模型上下文</span>
          <textarea
            aria-label="行为预览上下文"
            className="h-32 w-full rounded-md border border-input bg-card p-3 font-mono text-xs leading-relaxed text-foreground focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash"
            value={context}
            onChange={(e) => setContext(e.target.value)}
          />
        </label>
        <label className="block space-y-1.5 text-xs font-medium text-foreground">
          <span>输入 JSON</span>
          <textarea
            aria-label="行为预览输入"
            className="h-32 w-full rounded-md border border-input bg-card p-3 font-mono text-xs leading-relaxed text-foreground focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash"
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
        </label>
      </div>
      <Button disabled={busy || !isValid} onClick={() => void action(loadPreview)}>
        预览行为
      </Button>
      {preview && (
        <div className="space-y-3">
          <ul className="flex flex-wrap gap-2 text-xs">
            {Object.entries(preview.origins).map(([rule, origin]) => (
              <li
                key={rule}
                className="rounded-md border border-border/70 bg-secondary/30 px-2 py-0.5 font-mono text-2xs text-muted-foreground"
              >
                {rule} ← {origin}
              </li>
            ))}
          </ul>
          <pre
            aria-label="行为预览结果"
            className="max-h-96 overflow-auto whitespace-pre-wrap rounded-lg border border-border/60 bg-[var(--code-bg)] p-3.5 font-mono text-xs leading-relaxed"
          >
            {preview.exactJSON}
          </pre>
        </div>
      )}
    </section>
  )
}
