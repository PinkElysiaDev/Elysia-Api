import { useEffect, useMemo, useRef, useState } from 'react'
import { FlaskConical, Play, RefreshCw } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import type {
  CustomProtocolConfig,
  CustomProtocolModelsTestResult,
  CustomProtocolPreviewResult,
  CustomProtocolTestResult,
  Model,
  ModelSource,
} from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Seg } from '@/components/ui/seg'
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
import { customPlatformValue } from '@/lib/protocol'

const PREVIEW_REFRESH_DEBOUNCE_MS = 800
const TEST_MODEL_DATALIST_ID = 'custom-protocol-test-models'

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

type CredentialMode = 'ad-hoc' | 'source'

/** 预览与测试：渲染预览（凭证打码）+ 临时凭据或已存模型源真实发送一次请求并对照映射结果。 */
export function PreviewTestPanel({
  protocol,
  sources,
  models,
}: {
  protocol: CustomProtocolConfig
  sources: ModelSource[]
  models: Model[]
}) {
  const { success: toastSuccess, error: toastError } = useToast()
  const [sampleText, setSampleText] = useState(DEFAULT_SAMPLE_REQUEST)
  const [preview, setPreview] = useState<CustomProtocolPreviewResult | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [credentialMode, setCredentialMode] = useState<CredentialMode>('ad-hoc')
  const [adhocBaseUrl, setAdhocBaseUrl] = useState('')
  const [adhocApiKey, setAdhocApiKey] = useState('')
  const [adhocModel, setAdhocModel] = useState('')
  const [discoveredModels, setDiscoveredModels] = useState<CustomProtocolModelsTestResult['models'] | null>(null)
  const [modelsLoading, setModelsLoading] = useState(false)
  const [modelsError, setModelsError] = useState<string | null>(null)
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

  // 引用本协议的模型源（platform = custom:<本协议 id>）。
  const protocolSources = useMemo(() => {
    const prefix = customPlatformValue(protocol.id.trim().toLowerCase())
    return sources.filter((source) => source.platform.trim().toLowerCase() === prefix)
  }, [sources, protocol.id])

  const sourceModels = useMemo(
    () => models.filter((model) => model.sourceId === testSourceId && model.enabled),
    [models, testSourceId],
  )

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
      const result = await api.previewCustomProtocol(
        { protocol, ...(sampleRequest ? { sampleRequest } : {}) },
        { signal: controller.signal },
      )
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
    debounceRef.current = window.setTimeout(() => void runPreview(), PREVIEW_REFRESH_DEBOUNCE_MS)
    return () => {
      if (debounceRef.current) window.clearTimeout(debounceRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [protocol, sampleText])

  const executeTest = async (target: { model: string; sourceId?: string; baseUrl?: string; apiKey?: string }) => {
    setTestLoading(true)
    setTestResult(null)
    try {
      const result = await api.testCustomProtocol({
        protocol,
        model: target.model,
        ...(target.sourceId ? { sourceId: target.sourceId } : {}),
        ...(target.baseUrl ? { baseUrl: target.baseUrl, ...(target.apiKey ? { apiKey: target.apiKey } : {}) } : {}),
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

  const runTest = async () => {
    if (credentialMode === 'ad-hoc') {
      if (!adhocBaseUrl.trim() || !adhocModel.trim()) {
        toastError('请填写测试目标', '临时测试需要 baseUrl 与模型名')
        return
      }
      await executeTest({
        baseUrl: adhocBaseUrl.trim(),
        ...(adhocApiKey.trim() ? { apiKey: adhocApiKey.trim() } : {}),
        model: adhocModel.trim(),
      })
      return
    }
    if (!testSourceId || !testModel) {
      toastError('请选择测试模型', '需要选择模型源与模型')
      return
    }
    await executeTest({ sourceId: testSourceId, model: testModel })
  }

  // 模型发现试拉：按协议 models 配置请求上游，结果填充模型下拉候选。
  const runModelsTest = async () => {
    if (!adhocBaseUrl.trim()) {
      toastError('请填写 baseUrl', '模型发现试拉需要临时 baseUrl')
      return
    }
    setModelsLoading(true)
    setModelsError(null)
    try {
      const result = await api.testCustomProtocolModels({
        protocol,
        baseUrl: adhocBaseUrl.trim(),
        ...(adhocApiKey.trim() ? { apiKey: adhocApiKey.trim() } : {}),
      })
      setDiscoveredModels(result.models ?? [])
      if (result.parseError) {
        setModelsError(result.parseError)
      } else if (!result.models?.length) {
        setModelsError('未发现模型：检查 models.listPath 配置与上游返回结构')
      } else {
        toastSuccess('模型发现完成', `发现 ${result.models.length} 个模型，点击下方模型名填入`)
      }
    } catch (error) {
      setDiscoveredModels(null)
      setModelsError(error instanceof ApiError ? error.message : String(error))
    } finally {
      setModelsLoading(false)
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

      <SettingSection
        title="真实测试"
        description="以临时凭据或所选模型源向上游实际发送一次请求（产生真实用量），无需先保存协议"
      >
        <SettingRow label="凭据来源">
          <Seg
            value={credentialMode}
            options={[
              { value: 'ad-hoc', label: '临时输入' },
              { value: 'source', label: '已保存模型源' },
            ]}
            onChange={(value) => setCredentialMode(value as CredentialMode)}
          />
        </SettingRow>

        {credentialMode === 'ad-hoc' ? (
          <>
            <SettingRow label="Base URL" required description="上游根地址，协议 path 与其拼接">
              <Input
                className="w-72 font-mono text-xs"
                value={adhocBaseUrl}
                placeholder="https://api.vendor.com"
                onChange={(event) => setAdhocBaseUrl(event.target.value)}
              />
            </SettingRow>
            <SettingRow label="API Key" description="仅本次测试使用，不落库">
              <Input
                className="w-72 font-mono text-xs"
                type="password"
                value={adhocApiKey}
                placeholder="sk-…"
                onChange={(event) => setAdhocApiKey(event.target.value)}
              />
            </SettingRow>
            <SettingRow label="模型" required description="自由填写，或先试拉模型列表后点选">
              <Input
                className="w-72 font-mono text-xs"
                value={adhocModel}
                placeholder="vendor-model-name"
                list={TEST_MODEL_DATALIST_ID}
                onChange={(event) => setAdhocModel(event.target.value)}
              />
              <datalist id={TEST_MODEL_DATALIST_ID}>
                {(discoveredModels ?? []).map((model) => (
                  <option key={model.id} value={model.id}>
                    {model.name !== model.id ? model.name : undefined}
                  </option>
                ))}
              </datalist>
            </SettingRow>
            {protocol.models?.path && (
              <div className="space-y-2">
                <Button type="button" variant="outline" size="sm" disabled={modelsLoading} onClick={() => void runModelsTest()}>
                  <RefreshCw className="mr-1 h-3 w-3" /> {modelsLoading ? '拉取中…' : '拉取模型列表'}
                </Button>
                {modelsError && (
                  <p className="rounded-md border border-destructive/40 bg-destructive/5 p-2 text-xs text-destructive">
                    {modelsError}
                  </p>
                )}
                {(discoveredModels ?? []).length > 0 && (
                  <div className="flex flex-wrap gap-1.5">
                    {(discoveredModels ?? []).map((model) => (
                      <button
                        key={model.id}
                        type="button"
                        className="rounded-md border border-border bg-card px-2 py-0.5 font-mono text-2xs hover:bg-accent"
                        title={model.name !== model.id ? model.name : undefined}
                        onClick={() => setAdhocModel(model.id)}
                      >
                        {model.id}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )}
          </>
        ) : (
          <>
            <SettingRow label="模型源" description="仅列出引用本协议的源">
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
                  {protocolSources.map((source) => (
                    <SelectItem key={source.id} value={source.id}>
                      {source.name || source.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </SettingRow>
            {protocolSources.length === 0 && (
              <p className="text-xs text-muted-foreground">
                没有引用本协议的模型源——请先在模型源页创建（API 协议选本协议），或改用「临时输入」。
              </p>
            )}
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
          </>
        )}

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
