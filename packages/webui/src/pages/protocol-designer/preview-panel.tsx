import { useEffect, useMemo, useRef, useState } from 'react'
import { FlaskConical, Play } from 'lucide-react'
import { api, ApiError, request } from '@/lib/api'
import type {
  CustomProtocolConfig,
  CustomProtocolPreviewResult,
  CustomProtocolTestResult,
  Model,
  ModelSource,
} from '@/lib/types'
import { Button } from '@/components/ui/button'
import { SettingRow, SettingSection } from '@/components/ui/setting-card'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/input'
import { useToast } from '@/components/ui/use-toast'

const DEFAULT_SAMPLE_REQUEST = JSON.stringify(
  {
    model: 'sample-model',
    messages: [
      { role: 'system', content: [{ type: 'text', text: 'You are a helpful assistant.' }] },
      { role: 'user', content: [{ type: 'text', text: 'Hello!' }] },
    ],
    max_output_tokens: 1024,
    temperature: 0.7,
    stream: false,
  },
  null,
  2,
)

function JSONBlock({ text, className }: { text: string; className?: string }) {
  return (
    <pre
      className={`max-h-72 overflow-auto rounded-md border border-border bg-card p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all ${className ?? ''}`}
    >
      {text}
    </pre>
  )
}

/** 预览与测试：渲染预览（凭证打码）+ 向所选模型源真实发送一次请求并对照映射结果。 */
export function PreviewTestPanel({
  protocol,
  onSaved,
  sources,
  models,
}: {
  protocol: CustomProtocolConfig
  onSaved: () => Promise<boolean>
  sources: ModelSource[]
  models: Model[]
}) {
  const { success: toastSuccess, error: toastError } = useToast()
  const [sampleText, setSampleText] = useState(DEFAULT_SAMPLE_REQUEST)
  const [preview, setPreview] = useState<CustomProtocolPreviewResult | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [testSourceId, setTestSourceId] = useState('')
  const [testModel, setTestModel] = useState('')
  const [testStream, setTestStream] = useState(false)
  const [testLoading, setTestLoading] = useState(false)
  const [testResult, setTestResult] = useState<CustomProtocolTestResult | null>(null)
  const debounceRef = useRef<number | null>(null)

  const sampleRequest = useMemo(() => {
    try {
      return JSON.parse(sampleText) as unknown
    } catch {
      return undefined
    }
  }, [sampleText])

  // 代次守卫 + AbortController:防抖期间的连续触发若与手动点击并发,旧响应
  // 后到会覆盖新结果、先到方提前复位 loading;请求经 api.request 的 signal 取消。
  const previewAbort = useRef<AbortController | null>(null)
  const previewGen = useRef(0)
  const runPreview = async () => {
    previewAbort.current?.abort()
    const controller = new AbortController()
    previewAbort.current = controller
    const gen = ++previewGen.current
    setPreviewLoading(true)
    setPreviewError(null)
    try {
      const result = await request<CustomProtocolPreviewResult>('/custom-protocols/preview', {
        method: 'POST',
        body: { protocol, ...(sampleRequest ? { sampleRequest } : {}) },
        signal: controller.signal,
      })
      if (gen !== previewGen.current) return
      setPreview(result)
    } catch (error) {
      if (gen !== previewGen.current || controller.signal.aborted) return
      setPreview(null)
      setPreviewError(error instanceof ApiError ? error.message : String(error))
    } finally {
      if (gen === previewGen.current) setPreviewLoading(false)
    }
  }

  // 已有结果时，协议/样例变化后防抖刷新。
  useEffect(() => {
    if (!preview && !previewError) return
    if (debounceRef.current) window.clearTimeout(debounceRef.current)
    debounceRef.current = window.setTimeout(() => void runPreview(), 800)
    return () => {
      if (debounceRef.current) window.clearTimeout(debounceRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [protocol, sampleText])

  const sourceModels = useMemo(
    () => models.filter((model) => model.sourceId === testSourceId && model.enabled),
    [models, testSourceId],
  )

  const runTest = async () => {
    if (!testSourceId || !testModel) {
      toastError('请选择测试模型', '需要选择模型源与模型')
      return
    }
    if (!(await onSaved())) return
    setTestLoading(true)
    setTestResult(null)
    try {
      const result = await api.testCustomProtocol({
        protocol,
        sourceId: testSourceId,
        model: testModel,
        stream: testStream,
        ...(sampleRequest ? { sampleRequest } : {}),
      })
      setTestResult(result)
      toastSuccess('测试完成', `状态码 ${result.statusCode} · ${result.durationMs}ms`)
    } catch (error) {
      toastError('测试失败', error instanceof ApiError ? error.message : String(error))
    } finally {
      setTestLoading(false)
    }
  }

  return (
    <div className="space-y-8">
      <SettingSection title="样例请求" description="预览与测试共用的 Maheshvara 请求样例">
        <Textarea
          className="min-h-[140px] font-mono text-xs"
          spellCheck={false}
          value={sampleText}
          onChange={(event) => setSampleText(event.target.value)}
        />
        <p className="text-2xs text-muted-foreground">测试时 model 自动替换为所选模型，stream 跟随测试开关。</p>
      </SettingSection>

      <SettingSection
        title="渲染预览"
        action={
          <Button type="button" variant="outline" size="sm" disabled={previewLoading} onClick={() => void runPreview()}>
            <Play className="mr-1 h-3 w-3" /> {previewLoading ? '渲染中…' : '渲染预览'}
          </Button>
        }
      >
        {previewError ? (
          <p className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive">
            {previewError}
          </p>
        ) : preview ? (
          <div className="space-y-2">
            <p className="text-xs text-muted-foreground">
              <span className="font-mono font-semibold text-foreground">
                {preview.method} {preview.path || '/'}
              </span>{' '}
              · {preview.contentType} · {preview.authPreview}
            </p>
            {preview.query && Object.keys(preview.query).length > 0 && (
              <JSONBlock text={JSON.stringify(preview.query, null, 2)} className="max-h-28" />
            )}
            <JSONBlock text={JSON.stringify(preview.headers, null, 2)} className="max-h-36" />
            {preview.body && <JSONBlock text={preview.body} />}
          </div>
        ) : (
          <p className="py-2 text-xs text-muted-foreground">按样例渲染协议，展示真实发送形态（API key 打码）。</p>
        )}
      </SettingSection>

      <SettingSection title="真实测试" description="向所选模型源实际发送一次请求（产生真实用量）">
        <SettingRow label="模型源">
          <Select
            value={testSourceId}
            onValueChange={(value) => {
              setTestSourceId(value)
              setTestModel('')
            }}
          >
            <SelectTrigger className="w-56" aria-label="测试模型源">
              <SelectValue placeholder="选择模型源" />
            </SelectTrigger>
            <SelectContent>
              {sources.map((source) => (
                <SelectItem key={source.id} value={source.id}>
                  {source.name || source.id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
        <SettingRow label="模型">
          <Select value={testModel} onValueChange={setTestModel}>
            <SelectTrigger className="w-56" aria-label="测试模型">
              <SelectValue placeholder="选择模型" />
            </SelectTrigger>
            <SelectContent>
              {sourceModels.map((model) => (
                <SelectItem key={model.id} value={model.name || model.id}>
                  {model.name || model.id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
        <SettingRow label="流式" description="按 SSE 发送并采样前 50 个事件">
          <Switch checked={testStream} onCheckedChange={setTestStream} />
        </SettingRow>
        <div className="pt-2">
          <Button type="button" disabled={testLoading} onClick={() => void runTest()}>
            <FlaskConical className="mr-1.5 h-3.5 w-3.5" />
            {testLoading ? '测试中…' : '发送测试请求'}
          </Button>
        </div>
        {testResult && (
          <div className="space-y-3 pt-2">
            <p className="text-xs text-muted-foreground">
              状态码 <span className="font-mono font-semibold text-foreground">{testResult.statusCode}</span> · 耗时{' '}
              {testResult.durationMs}ms · 模型 {testResult.targetModel}
            </p>
            {testResult.rawBody && (
              <div>
                <p className="mb-1 text-xs font-medium text-foreground">上游原始响应</p>
                <JSONBlock text={testResult.rawBody} />
              </div>
            )}
            {testResult.events && (
              <div>
                <p className="mb-1 text-xs font-medium text-foreground">上游流事件采样</p>
                <JSONBlock
                  text={testResult.events
                    .map((event) => `event: ${event.event || '(默认)'}\ndata: ${event.data}`)
                    .join('\n\n')}
                />
              </div>
            )}
            {testResult.decoded && testResult.decoded.length > 0 && (
              <div>
                <p className="mb-1 text-xs font-medium text-foreground">解码出的 Maheshvara 流事件</p>
                <JSONBlock text={JSON.stringify(testResult.decoded, null, 2)} />
              </div>
            )}
            {testResult.maheshvara !== undefined && (
              <div>
                <p className="mb-1 text-xs font-medium text-foreground">映射结果（Maheshvara）</p>
                <JSONBlock text={JSON.stringify(testResult.maheshvara, null, 2)} />
              </div>
            )}
            {testResult.mappingError && (
              <p className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive">
                映射失败：{testResult.mappingError}
              </p>
            )}
            {testResult.streamError && (
              <p className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive">
                流式错误：{testResult.streamError}
              </p>
            )}
          </div>
        )}
      </SettingSection>
    </div>
  )
}
