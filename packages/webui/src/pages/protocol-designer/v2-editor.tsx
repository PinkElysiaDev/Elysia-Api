import { useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  FileCheck2,
  Play,
  RotateCcw,
  Save,
  ShieldCheck,
  X,
  Zap,
} from 'lucide-react'
import { ProtocolProbe } from './v2-probe'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input, Textarea } from '@/components/ui/input'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ApiError } from '@/lib/api'
import { ProtocolDocument } from '@/lib/protocol-document'
import {
  protocolAPI,
  type ConversionIssue,
  type ProtocolDraft,
  type ProtocolRevision,
  type ProtocolSchema,
  type VerificationReport,
} from '@/lib/protocol-v2'
import { cn } from '@/lib/utils'
import { ProtocolDirectionEditor, ProtocolOperationEditor } from './v2-operation-editor'

const sectionNames = [
  ['directions', '方向与映射', '方向映射'],
  ['operations', '传输与操作', '传输操作'],
  ['samples', '请求、响应与事件样例', '消息样例'],
  ['agent', '原生 Agent 参数、工具结果与样例', 'Agent 规则'],
  ['sessionSamples', '实时会话样例', '会话样例'],
  ['taskSamples', '异步任务样例', '任务样例'],
  ['modelSamples', '模型目录样例', '目录样例'],
  ['native', '原生字段规则', '原生字段'],
  ['extensions', '扩展元数据', '扩展元数据'],
] as const

const EMPTY_DEFINITION = JSON.stringify(
  {
    schemaVersion: 2,
    id: '',
    name: '',
    version: '1',
    family: '',
    wireVersion: '1',
    capabilities: {},
    directions: {},
    operations: {},
    native: { preserve: false },
    samples: [],
  },
  null,
  2,
)

/** 编辑器内原生 select 的统一样式（Input 体系不覆盖 select）。 */
const selectClass =
  'h-[34px] rounded-md border border-input bg-card px-2.5 text-sm text-foreground transition-[border-color,box-shadow] duration-200 focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash disabled:cursor-not-allowed disabled:opacity-50'

/** Complete v2 editor. Raw JSON is the only editable source of truth. */
export function ProtocolV2Editor({
  schema,
  draft,
  activeHash,
  initialSource,
  onSaved,
  onClose,
}: {
  schema: ProtocolSchema
  draft?: ProtocolDraft
  activeHash: string
  initialSource?: string
  onSaved: (id: string) => Promise<void>
  onClose: () => void
}) {
  const [source, setSource] = useState(initialSource ?? draft?.definition ?? EMPTY_DEFINITION)
  // 起点与 source 对齐：全新模板/复制预置进入时不算「有未保存修改」，
  // 只有真正动过内容才需要关闭确认；尚未保存过草稿由 draftHash 单独表达。
  const [savedSource, setSavedSource] = useState(
    initialSource ?? draft?.definition ?? EMPTY_DEFINITION,
  )
  const [draftHash, setDraftHash] = useState(draft?.hash ?? '')
  const [tab, setTab] = useState('basic')
  const [isBusy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [issues, setIssues] = useState<ConversionIssue[]>([])
  const [report, setReport] = useState<VerificationReport>()
  const [verifiedSource, setVerifiedSource] = useState('')
  const [revisions, setRevisions] = useState<ProtocolRevision[]>([])
  const [selectedRevision, setSelectedRevision] = useState('')
  const [direction, setDirection] = useState('decode_request')
  const [input, setInput] = useState('{}')
  const [isSequence, setSequence] = useState(false)
  const [previewMode, setPreviewMode] = useState('mapping')
  const [workflowSample, setWorkflowSample] = useState('')
  const [taskOperation, setTaskOperation] = useState('')
  const [taskKind, setTaskKind] = useState('decode')
  const [taskPurpose, setTaskPurpose] = useState('status')
  const [output, setOutput] = useState<unknown>()
  const [target, setTarget] = useState('{}')
  const [focusPath, setFocusPath] = useState('')
  const [pendingSections, setPendingSections] = useState<
    Record<string, { source: string; error: string }>
  >({})
  const jsonRef = useRef<HTMLTextAreaElement>(null)

  const parsed = useMemo(() => {
    try {
      return { document: new ProtocolDocument(source), error: '' }
    } catch (failure) {
      return { document: undefined, error: String(failure) }
    }
  }, [source])

  const document = parsed.document
  const scalar = (path: string): string => {
    const raw = document?.read(path)
    const value: unknown = raw ? JSON.parse(raw) : ''
    return typeof value === 'string' ? value : ''
  }
  const id = scalar('/id')
  const protocolName = scalar('/name')
  const hasPendingSections = Object.keys(pendingSections).length > 0
  const isDirty = source !== savedSource || hasPendingSections
  const [confirmingClose, setConfirmingClose] = useState(false)
  const canActivate =
    !isDirty &&
    source === verifiedSource &&
    report?.passed &&
    report.compilerVersion === schema.compilerVersion

  const change = (path: string, value: string) => {
    if (document) {
      setSource(document.set(path, value))
      setError('')
    }
  }

  const editSection = (key: string, value: string) => {
    try {
      new ProtocolDocument(value)
      if (!document) throw new Error('请先修正完整 JSON')
      change('/' + key, value)
      setPendingSections((current) => {
        const next = { ...current }
        delete next[key]
        return next
      })
    } catch (failure) {
      setPendingSections((current) => ({
        ...current,
        [key]: { source: value, error: String(failure) },
      }))
    }
  }

  const run = async (work: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await work()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
      if (failure instanceof ApiError && failure.details && typeof failure.details === 'object' && 'issues' in failure.details && Array.isArray(failure.details.issues)) {
        setIssues(failure.details.issues)
      }
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    if (!document || !id || hasPendingSections) {
      throw new Error('请填写协议 ID，并修正所有 JSON 片段')
    }
    const result = await protocolAPI.save(id, source, draftHash)
    setDraftHash(result.draft.hash)
    setSavedSource(source)
    setIssues(result.issues ?? [])
    await onSaved(id)
    setRevisions(await protocolAPI.revisions(id))
  }

  const verify = async () => {
    if (isDirty || !draftHash) throw new Error('请先保存当前草稿，再验证这个版本')
    const result = await protocolAPI.verify(id, draftHash)
    setReport(result.report)
    setVerifiedSource(source)
    setIssues(result.report.issues ?? [])
    setRevisions(await protocolAPI.revisions(id))
  }

  const locate = (issue: ConversionIssue) => {
    let path = issue.path
    if (issue.evidence) {
      for (const section of ['samples', 'sessionSamples', 'taskSamples', 'modelSamples']) {
        const entries = document?.locate(`/${section}`)?.children
        for (const [index] of entries ?? []) {
          if (scalar(`/${section}/${index}/id`) === issue.evidence) {
            path = `/${section}/${index}`
          }
        }
      }
    }
    setFocusPath(path)
    setTab('json')
  }

  useEffect(() => {
    if (tab !== 'json' || !focusPath) return
    const currentDoc = parsed.document
    const location = currentDoc?.locate(focusPath)
    if (location && jsonRef.current) {
      jsonRef.current.focus()
      jsonRef.current.setSelectionRange(location.start, location.end)
    }
  }, [tab, focusPath, parsed.document])

  useEffect(() => {
    if (!draft) return
    let isCurrent = true
    void protocolAPI
      .revisions(draft.protocolId)
      .then((items) => {
        if (isCurrent) setRevisions(items)
      })
      .catch((failure: unknown) => {
        if (isCurrent) setError(String(failure))
      })
    return () => {
      isCurrent = false
    }
  }, [draft])

  const capabilities = document?.locate('/capabilities')
  const mappings = document?.locate('/directions')
  const directionKeys = [...(mappings?.children.keys() ?? [])]
  const declaredCapabilities = schema.capabilities.filter(
    (capability) => document?.read(`/capabilities/${capability}`) === 'true',
  )

  return (
    <section className="space-y-6 pt-1" aria-label="协议版本编辑器">
      {/* 顶部工作台标题与动作簇 */}
      <div className="flex flex-wrap items-start justify-between gap-4 border-b border-border/50 pb-4">
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-2.5">
            <h2 className="font-display text-lg font-semibold tracking-tight text-foreground">
              {id || '新协议'}
            </h2>
            {protocolName && protocolName !== id && (
              <span className="text-sm text-muted-foreground">· {protocolName}</span>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            {!draftHash ? '尚未保存为草稿' : isDirty ? '草稿尚未保存' : '草稿已保存'} ·{' '}
            <span className="font-mono">
              {activeHash ? `启用版本 ${activeHash.slice(0, 12)}` : '尚未启用'}
            </span>
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button disabled={isBusy || hasPendingSections} onClick={() => void run(save)}>
            <Save className="h-3.5 w-3.5" aria-hidden />
            保存草稿
          </Button>
          <Button
            disabled={isBusy || !document || hasPendingSections}
            onClick={() =>
              void run(async () => {
                const result = await protocolAPI.validate(source)
                setIssues(result.issues ?? [])
                setOutput(result)
              })
            }
          >
            <FileCheck2 className="h-3.5 w-3.5" aria-hidden />
            检查定义
          </Button>
          <Button disabled={isBusy || isDirty || !draftHash} onClick={() => void run(verify)}>
            <ShieldCheck className="h-3.5 w-3.5" aria-hidden />
            离线验证
          </Button>
          <Button
            variant="primary"
            disabled={isBusy || !canActivate}
            onClick={() =>
              void run(async () => {
                if (report) {
                  await protocolAPI.activate(id, report.definitionHash, activeHash)
                  await onSaved(id)
                }
              })
            }
          >
            <Zap className="h-3.5 w-3.5" aria-hidden />
            启用版本
          </Button>
          <span aria-hidden className="mx-0.5 h-5 w-px bg-border" />
          <Button
            variant="ghost"
            disabled={isBusy}
            onClick={() => {
              if (isDirty) setConfirmingClose(true)
              else onClose()
            }}
          >
            <X className="h-3.5 w-3.5" aria-hidden />
            关闭编辑器
          </Button>
        </div>
      </div>

      <Dialog open={confirmingClose} onOpenChange={setConfirmingClose}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>关闭编辑器</DialogTitle>
            <DialogDescription>当前草稿尚未保存，关闭后将丢失未保存的修改。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirmingClose(false)}>
              继续编辑
            </Button>
            <Button variant="destructive" onClick={onClose}>
              放弃修改并关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {(error || parsed.error) && (
        <div
          role="alert"
          className="flex items-start gap-2.5 rounded-lg border border-destructive/30 bg-destructive/5 px-3.5 py-2.5 text-sm text-destructive"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <span>{error || parsed.error}</span>
        </div>
      )}

      {hasPendingSections && (
        <div
          role="alert"
          className="flex items-start gap-2.5 rounded-lg border border-destructive/30 bg-destructive/5 px-3.5 py-2.5 text-sm text-destructive"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <span>以下片段尚有语法错误，请修正后保存：{Object.keys(pendingSections).join('、')}</span>
        </div>
      )}

      {/* 分段导航条 */}
      <div
        role="tablist"
        aria-label="协议编辑选项"
        className="no-scrollbar -mx-1 flex gap-1.5 overflow-x-auto border-b border-border/40 px-1 pb-2.5"
      >
        {(
          [
            ['basic', '基本信息', '基本信息'],
            ...sectionNames,
            ['preview', '转换预览', '转换预览'],
            ['verification', '能力与验证', '能力验证'],
            ['revisions', '版本与回滚', '版本回滚'],
            ['json', '完整 JSON', '完整 JSON'],
          ] as const
        ).map(([value, fullLabel, shortLabel]) => (
          <Button
            key={value}
            role="tab"
            size="sm"
            aria-label={fullLabel}
            aria-selected={tab === value}
            variant={tab === value ? 'default' : 'ghost'}
            className="shrink-0"
            onClick={() => setTab(value)}
          >
            {shortLabel}
          </Button>
        ))}
      </div>

      <fieldset disabled={isBusy} className="min-w-0 space-y-5 pt-1">
        {tab === 'directions' && document && !hasPendingSections && (
          <ProtocolDirectionEditor document={document} schema={schema} onChange={setSource} />
        )}
        {tab === 'operations' && document && !hasPendingSections && (
          <ProtocolOperationEditor document={document} schema={schema} onChange={setSource} />
        )}
        {tab === 'basic' && (
          <div className="space-y-5">
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {[
                ['id', '协议 ID'],
                ['name', '名称'],
                ['version', '定义版本'],
                ['family', '协议族'],
                ['wireVersion', '线格式版本'],
              ].map(([key, label]) => (
                <label key={key} className="space-y-1.5 text-xs font-medium text-foreground">
                  <span>{label}</span>
                  <Input
                    className={cn(key !== 'name' && 'font-mono text-xs')}
                    value={scalar('/' + key)}
                    disabled={!document || (key === 'id' && !!draftHash)}
                    onChange={(event) => change('/' + key, JSON.stringify(event.target.value))}
                  />
                </label>
              ))}
            </div>

            <fieldset className="space-y-2.5 border-t border-border/40 pt-4">
              <legend className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                声明能力
              </legend>
              <div className="flex flex-wrap gap-2">
                {schema.capabilities.map((capability) => {
                  const checked = document?.read(`/capabilities/${capability}`) === 'true'
                  return (
                    <label
                      key={capability}
                      className={cn(
                        'inline-flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-1.5 font-mono text-xs transition-colors',
                        checked
                          ? 'border-primary/40 bg-wash font-medium text-foreground'
                          : 'border-border/70 bg-card text-muted-foreground hover:border-border hover:text-foreground',
                      )}
                    >
                      <input
                        type="checkbox"
                        className="accent-primary"
                        disabled={!capabilities}
                        checked={checked}
                        onChange={(event) =>
                          change(`/capabilities/${capability}`, String(event.target.checked))
                        }
                      />
                      {capability}
                    </label>
                  )
                })}
              </div>
              <p className="pt-1 text-xs text-muted-foreground">
                每个方向的映射与样例必须证明所声明的能力。新增能力后重新验证才能启用。
              </p>
            </fieldset>
          </div>
        )}

        {sectionNames.map(
          ([key, label]) =>
            tab === key && (
              <div key={key} className="space-y-2">
                <label className="block space-y-1.5 text-xs font-medium text-foreground">
                  <span>{label} JSON</span>
                  <Textarea
                    aria-label={`${label} JSON`}
                    rows={16}
                    className="font-mono text-xs leading-relaxed"
                    value={
                      pendingSections[key]?.source ??
                      document?.read('/' + key) ??
                      (key.includes('Samples') || key === 'samples' ? '[]' : '{}')
                    }
                    onChange={(event) => editSection(key, event.target.value)}
                  />
                </label>
                {pendingSections[key] && (
                  <p className="text-xs text-destructive">{pendingSections[key].error}</p>
                )}
              </div>
            ),
        )}

        {tab === 'directions' && (
          <details className="border-t border-border/40 pt-3.5 text-xs">
            <summary className="cursor-pointer font-medium text-foreground">
              引擎支持的方向与映射操作
            </summary>
            <div className="mt-3 space-y-2 text-muted-foreground">
              <p className="font-mono">{schema.directions.join(' · ')}</p>
              <ul className="space-y-1">
                {schema.mappingOperations.map((operation) => (
                  <li key={operation.name}>
                    <code className="font-mono text-foreground">{operation.name}</code>：
                    {operation.description}
                  </li>
                ))}
              </ul>
              <p>
                可复用模块：
                {schema.modules
                  .map((module) => `${module.name} (${module.directions.join(', ')})`)
                  .join('；')}
              </p>
            </div>
          </details>
        )}

        {tab === 'operations' && (
          <p className="text-xs text-muted-foreground">
            操作定义包含 method、path、transport、auth，以及可选的 session 或 task。支持 HTTP
            JSON、SSE、NDJSON、WebSocket；任务流声明 submit/status/result/cancel。未安装的引擎能力会在检查定义时报告。
          </p>
        )}

        {tab === 'json' && (
          <div className="space-y-2">
            <label className="block space-y-1.5 text-xs font-medium text-foreground">
              <span>完整协议 JSON</span>
              <Textarea
                ref={jsonRef}
                aria-label="完整协议 JSON"
                rows={22}
                className="font-mono text-xs leading-relaxed"
                value={source}
                onChange={(event) => setSource(event.target.value)}
              />
            </label>
            {focusPath && (
              <p className="font-mono text-xs text-muted-foreground">定位：{focusPath}</p>
            )}
          </div>
        )}

        {tab === 'preview' && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-4 border-b border-border/40 pb-4">
              <label className="space-y-1 text-xs font-medium text-foreground">
                <span className="block">预览类型</span>
                <select
                  aria-label="预览类型"
                  className={selectClass}
                  value={previewMode}
                  onChange={(event) => setPreviewMode(event.target.value)}
                >
                  <option value="mapping">请求 / 响应 / 事件</option>
                  <option value="session">完整双向会话</option>
                  <option value="task">任务操作</option>
                  <option value="agent">Agent settings</option>
                  <option value="models">模型目录</option>
                </select>
              </label>
              <label className="space-y-1 text-xs font-medium text-foreground">
                <span className="block">转换方向</span>
                <select
                  aria-label="转换方向"
                  className={selectClass}
                  value={direction}
                  onChange={(event) => setDirection(event.target.value)}
                >
                  {schema.directions.map((value) => (
                    <option key={value}>{value}</option>
                  ))}
                </select>
              </label>
              <label className="inline-flex h-[34px] cursor-pointer items-center gap-2 text-xs font-medium text-foreground">
                <input
                  type="checkbox"
                  className="accent-primary"
                  checked={isSequence}
                  onChange={(event) => setSequence(event.target.checked)}
                />
                事件序列
              </label>
            </div>

            {previewMode === 'models' && (
              <label className="block space-y-1.5 text-xs font-medium text-foreground">
                <span>发现操作名</span>
                <Input
                  value={taskOperation}
                  onChange={(event) => setTaskOperation(event.target.value)}
                />
              </label>
            )}
            {previewMode === 'session' && (
              <label className="block space-y-1.5 text-xs font-medium text-foreground">
                <span>会话样例 ID</span>
                <Input
                  value={workflowSample}
                  onChange={(event) => setWorkflowSample(event.target.value)}
                />
              </label>
            )}
            {previewMode === 'task' && (
              <div className="grid gap-3 md:grid-cols-3">
                <label className="space-y-1.5 text-xs font-medium text-foreground">
                  <span>提交操作名</span>
                  <Input
                    value={taskOperation}
                    onChange={(event) => setTaskOperation(event.target.value)}
                  />
                </label>
                <label className="space-y-1.5 text-xs font-medium text-foreground">
                  <span className="block">任务映射</span>
                  <select
                    className={cn(selectClass, 'w-full')}
                    value={taskKind}
                    onChange={(event) => setTaskKind(event.target.value)}
                  >
                    {['decode', 'encode', 'control'].map((kind) => (
                      <option key={kind}>{kind}</option>
                    ))}
                  </select>
                </label>
                <label className="space-y-1.5 text-xs font-medium text-foreground">
                  <span className="block">任务阶段</span>
                  <select
                    className={cn(selectClass, 'w-full')}
                    value={taskPurpose}
                    onChange={(event) => setTaskPurpose(event.target.value)}
                  >
                    {['submit', 'status', 'result', 'cancel'].map((purpose) => (
                      <option key={purpose}>{purpose}</option>
                    ))}
                  </select>
                </label>
              </div>
            )}

            <div className="grid gap-4 lg:grid-cols-2">
              <div className="space-y-2.5">
                <label className="block space-y-1.5 text-xs font-medium text-foreground">
                  <span>输入 JSON</span>
                  <Textarea
                    aria-label="预览输入 JSON"
                    rows={7}
                    className="font-mono text-xs"
                    value={input}
                    onChange={(event) => setInput(event.target.value)}
                  />
                </label>
                <Button
                  disabled={hasPendingSections}
                  onClick={() =>
                    void run(async () => {
                      new ProtocolDocument(input)
                      const result =
                        previewMode === 'mapping'
                          ? await protocolAPI.preview(source, direction, input, isSequence)
                          : await protocolAPI.workflow(
                              source,
                              {
                                mode: previewMode,
                                sample: workflowSample,
                                operation: taskOperation,
                                kind: taskKind,
                                purpose: taskPurpose,
                              },
                              input,
                            )
                      setOutput(result)
                      setIssues(result.issues)
                    })
                  }
                >
                  <Play className="h-3.5 w-3.5" aria-hidden />
                  运行转换预览
                </Button>
              </div>

              <div className="space-y-2.5">
                <label className="block space-y-1.5 text-xs font-medium text-foreground">
                  <span>目标协议完整 JSON</span>
                  <Textarea
                    aria-label="目标协议 JSON"
                    rows={7}
                    className="font-mono text-xs"
                    value={target}
                    onChange={(event) => setTarget(event.target.value)}
                  />
                </label>
                <Button
                  disabled={hasPendingSections}
                  onClick={() =>
                    void run(async () => {
                      new ProtocolDocument(target)
                      const result = await protocolAPI.combine(source, target)
                      setOutput(result)
                      setIssues(result.issues)
                    })
                  }
                >
                  <CheckCircle2 className="h-3.5 w-3.5" aria-hidden />
                  验证协议组合
                </Button>
              </div>
            </div>

            <pre
              aria-label="转换链路结果"
              className="max-h-96 overflow-auto rounded-lg border border-border/60 bg-[var(--code-bg)] p-3.5 font-mono text-xs leading-relaxed"
            >
              {output === undefined
                ? '预览将显示语义模型、输出与诊断。'
                : typeof output === 'string'
                  ? output
                  : JSON.stringify(output, null, 2)}
            </pre>
          </div>
        )}

        {tab === 'verification' && (
          <div className="space-y-4">
            <div
              role="status"
              className={cn(
                'inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium',
                report && verifiedSource === source
                  ? report.passed
                    ? 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_9%,transparent)] text-jade'
                    : 'border-destructive/30 bg-destructive/10 text-destructive'
                  : 'border-border/80 bg-secondary/40 text-muted-foreground',
              )}
            >
              离线验证：
              {report && verifiedSource === source
                ? report.passed
                  ? '通过'
                  : '未通过'
                : '当前草稿尚未验证'}
            </div>

            <ProtocolProbe
              source={source}
              id={id}
              hash={verifiedSource === source ? report?.definitionHash : undefined}
            />

            <p className="text-xs text-muted-foreground">
              离线报告绑定定义、样例和编译器版本；修改内容后需要重新验证。真实上游验证是独立证据。
            </p>

            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <TableHeader>
                  <TableRow className="border-b border-border/50 hover:bg-transparent">
                    <TableHead className="py-2.5 pl-3">能力</TableHead>
                    {directionKeys.map((name) => (
                      <TableHead key={name} className="py-2.5 text-center font-mono">
                        {name}
                      </TableHead>
                    ))}
                    <TableHead className="py-2.5 pr-3 text-right">样例证据</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody className="divide-y divide-border/30">
                  {declaredCapabilities.map((capability) => (
                    <TableRow key={capability} className="border-b-0">
                      <TableCell className="py-2.5 pl-3 font-mono font-medium">
                        {capability}
                      </TableCell>
                      {directionKeys.map((name) => (
                        <TableCell key={name} className="py-2.5 text-center text-muted-foreground">
                          {document?.locate(`/directions/${name}/capabilities`)
                            ? document.read(`/directions/${name}/capabilities/${capability}`) ===
                              'true'
                              ? '支持'
                              : '—'
                            : '继承声明'}
                        </TableCell>
                      ))}
                      <TableCell className="py-2.5 pr-3 text-right">
                        {verifiedSource === source && report?.covered?.includes(capability) ? (
                          <span className="font-medium text-jade">已验证</span>
                        ) : (
                          <span className="text-muted-foreground">待验证</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </table>
            </div>

            {(report?.checks?.length ?? 0) > 0 && (
              <ul className="flex flex-wrap gap-2 text-xs">
                {report?.checks?.map((check, index) => (
                  <li
                    key={index}
                    className={cn(
                      'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 font-mono',
                      check.passed
                        ? 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_8%,transparent)] text-jade'
                        : 'border-destructive/30 bg-destructive/10 text-destructive',
                    )}
                  >
                    {check.passed ? '✓' : '×'} {check.sampleId} {check.direction}
                  </li>
                ))}
              </ul>
            )}

            <details className="border-t border-border/40 pt-3.5 text-xs">
              <summary className="cursor-pointer font-medium text-foreground">当前引擎契约</summary>
              <pre className="mt-3 max-h-96 overflow-auto rounded-md bg-[var(--code-bg)] p-3 font-mono text-xs">
                {JSON.stringify(
                  {
                    engineFeatures: schema.engineFeatures,
                    events: schema.events,
                    definition: schema.definitionSchema,
                    semantic: schema.semanticSchema,
                  },
                  null,
                  2,
                )}
              </pre>
            </details>
          </div>
        )}

        {tab === 'revisions' && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-3 border-b border-border/40 pb-4">
              <label className="space-y-1.5 text-xs font-medium text-foreground">
                <span className="block">选择已保存修订</span>
                <select
                  aria-label="选择修订"
                  className={cn('min-w-[260px] max-w-full font-mono', selectClass)}
                  value={selectedRevision}
                  onChange={(event) => setSelectedRevision(event.target.value)}
                >
                  <option value="">请选择</option>
                  {revisions.map((revision) => (
                    <option key={revision.hash} value={revision.hash}>
                      {revision.hash.slice(0, 16)} · {revision.createdAt}
                    </option>
                  ))}
                </select>
              </label>
              <div className="flex flex-wrap gap-2">
                <Button
                  disabled={!selectedRevision || !activeHash}
                  onClick={() =>
                    void run(async () =>
                      setOutput(await protocolAPI.diff(id, activeHash, selectedRevision)),
                    )
                  }
                >
                  比较启用版本
                </Button>
                <Button
                  disabled={!selectedRevision || isDirty}
                  onClick={() => {
                    const revision = revisions.find((item) => item.hash === selectedRevision)
                    if (revision) {
                      setSource(revision.definition)
                      setTab('json')
                    }
                  }}
                >
                  载入为草稿
                </Button>
                <Button
                  disabled={!selectedRevision}
                  onClick={() =>
                    void run(async () => {
                      const result = await protocolAPI.verifyRevision(id, selectedRevision)
                      setIssues(result.issues)
                      setOutput(result)
                      if (!result.passed) return
                      await protocolAPI.activate(id, selectedRevision, activeHash, true)
                      await onSaved(id)
                    })
                  }
                >
                  <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                  验证并回滚至所选修订
                </Button>
              </div>
            </div>

            <pre className="max-h-96 overflow-auto rounded-lg border border-border/60 bg-[var(--code-bg)] p-3.5 font-mono text-xs">
              {output === undefined
                ? ''
                : typeof output === 'string'
                  ? output
                  : JSON.stringify(output, null, 2)}
            </pre>
          </div>
        )}
      </fieldset>

      {!!issues.length && (
        <div
          aria-label="协议诊断"
          className="space-y-2.5 border-t border-border/60 pt-4"
        >
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            诊断与定位（{issues.length}）
          </div>
          <div className="divide-y divide-border/30">
            {issues.map((issue, index) => (
              <div key={index} className="space-y-1 py-2.5 text-sm first:pt-0 last:pb-0">
                <button
                  type="button"
                  className="inline-flex items-center rounded border border-primary/30 bg-wash px-2 py-0.5 text-left font-mono text-xs font-medium text-primary underline-offset-2 hover:underline"
                  onClick={() => locate(issue)}
                >
                  {issue.code} · {issue.path || '/'}
                </button>
                <p className="text-xs font-medium text-foreground">{issue.reason}</p>
                {issue.suggestion && (
                  <p className="text-xs text-muted-foreground">{issue.suggestion}</p>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  )
}
