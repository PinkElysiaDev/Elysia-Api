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
  const [result, setResult] = useState<{ inputKey: string; output: Awaited<ReturnType<typeof conversionAPI.preview>> }>()
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
    <section className="space-y-3 rounded-lg border p-4" aria-label="转换行为预览">
      <h2>逐阶段预览</h2>
      <select
        aria-label="预览阶段"
        className={field}
        value={phase}
        onChange={(e) => setPhase(e.target.value as ConversionPhase)}
      >
        {registry?.phases.map((p) => (
          <option key={p} value={p}>
            {phases[p]}
          </option>
        ))}
      </select>
      {phase === 'source_event' && <p className="text-sm text-muted-foreground">输入按接收顺序排列的完整上游 JSON 帧数组；source 为上游协议，target 为客户端协议。仅匹配空启动帧的身份规则，输出仍为帧数组。</p>}
      <label className="block">
        协议与模型上下文
        <textarea
          aria-label="行为预览上下文"
          className={field + ' h-28 font-mono'}
          value={context}
          onChange={(e) => setContext(e.target.value)}
        />
      </label>
      <label className="block">
        输入 JSON
        <textarea
          aria-label="行为预览输入"
          className={field + ' h-40 font-mono'}
          value={input}
          onChange={(e) => setInput(e.target.value)}
        />
      </label>
      <Button
        disabled={busy || !isValid}
        onClick={() => void action(loadPreview)}
      >
        预览行为
      </Button>
      {preview && (
        <>
          <ul className="text-xs">
            {Object.entries(preview.origins).map(([rule, origin]) => (
              <li key={rule}>
                {rule} ← {origin}
              </li>
            ))}
          </ul>
          <pre aria-label="行为预览结果" className="overflow-auto whitespace-pre-wrap text-xs">
            {preview.exactJSON}
          </pre>
        </>
      )}
    </section>
  )
}
