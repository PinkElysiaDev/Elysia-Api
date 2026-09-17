import { useEffect, useMemo, useRef, useState } from 'react'
import { FileText, Image as ImageIcon, Paperclip, Plus, Send, Sparkles, Trash2, X } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import type {
  CustomProtocolAssistDocument,
  CustomProtocolAssistResult,
  CustomProtocolAssistVerification,
  CustomProtocolConfig,
  Model,
  ModelSource,
} from '@/lib/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useToast } from '@/components/ui/use-toast'
import { cn } from '@/lib/utils'

/**
 * AI 协议助手：上传 API 文档/截图/示例 → 所选模型读取材料生成字段级映射配置
 * （服务端自动校验并修复，离线验证渲染与映射）→ 应用到编辑器继续迭代。
 */

interface AssistTurn {
  role: 'user' | 'assistant'
  text: string
  draft?: CustomProtocolConfig
  valid?: boolean
  issues?: string
  rounds?: number
  verification?: CustomProtocolAssistVerification
  documentNames?: string[]
}

const STORAGE_KEY = 'elysia.protocolAssistant.target'

/** 本地文件 → 助手文档：文本类内联，图片/二进制走 dataURL 交给模型原生能力。 */
async function fileToDocument(file: File): Promise<CustomProtocolAssistDocument> {
  const isText =
    file.type.startsWith('text/') ||
    /\.(md|txt|json|jsonl|ya?ml|csv|html?|xml|ts|js|py|go)$/i.test(file.name)
  if (isText) {
    return { name: file.name, mime: file.type || 'text/plain', text: await file.text() }
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () =>
      resolve({ name: file.name, mime: file.type || 'application/octet-stream', dataUrl: String(reader.result) })
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

function VerificationBlock({ verification }: { verification: CustomProtocolAssistVerification }) {
  return (
    <div className="space-y-1.5">
      {verification.request && (
        <div>
          <p className="text-2xs font-medium text-foreground">
            离线渲染（{verification.request.method} {verification.request.path || '/'}）
          </p>
          <pre className="max-h-28 overflow-auto whitespace-pre-wrap break-all rounded bg-background/70 p-1.5 font-mono text-2xs text-muted-foreground">
            {verification.request.body || '（无请求体）'}
          </pre>
        </div>
      )}
      {verification.requestError && (
        <p className="text-2xs text-destructive">渲染失败：{verification.requestError}</p>
      )}
      {verification.mappedResponse !== undefined && (
        <div>
          <p className="text-2xs font-medium text-foreground">示例响应映射结果</p>
          <pre className="max-h-28 overflow-auto whitespace-pre-wrap break-all rounded bg-background/70 p-1.5 font-mono text-2xs text-muted-foreground">
            {JSON.stringify(verification.mappedResponse, null, 2)}
          </pre>
        </div>
      )}
      {verification.mappingError && (
        <p className="text-2xs text-destructive">映射失败：{verification.mappingError}</p>
      )}
    </div>
  )
}

export function AssistantDialog({
  open,
  onOpenChange,
  sources,
  models,
  protocolType,
  currentConfig,
  onApplyDraft,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  sources: ModelSource[]
  models: Model[]
  protocolType: string
  currentConfig: CustomProtocolConfig | null
  onApplyDraft: (config: CustomProtocolConfig) => void
}) {
  const { error: toastError } = useToast()
  const [sourceId, setSourceId] = useState('')
  const [modelName, setModelName] = useState('')
  const [documents, setDocuments] = useState<CustomProtocolAssistDocument[]>([])
  const [message, setMessage] = useState('')
  const [turns, setTurns] = useState<AssistTurn[]>([])
  const [loading, setLoading] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  // 从当前草稿提取示例响应，供服务端离线验证响应映射。
  const exampleResponse = useMemo(() => {
    const sample = currentConfig?.response?.sample
    if (sample === undefined || sample === null) return undefined
    if (typeof sample === 'string') {
      try {
        return JSON.parse(sample)
      } catch {
        return undefined
      }
    }
    return sample
  }, [currentConfig])

  useEffect(() => {
    if (!open) return
    const saved = localStorage.getItem(STORAGE_KEY)
    if (!saved) return
    try {
      const { sourceId: s, model: m } = JSON.parse(saved) as { sourceId: string; model: string }
      if (s && m) {
        setSourceId(s)
        setModelName(m)
      }
    } catch {
      localStorage.removeItem(STORAGE_KEY)
    }
  }, [open])
  useEffect(() => {
    if (sourceId && modelName) localStorage.setItem(STORAGE_KEY, JSON.stringify({ sourceId, model: modelName }))
  }, [sourceId, modelName])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [turns, loading])

  const llmModels = useMemo(
    () => models.filter((model) => model.sourceId === sourceId && model.enabled && (model.type === 'llm' || !model.type)),
    [models, sourceId],
  )

  // 读取计数:文件尚在读取(file.text() 未完成)时禁用发送——否则材料
  // 既没参与本次生成,setDocuments([]) 清空后读取完成又把文件追加回来。
  const [readingFiles, setReadingFiles] = useState(0)
  const addFiles = async (files: FileList | File[]) => {
    const incoming = Array.from(files).slice(0, 20 - documents.length)
    if (incoming.length < Array.from(files).length) {
      toastError('附件数量超限', '单次最多 20 个材料')
    }
    setReadingFiles((n) => n + incoming.length)
    try {
      // allSettled:单个文件读失败不拖垮整批。
      const settled = await Promise.allSettled(incoming.map(fileToDocument))
      const docs = settled.flatMap((item) => (item.status === 'fulfilled' ? [item.value] : []))
      if (docs.length) setDocuments((current) => [...current, ...docs])
      const failed = settled.length - docs.length
      if (failed > 0) toastError('部分文件读取失败', `${failed} 个文件无法读取,已跳过`)
    } finally {
      setReadingFiles((n) => Math.max(0, n - incoming.length))
    }
  }

  const send = async () => {
    if (readingFiles > 0) {
      toastError('材料仍在读取', '请等待文件读取完成后再生成')
      return
    }
    if (!sourceId || !modelName) {
      toastError('请先选择助手模型', '建议选择支持文档/视觉输入的模型')
      return
    }
    if (!message.trim() && documents.length === 0) {
      toastError('输入为空', '请提供材料或填写说明')
      return
    }
    const outgoingDocuments = documents
    const outgoingMessage = message
    setDocuments([])
    setMessage('')
    setTurns((current) => [
      ...current,
      {
        role: 'user',
        text: outgoingMessage || `（提交 ${outgoingDocuments.length} 份材料）`,
        documentNames: outgoingDocuments.map((doc) => doc.name),
      },
    ])
    setLoading(true)
    try {
      const result: CustomProtocolAssistResult = await api.assistCustomProtocol({
        sourceId,
        model: modelName,
        protocolType: protocolType || 'llm',
        message: outgoingMessage || '请根据以上材料设计协议配置。',
        documents: outgoingDocuments,
        ...(currentConfig?.id ? { currentConfig } : {}),
        ...(exampleResponse !== undefined ? { exampleResponse } : {}),
      })
      setTurns((current) => [
        ...current,
        {
          role: 'assistant',
          text: result.reply,
          draft: result.config,
          valid: result.valid,
          issues: result.issues,
          rounds: result.rounds,
          verification: result.verification,
        },
      ])
    } catch (error) {
      setTurns((current) => [
        ...current,
        { role: 'assistant', text: `调用失败：${error instanceof ApiError ? error.message : String(error)}` },
      ])
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[84vh] w-full max-w-2xl flex-col overflow-hidden">
        <DialogHeader>
          <DialogTitle>AI 协议助手</DialogTitle>
          <DialogDescription>上传 API 文档 / 截图 / 示例响应，生成字段级映射配置草稿。</DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <Select value={sourceId} onValueChange={(value) => { setSourceId(value); setModelName('') }}>
              <SelectTrigger className="h-8 w-40 text-xs" aria-label="助手模型源">
                <SelectValue placeholder="选择模型源" />
              </SelectTrigger>
              <SelectContent>
                {sources.map((source) => (
                  <SelectItem key={source.id} value={source.id} className="text-xs">
                    {source.name || source.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={modelName} onValueChange={setModelName}>
              <SelectTrigger className="h-8 min-w-36 flex-1 text-xs" aria-label="助手模型">
                <SelectValue placeholder="选择模型" />
              </SelectTrigger>
              <SelectContent>
                {llmModels.map((model) => (
                  <SelectItem key={model.id} value={model.name || model.id} className="text-xs">
                    {model.name || model.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div ref={scrollRef} className="min-h-48 flex-1 space-y-3 overflow-y-auto rounded-md border border-border bg-card/50 p-3">
            {turns.length === 0 && (
              <div className="flex h-full flex-col items-center justify-center gap-2 p-4 text-center text-xs text-muted-foreground">
                <Sparkles className="h-5 w-5 text-primary/60" />
                <p>把 API 文档、示例请求/响应或截图拖入下方，补充一句说明即可生成协议草稿。</p>
                {exampleResponse !== undefined && <p>已携带当前草稿的示例响应用于离线映射验证。</p>}
              </div>
            )}
            {turns.map((turn, index) => (
              <div
                key={index}
                className={cn(
                  'rounded-md p-2.5 text-xs leading-relaxed',
                  turn.role === 'user' ? 'bg-wash ml-6' : 'mr-2 border border-border/60 bg-card',
                )}
              >
                <div className="mb-1 flex flex-wrap items-center gap-1.5 text-2xs text-muted-foreground">
                  {turn.role === 'user' ? '你' : '助手'}
                  {turn.rounds !== undefined && turn.rounds > 1 && <span>· {turn.rounds} 轮</span>}
                  {turn.documentNames?.map((name) => (
                    <span key={name} className="inline-flex items-center gap-0.5 rounded bg-background/70 px-1 py-px font-mono">
                      {/\.(png|jpe?g|gif|webp)$/i.test(name) ? <ImageIcon className="h-2.5 w-2.5" /> : <FileText className="h-2.5 w-2.5" />}
                      {name}
                    </span>
                  ))}
                </div>
                <p className="whitespace-pre-wrap break-words">{turn.text}</p>
                {turn.role === 'assistant' && turn.draft && (
                  <div className="mt-2 space-y-2 rounded-md border border-border bg-background/70 p-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={turn.valid ? 'success' : 'destructive'}>
                        {turn.valid ? '校验通过' : '校验未通过'}
                      </Badge>
                      <span className="font-mono text-2xs text-muted-foreground">id: {turn.draft.id}</span>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        className="ml-auto h-6 text-2xs"
                        onClick={() => onApplyDraft(turn.draft!)}
                      >
                        <Plus className="mr-1 h-3 w-3" /> 应用到编辑器
                      </Button>
                    </div>
                    {turn.issues && <p className="text-2xs text-destructive">{turn.issues}</p>}
                    {turn.verification && <VerificationBlock verification={turn.verification} />}
                    <details>
                      <summary className="cursor-pointer text-2xs text-muted-foreground">配置 JSON</summary>
                      <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all font-mono text-2xs text-muted-foreground">
                        {JSON.stringify(turn.draft, null, 2)}
                      </pre>
                    </details>
                  </div>
                )}
              </div>
            ))}
            {loading && (
              <div className="flex items-center gap-2 rounded-md border border-border/60 bg-card p-2.5 text-xs text-muted-foreground">
                <Sparkles className="h-3.5 w-3.5 animate-pulse text-primary" />
                正在生成并校验草稿…（含自动修复，大文档可能需要一两分钟）
              </div>
            )}
          </div>

          {documents.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {documents.map((doc, index) => (
                <span
                  key={`${doc.name}:${index}`}
                  className="inline-flex items-center gap-1 rounded-full border border-border bg-card px-2 py-0.5 font-mono text-2xs"
                >
                  {doc.dataUrl && !doc.text ? <ImageIcon className="h-3 w-3" /> : <FileText className="h-3 w-3" />}
                  {doc.name}
                  <button
                    type="button"
                    aria-label={`移除 ${doc.name}`}
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => setDocuments((current) => current.filter((_, i) => i !== index))}
                  >
                    <X className="h-3 w-3" />
                  </button>
                </span>
              ))}
              <button
                type="button"
                className="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-2xs text-muted-foreground hover:text-foreground"
                onClick={() => setDocuments([])}
              >
                <Trash2 className="h-3 w-3" /> 清空
              </button>
            </div>
          )}

          <div
            className={cn('rounded-md border border-dashed p-2 transition-colors', dragOver ? 'border-rose bg-wash' : 'border-border')}
            onDragOver={(event) => {
              event.preventDefault()
              setDragOver(true)
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={(event) => {
              event.preventDefault()
              setDragOver(false)
              void addFiles(event.dataTransfer.files)
            }}
          >
            <Textarea
              className="min-h-[64px] border-0 p-1 text-xs focus-visible:border-none focus-visible:ring-0"
              placeholder="补充说明 / 迭代要求（或直接拖入文档发送）"
              value={message}
              onChange={(event) => setMessage(event.target.value)}
              onKeyDown={(event) => {
                if (loading || readingFiles > 0) return
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
                  event.preventDefault()
                  void send()
                }
              }}
            />
            <div className="flex items-center justify-between pt-1">
              <div className="flex items-center gap-1.5">
                <Button type="button" variant="ghost" size="iconSm" aria-label="添加材料" onClick={() => fileInputRef.current?.click()}>
                  <Paperclip className="h-3.5 w-3.5" />
                </Button>
                <span className="text-2xs text-muted-foreground">拖拽或点击上传 · Ctrl+Enter 发送</span>
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  className="hidden"
                  onChange={async (event) => {
                    if (event.target.files?.length) await addFiles(event.target.files)
                    event.target.value = ''
                  }}
                />
              </div>
              <Button type="button" size="sm" disabled={loading || readingFiles > 0} onClick={() => void send()}>
                <Send className="mr-1 h-3 w-3" /> 生成
              </Button>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
