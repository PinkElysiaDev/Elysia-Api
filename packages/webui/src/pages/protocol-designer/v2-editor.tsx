import { useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertTriangle,
  FileCheck2,
  Save,
  ShieldCheck,
  X,
  Zap,
} from 'lucide-react'
import { ProtocolPreview } from './v2-preview'
import { ProtocolRevisions } from './v2-revisions'
import { ProtocolVerification } from './v2-verification'
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
import { ApiError } from '@/lib/api'
import { ProtocolDocument } from '@/lib/protocol-document'
import {
  protocolAPI,
  protocolIdentityPrefix,
  type ConversionIssue,
  type ProtocolDraft,
  type ProtocolRevision,
  type ProtocolSchema,
  type VerificationReport,
} from '@/lib/protocol-v2'
import { cn, downloadJSONSource } from '@/lib/utils'
import { ProtocolDirectionEditor, ProtocolOperationEditor } from './v2-operation-editor'

const legacySectionNames = [
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

const sourceSectionNames = [
  ['spec', '外部协议 spec.json', '协议定义'],
  ['mapping', '转换说明 mapping.json', '转换说明'],
] as const

const EMPTY_DEFINITION = JSON.stringify(
  {
    schemaVersion: 4,
    spec: {
      id: '',
      name: '',
      version: '1',
      family: '',
      wireVersion: '1',
      documents: {},
      operations: {},
    },
    mapping: {
      coreVersion: 2,
      dslVersion: 2,
      capabilities: {},
      directions: {},
      operations: {},
      native: { preserve: false },
      samples: [],
    },
  },
  null,
  2,
)

/** Edit spec and mapping as one revision; raw JSON remains the source of truth. */
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
  const [focusPath, setFocusPath] = useState('')
  const [pendingSections, setPendingSections] = useState<
    Record<string, { source: string; error: string }>
  >({})
  const jsonRef = useRef<HTMLTextAreaElement>(null)
  const sectionRefs = useRef<Record<string, HTMLTextAreaElement | null>>({})

  const parsed = useMemo(() => {
    try {
      return { document: new ProtocolDocument(source), error: '' }
    } catch (failure) {
      return { document: undefined, error: String(failure) }
    }
  }, [source])

  const document = parsed.document
  const identityPrefix = document ? protocolIdentityPrefix(document) : ''
  const isPackage = identityPrefix === '/spec'
  const mappingPrefix = isPackage ? '/mapping' : ''
  const sectionNames = isPackage ? sourceSectionNames : legacySectionNames
  const scalar = (path: string): string => {
    const raw = document?.read(path)
    const value: unknown = raw ? JSON.parse(raw) : ''
    return typeof value === 'string' ? value : ''
  }
  const id = scalar(`${identityPrefix}/id`)
  const protocolName = scalar(`${identityPrefix}/name`)
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
      setFocusPath('')
      setError('')
    }
  }

  const editSection = (key: string, value: string) => {
    setFocusPath('')
    try {
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

  const sectionSource = (key: string) =>
    pendingSections[key]?.source ?? document?.read('/' + key) ??
    (key.includes('Samples') || key === 'samples' ? '[]' : '{}')

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
      for (const section of ['samples', 'sessionSamples', 'taskSamples', 'modelSamples', 'tokenCountSamples']) {
        const entries = document?.locate(`${mappingPrefix}/${section}`)?.children
        for (const [index] of entries ?? []) {
          if (scalar(`${mappingPrefix}/${section}/${index}/id`) === issue.evidence) {
            path = `${mappingPrefix}/${section}/${index}`
          }
        }
      }
    }
    setFocusPath(path)
    const sourceTab = isPackage && sourceSectionNames.find(([key]) => path === `/${key}` || path.startsWith(`/${key}/`))?.[0]
    setTab(sourceTab || 'json')
  }

  useEffect(() => {
    if (!focusPath) return
    const currentDoc = parsed.document
    const location = currentDoc?.locate(focusPath)
    const editor = tab === 'json' ? jsonRef.current : sectionRefs.current[tab]
    const offset = tab === 'json' ? 0 : currentDoc?.locate(`/${tab}`)?.start
    if (location && editor && offset !== undefined) {
      editor.focus()
      editor.setSelectionRange(location.start - offset, location.end - offset)
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

  const capabilities = document?.locate(`${mappingPrefix}/capabilities`)

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
                    value={scalar(`${identityPrefix}/${key}`)}
                    disabled={!document || (key === 'id' && !!draftHash)}
                    onChange={(event) => change(`${identityPrefix}/${key}`, JSON.stringify(event.target.value))}
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
                  const checked = document?.read(`${mappingPrefix}/capabilities/${capability}`) === 'true'
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
                          change(`${mappingPrefix}/capabilities/${capability}`, String(event.target.checked))
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
                {isPackage && (
                  <div className="flex flex-wrap items-end gap-3">
                    <label className="space-y-1 text-xs font-medium text-foreground">
                      <span className="block">导入 {key}.json</span>
                      <Input type="file" accept=".json,application/json" aria-label={`导入 ${key}.json`}
                        onChange={(event) => {
                          const file = event.target.files?.[0]
                          event.target.value = ''
                          if (file) void run(async () => editSection(key, await file.text()))
                        }} />
                    </label>
                    <Button onClick={() => downloadJSONSource(`${key}.json`, sectionSource(key))}>
                      导出 {key}.json
                    </Button>
                    <p className="text-xs text-muted-foreground">两份源一并保存、验证和启用。</p>
                  </div>
                )}
                {isPackage && key === 'mapping' && (
                  <p className="text-xs text-muted-foreground">
                    conversionDefaults 使用 use: request_projection 或 model_policy 绑定已声明程序；ID、顺序和启用状态由本规则指定。
                    metadata 处置由配置声明字段与形状；enum 的 otherwise 只处理未映射的字符串。
                    each.select: error 可读取当前错误并更新 message、code、param、details、providerType；分类和终态不可写。
                    aggregate.layout 通过 remainder、fields、labels 声明请求对象拆分；核心路径、成员类型和无覆盖重建在编译时检查。
                    steps.layout 使用 stable_partition、combine_children 或 group_runs 处理请求或完整响应的 /content；group_runs 通过 role 声明相邻根的消息角色，保留叶子及顺序，并明确诊断容器边界变化。
                    unfold_children 通过 childWhen 展开选中子节点，保留其余内容的消息角色和顺序。
                    merge_text 处理 /content/*/children；text.measure 声明长度口径（如 utf16_length），offsetBinding、lengthBinding 为 metadata 表达式提供原文偏移，across 显式允许跨越未选中节点。
                    track_position 按固定 name 和 item key 记录增量长度；project_range 将声明的 start/end 换算到输出区间，拒绝跨其他 item 的范围。长度单位与引用字段由 Mapping 指定。
                    conversionDefaults.streamDisposition 声明流内损失的 degrade／reject、path 和 reason；目标事件程序使用 require_disposition 的固定 name 引用规则，source 提供实际位置。严格模式、规则禁用或不匹配时拒绝，原生 observer 同样执行授权。
                    nativeWhen 可读取 runtime 的 /encoding/hasRendered，判断此前是否执行过语义编码，据此选择包含历史内容的帧是否重建；它不代表成功交付，不授予引用、资源或续传证明。
                    encode_event.semanticRanges 使用相同的文本／引用记录检查核心事件改写：仅可读取事件身份、delta、item 的 kind／payload 和 metadata。原值由宿主保留，配置不能提供改写证明；最终 wire 与实际交付检查仍分别执行。
                    models 操作通过 Spec 的 models.clientDocument 声明客户端目录合同，Mapping 的 encodeModels 将核心 models 数组编码成目标格式；decodeModels 继续负责上游发现。modelSamples.direction 使用 encode_response 验证客户端输出，省略时验证 decode_response。两方向的样例覆盖分别检查。
                    encode_response／encode_event.deliveryRanges 声明引用关联：name 为通道，measure 度量字符串，texts 返回 key／text／mode／path，ranges 返回 key／id／start／end／path。选择程序只读 wire；最终改写仅保留 Unicode 字符边界上相同的前后区间，引用索引必须匹配已证明位置。事件方向保留摘要和区间，支持晚到引用；只有成功 Flush 后提交，snapshot 不能改写已交付前缀。
                  </p>
                )}
                <label className="block space-y-1.5 text-xs font-medium text-foreground">
                  <span>{label} JSON</span>
                  <Textarea
                    ref={(element) => { sectionRefs.current[key] = element }}
                    aria-label={`${label} JSON`}
                    rows={16}
                    className="font-mono text-xs leading-relaxed"
                    value={sectionSource(key)}
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
                onChange={(event) => {
                  setFocusPath('')
                  setSource(event.target.value)
                }}
              />
            </label>
            {focusPath && (
              <p className="font-mono text-xs text-muted-foreground">定位：{focusPath}</p>
            )}
          </div>
        )}

        <ProtocolPreview source={source} schema={schema} isActive={tab === 'preview'}
          hasPendingSections={hasPendingSections} run={run} onIssues={setIssues} />

        {tab === 'verification' && (
          <ProtocolVerification source={source} id={id} schema={schema} document={document}
            mappingPrefix={mappingPrefix} report={report} verifiedSource={verifiedSource} />
        )}

        <ProtocolRevisions id={id} revisions={revisions} activeHash={activeHash}
          isActive={tab === 'revisions'} isDirty={isDirty} run={run} onIssues={setIssues}
          onLoad={(revision) => { setSource(revision); setTab('json') }} onSaved={onSaved} />
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
