import { useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { AlertTriangle, Copy, Pencil, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { EmptyState, LoadingState } from '@/components/ui/states'
import { Button } from '@/components/ui/button'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { protocolAPI, type EnabledProtocol, type ProtocolDraft, type ProtocolListing, type ProtocolSchema } from '@/lib/protocol-v2'
import { protocolLabel } from '@/lib/protocol'
import { ProtocolV2Editor } from './v2-editor'
import { ProtocolArchiveDialog } from './archive-dialog'
import { ProtocolDocument } from '@/lib/protocol-document'
import { useEnabledProtocols } from '@/lib/hooks'
import { cn, formatRelative } from '@/lib/utils'

type StatusTone = 'jade' | 'amber' | 'muted'

const STATUS_PILL_STYLE: Record<StatusTone, string> = {
  jade: 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_9%,transparent)] text-jade',
  amber: 'border-[color-mix(in_srgb,var(--amber)_28%,transparent)] bg-[color-mix(in_srgb,var(--amber)_9%,transparent)] text-amber',
  muted: 'border-border/80 bg-secondary/40 text-muted-foreground',
}

const PRESET_DISPLAY_NAMES: Record<string, string> = {
  'openai-chat-completions': 'OpenAI Chat Completions',
  'openai-responses': 'OpenAI Responses',
  'anthropic-messages': 'Anthropic Messages',
  'google-generate-content': 'Google Generate Content',
}
function extractDraftName(draft: ProtocolDraft, protocols: readonly EnabledProtocol[]): string {
  const fromEnabled = protocols.find((item) => item.id === draft.protocolId)?.name?.trim()
  if (fromEnabled) return fromEnabled
  try {
    const rawDef: unknown = draft.definition
    if (typeof rawDef === 'string') {
      const doc = new ProtocolDocument(rawDef)
      const raw = doc.read('/name')
      const parsed: unknown = raw ? JSON.parse(raw) : ''
      if (typeof parsed === 'string' && parsed.trim()) return parsed.trim()
    } else if (rawDef && typeof rawDef === 'object' && 'name' in rawDef) {
      const candidate = rawDef.name
      if (typeof candidate === 'string' && candidate.trim()) return candidate.trim()
    }
  } catch {
    // Ignore malformed draft definitions in list view.
  }
  return ''
}

/** Versioned definitions are saved as drafts and activated only with evidence. */
export function ProtocolDesignerPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const { data: protocols = [], mutate: refreshProtocols } = useEnabledProtocols()
  const [schema, setSchema] = useState<ProtocolSchema>()
  const [listing, setListing] = useState<ProtocolListing>()
  const [error, setError] = useState('')
  const [archiveID, setArchiveID] = useState('')
  const [selection, setSelection] = useState<{ draft?: ProtocolDraft; source?: string }>()

  const refresh = useCallback(
    async (id: string) => {
      const [items] = await Promise.all([protocolAPI.list(), refreshProtocols()])
      setListing(items)
      setSelection(
        (current) =>
          current && {
            ...current,
            draft: items.drafts.find((entry) => entry.protocolId === id),
          },
      )
    },
    [refreshProtocols],
  )

  /** 预置只读：复制为派生 ID 的可编辑草稿（名称加副本后缀）。 */
  const copyPreset = useCallback(
    async (id: string) => {
      const revisions = await protocolAPI.revisions(id)
      const latest = revisions.find(
        (revision) =>
          revision.hash === listing?.active.find((entry) => entry.protocolId === id)?.revisionHash,
      )
      if (!latest) return
      const document = new ProtocolDocument(latest.definition)
      const taken = new Set(listing?.drafts.map((draft) => draft.protocolId) ?? [])
      let candidate = `${id}-copy`
      for (let index = 1; taken.has(candidate); index += 1) candidate = `${id}-copy-${index}`
      const source = new ProtocolDocument(document.set('/id', JSON.stringify(candidate))).set(
        '/name',
        JSON.stringify(`${JSON.parse(document.read('/name') ?? JSON.stringify(id))}（副本）`),
      )
      setSelection({ source })
    },
    [listing],
  )

  useEffect(() => {
    let isCurrent = true
    void Promise.all([protocolAPI.schema(), protocolAPI.list()])
      .then(([contract, items]) => {
        if (isCurrent) {
          setSchema(contract)
          setListing(items)
        }
      })
      .catch((failure: unknown) => {
        if (isCurrent) setError(String(failure))
      })
    return () => {
      isCurrent = false
    }
  }, [])

  useEffect(() => {
    const rawState: unknown = location.state
    if (!rawState || typeof rawState !== 'object') return
    if ('definitionJSON' in rawState && typeof rawState.definitionJSON === 'string') {
      setSelection({ source: rawState.definitionJSON })
      navigate(location.pathname, { replace: true, state: null })
    } else if ('draft' in rawState && rawState.draft) {
      navigate('/protocols/legacy', { replace: true, state: rawState })
    }
  }, [location, navigate])

  useEffect(() => {
    const rawState: unknown = location.state
    if (!rawState || typeof rawState !== 'object' || !listing) return
    if ('protocolId' in rawState && typeof rawState.protocolId === 'string') {
      const targetId = rawState.protocolId
      const draft = listing.drafts.find((entry) => entry.protocolId === targetId)
      if (draft) setSelection({ draft })
      navigate(location.pathname, { replace: true, state: null })
    }
  }, [listing, location, navigate])
  const presets = listing?.presets ?? []
  const presetIDs = new Set(presets)
  const customDrafts = (listing?.drafts ?? []).filter((draft) => !presetIDs.has(draft.protocolId))

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="协议设计器"
          actions={
            !selection && (
              <>
                <Button variant="ghost" onClick={() => navigate('/protocols/conversions')}>
                  转换行为
                </Button>
                <Button variant="ghost" onClick={() => navigate('/protocols/history')}>
                  协议历史
                </Button>
                <span aria-hidden className="mx-0.5 h-4 w-px bg-border/80 max-rail:hidden" />
                <Button onClick={() => navigate('/agent?mode=create')}>Agent 编写</Button>
                <Button variant="primary" onClick={() => setSelection({})}>
                  新建协议
                </Button>
              </>
            )
          }
        />

        {archiveID && (
          <ProtocolArchiveDialog
            id={archiveID}
            onClose={() => setArchiveID('')}
            onArchived={() => refresh(archiveID)}
          />
        )}

        {error && (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
            <span>{error}</span>
          </div>
        )}

        {listing?.runtimeError && (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
            <div>
              <div>协议运行时尚未就绪：{listing.runtimeError}</div>
              {listing.startupFailure && (
                <span className="mt-1 block font-mono text-xs opacity-90">
                  失败阶段：{listing.startupFailure.stage} · {listing.startupFailure.code}
                </span>
              )}
            </div>
          </div>
        )}

        {!schema || !listing ? (
          <div className="space-y-3">
            <p role="status" className="text-xs text-muted-foreground">
              正在读取协议引擎契约…
            </p>
            <LoadingState rows={4} columns={4} />
          </div>
        ) : selection ? (
          <ProtocolV2Editor
            schema={schema}
            draft={selection.draft}
            initialSource={selection.source}
            activeHash={
              listing.active.find((entry) => entry.protocolId === selection.draft?.protocolId)
                ?.revisionHash ?? ''
            }
            onSaved={refresh}
            onClose={() => setSelection(undefined)}
          />
        ) : (
          <div className="space-y-10">
            {presets.length > 0 && (
              <section className="space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/50 pb-3">
                  <h2
                    aria-label="预置协议（只读）"
                    className="text-sm font-semibold tracking-tight text-foreground"
                  >
                    预置协议
                    <span className="ml-2 font-normal text-xs text-muted-foreground">只读</span>
                  </h2>
                  {listing.runtimeReady && (
                    <p role="status" className="text-xs text-muted-foreground">
                      协议运行时已就绪
                    </p>
                  )}
                </div>

                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <TableHeader>
                      <TableRow className="border-b border-border/50 hover:bg-transparent">
                        <TableHead className="py-3 pl-2">协议</TableHead>
                        <TableHead className="py-3 text-center">版本</TableHead>
                        <TableHead className="py-3 text-center">状态</TableHead>
                        <TableHead className="py-3 pr-2 text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody className="divide-y divide-border/30">
                      {presets.map((id) => {
                        const active = listing.active.find((entry) => entry.protocolId === id)
                        const created =
                          active || listing.drafts.some((draft) => draft.protocolId === id)
                        const isLoaded = Boolean(listing.loaded?.[id])
                        const state = isLoaded
                          ? '已加载'
                          : created
                            ? '已创建，等待验证或加载'
                            : '内置，尚未创建'
                        const tone = isLoaded ? 'jade' : created ? 'amber' : 'muted'
                        const displayName =
                          PRESET_DISPLAY_NAMES[id] ?? protocolLabel(id, 'long', protocols)
                        return (
                          <TableRow key={id} className="border-b-0">
                            <TableCell className="py-3.5 pl-2">
                              <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
                                <span className="font-medium text-foreground">{displayName}</span>
                                <span className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                                  {id}
                                </span>
                              </div>
                            </TableCell>
                            <TableCell className="py-3.5 whitespace-nowrap text-center font-mono text-xs text-muted-foreground">
                              {active?.revisionHash ? active.revisionHash.slice(0, 12) : '—'}
                            </TableCell>
                            <TableCell className="py-3.5 text-center">
                              <div className="inline-flex flex-col items-center gap-1">
                                <span
                                  className={cn(
                                    'inline-flex items-center whitespace-nowrap rounded-full border px-2.5 py-0.5 text-xs font-medium',
                                    STATUS_PILL_STYLE[tone],
                                  )}
                                >
                                  {state}
                                </span>
                                {listing.runtimeFailures?.[id] && (
                                  <p className="max-w-lg text-xs text-destructive">
                                    {listing.runtimeFailures[id]}
                                  </p>
                                )}
                              </div>
                            </TableCell>
                            <TableCell className="py-3.5 pr-2 whitespace-nowrap text-right">
                              <Button
                                variant="ghost"
                                size="sm"
                                aria-label="复制为新协议"
                                disabled={!isLoaded}
                                onClick={() => void copyPreset(id)}
                              >
                                <Copy className="h-3.5 w-3.5" aria-hidden />
                                创建副本
                              </Button>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </table>
                </div>
              </section>
            )}

            <section className="space-y-2">
              <div className="flex items-center justify-between gap-2 border-b border-border/50 pb-3">
                <h2 className="text-sm font-semibold tracking-tight text-foreground">
                  自定义协议
                </h2>
                <span className="font-mono text-xs text-muted-foreground">
                  共 {customDrafts.length} 项
                </span>
              </div>

              {customDrafts.length === 0 ? (
                <EmptyState
                  title="暂无自定义协议"
                  action={
                    <Button variant="primary" className="h-9 px-5" onClick={() => setSelection({})}>
                      新建协议
                    </Button>
                  }
                />
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <TableHeader>
                      <TableRow className="border-b border-border/50 hover:bg-transparent">
                        <TableHead className="py-3 pl-2">协议</TableHead>
                        <TableHead className="py-3 text-center">版本</TableHead>
                        <TableHead className="py-3 text-center">状态</TableHead>
                        <TableHead className="py-3 pr-2 text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody className="divide-y divide-border/30">
                      {customDrafts.map((draft) => {
                        const active = listing.active.find(
                          (entry) => entry.protocolId === draft.protocolId,
                        )
                        const isEnabled =
                          Boolean(active) &&
                          listing.loaded?.[draft.protocolId] === active?.revisionHash
                        const state = active
                          ? isEnabled
                            ? '已启用'
                            : '需重新验证或修复'
                          : '未启用'
                        const tone = isEnabled ? 'jade' : active ? 'amber' : 'muted'
                        const displayName = extractDraftName(draft, protocols)
                        const hasDistinctName =
                          Boolean(displayName) && displayName !== draft.protocolId
                        return (
                          <TableRow key={draft.protocolId} className="border-b-0">
                            <TableCell className="py-3.5 pl-2">
                              <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
                                {hasDistinctName ? (
                                  <>
                                    <span className="font-medium text-foreground">
                                      {displayName}
                                    </span>
                                    <span className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                                      {draft.protocolId}
                                    </span>
                                  </>
                                ) : (
                                  <span className="font-mono font-medium text-foreground">
                                    {draft.protocolId}
                                  </span>
                                )}
                                {draft.updatedAt && (
                                  <span className="text-2xs text-muted-foreground/70">
                                    · {formatRelative(draft.updatedAt)}
                                  </span>
                                )}
                              </div>
                            </TableCell>
                            <TableCell className="py-3.5 whitespace-nowrap text-center font-mono text-xs text-muted-foreground">
                              {draft.hash ? draft.hash.slice(0, 12) : '—'}
                            </TableCell>
                            <TableCell className="py-3.5 text-center">
                              <div className="inline-flex flex-col items-center gap-1">
                                <span
                                  className={cn(
                                    'inline-flex items-center whitespace-nowrap rounded-full border px-2.5 py-0.5 text-xs font-medium',
                                    STATUS_PILL_STYLE[tone],
                                  )}
                                >
                                  {state}
                                </span>
                                {listing.runtimeFailures?.[draft.protocolId] && (
                                  <p className="max-w-lg text-xs text-destructive">
                                    {listing.runtimeFailures[draft.protocolId]}
                                  </p>
                                )}
                              </div>
                            </TableCell>
                            <TableCell className="py-3.5 pr-2 whitespace-nowrap text-right">
                              <div className="flex items-center justify-end gap-1">
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  aria-label={`编辑 ${draft.protocolId}`}
                                  onClick={() => setSelection({ draft })}
                                >
                                  <Pencil className="h-3.5 w-3.5" aria-hidden />
                                  编辑
                                </Button>
                                <Button
                                  variant="danger"
                                  size="sm"
                                  aria-label={`删除 ${draft.protocolId}`}
                                  onClick={() => setArchiveID(draft.protocolId)}
                                >
                                  <Trash2 className="h-3.5 w-3.5" aria-hidden />
                                  删除
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </table>
                </div>
              )}
            </section>
          </div>
        )}
      </div>
    </>
  )
}
