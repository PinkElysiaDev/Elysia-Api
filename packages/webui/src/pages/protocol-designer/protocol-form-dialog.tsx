import { useEffect, useRef, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import { api, ApiError } from '@/lib/api'
import { useModels, useSources } from '@/lib/hooks'
import type { CustomProtocolConfig, CustomProtocolSchema } from '@/lib/types'
import { ModelsDiscoveryEditor } from './models-discovery-editor'
import { PreviewTestPanel } from './preview-panel'
import { RequestBuilder } from './request-builder'
import { ResponseBodyTreeEditor } from './body-tree-editor'

type EditorTab = 'basic' | 'request' | 'response' | 'models' | 'test' | 'json'

const TAB_OPTIONS: { value: EditorTab; label: string }[] = [
  { value: 'basic', label: '基本信息' },
  { value: 'request', label: '请求' },
  { value: 'response', label: '响应' },
  { value: 'models', label: '模型拉取' },
  { value: 'test', label: '预览与测试' },
  { value: 'json', label: 'JSON' },
]

export function ProtocolFormDialog({
  open,
  onOpenChange,
  protocol,
  schema,
  onSaved,
  isNew,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  protocol: CustomProtocolConfig | null
  schema: CustomProtocolSchema | undefined
  onSaved: () => Promise<void> | void
  isNew: boolean
}) {
  const { success: toastSuccess, error: toastError } = useToast()
  const { data: sources = [] } = useSources()
  const { data: models = [] } = useModels()
  const [draft, setDraft] = useState<CustomProtocolConfig | null>(protocol)
  const [tab, setTab] = useState<EditorTab>('basic')
  const [saving, setSaving] = useState(false)
  const [jsonDraft, setJsonDraft] = useState('')
  const contentRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (open) {
      setDraft(protocol)
      setTab('basic')
      setJsonDraft('')
      contentRef.current?.scrollTo({ top: 0 })
    }
  }, [open, protocol])

  // 切换标签页时内容区回到顶部，保证各标签页初始位置一致。
  useEffect(() => {
    contentRef.current?.scrollTo({ top: 0 })
  }, [tab])

  // 离开 JSON 标签清空草稿;进入时若为空则用当前 draft 预填(可自由清空
  // 重写,旧实现的 `jsonDraft || 序列化` 兜底会让清空动作瞬间回弹)。
  useEffect(() => {
    if (tab !== 'json') setJsonDraft('')
    else setJsonDraft((prev) => prev || JSON.stringify(draft, null, 2))
  }, [tab, draft])

  if (!draft) return null

  // save 前检查 JSON 草稿:手改未「应用到编辑器」时先应用(可解析)或阻止
  // 保存(语法错误),否则用户粘贴的配置被静默丢弃。
  const applyPendingJsonDraft = (): boolean => {
    if (tab !== 'json' || !jsonDraft) return true
    if (jsonDraft === JSON.stringify(draft, null, 2)) return true
    try {
      const parsed = JSON.parse(jsonDraft) as CustomProtocolConfig
      setDraft(parsed)
      if (!parsed.id?.trim()) {
        toastError('缺少协议 ID', 'id 是必填的短英文标识')
        return false
      }
      return true
    } catch {
      toastError('JSON 源码有语法错误', '请修正后再保存,或切回其他标签页放弃修改')
      return false
    }
  }

  const save = async (): Promise<boolean> => {
    if (!applyPendingJsonDraft()) {
      return false
    }
    if (!draft.id.trim()) {
      toastError('缺少协议 ID', 'id 是必填的短英文标识')
      return false
    }
    setSaving(true)
    try {
      const result = await api.upsertCustomProtocol(draft)
      if (result.warning) {
        toastError('已保存但注册失败', result.warning)
      } else {
        toastSuccess('协议已保存', draft.id)
      }
      await onSaved()
      return true
    } catch (error) {
      toastError('保存失败', error instanceof ApiError ? error.message : String(error))
      return false
    } finally {
      setSaving(false)
    }
  }

  const typeValue = (draft.type || 'llm') as string
  const isCustomType = typeValue.startsWith('x-')
  const stream = draft.response?.stream

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[84vh] w-full max-w-4xl flex-col overflow-hidden max-lg:left-4 max-lg:right-4 max-lg:w-auto">
        <DialogHeader>
          <DialogTitle>{isNew ? '新建协议' : `编辑协议 ${draft.id}`}</DialogTitle>
          <DialogDescription>构造请求体与返回体，映射位就地声明与大自在天字段的对应关系。</DialogDescription>
        </DialogHeader>

        <div className="flex shrink-0 items-center justify-between">
          <Seg value={tab} options={TAB_OPTIONS} onChange={(value) => setTab(value as EditorTab)} size="sm" />
          {typeValue !== 'llm' && (
            <span className="text-2xs text-amber-600 dark:text-amber-400">
              {typeValue} 为预留类型：中转暂仅支持 LLM
            </span>
          )}
        </div>

        <div ref={contentRef} className="min-h-0 flex-1 overflow-y-auto py-1 pr-1">
          {tab === 'basic' && (
            <SettingSection title="基本信息">
              <SettingRow label="协议 ID" required description="模型源以 platform: custom:<id> 引用">
                <Input
                  className="w-52 font-mono text-xs"
                  value={draft.id}
                  disabled={!isNew}
                  onChange={(event) => setDraft({ ...draft, id: event.target.value.trim() })}
                />
              </SettingRow>
              <SettingRow label="名称">
                <Input
                  className="w-52"
                  value={draft.name ?? ''}
                  placeholder="Vendor JSON API"
                  onChange={(event) => setDraft({ ...draft, name: event.target.value })}
                />
              </SettingRow>
              <SettingRow label="版本">
                <Input
                  className="w-28"
                  value={draft.version ?? ''}
                  placeholder="1"
                  onChange={(event) => setDraft({ ...draft, version: event.target.value })}
                />
              </SettingRow>
              <SettingRow label="协议类型" inline={false}>
                <div className="space-y-2">
                  <Seg
                    value={isCustomType ? 'x-' : typeValue}
                    options={[
                      ...(schema?.types ?? []).map((type) => ({ value: type.value, label: type.label.split('（')[0] })),
                      { value: 'x-', label: '自定义扩展' },
                    ]}
                    onChange={(value) =>
                      setDraft({ ...draft, type: value === 'x-' ? 'x-' : (value as CustomProtocolConfig['type']) })
                    }
                  />
                  {isCustomType && (
                    <Input
                      className="w-52 font-mono text-xs"
                      value={typeValue}
                      placeholder="x-tts"
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          type: (event.target.value.startsWith('x-') ? event.target.value : `x-${event.target.value}`) as CustomProtocolConfig['type'],
                        })
                      }
                    />
                  )}
                </div>
              </SettingRow>
            </SettingSection>
          )}

          {tab === 'request' && (
            <RequestBuilder
              request={draft.request}
              onChange={(request) => setDraft({ ...draft, request })}
              requestFields={schema?.requestFields ?? []}
            />
          )}

          {tab === 'response' && (
            <div className="space-y-8">
              <SettingSection title="返回体构造" description="按上游响应示例搭建结构；映射位就地选择对应的大自在天字段">
                <ResponseBodyTreeEditor
                  response={draft.response ?? {}}
                  onChange={(response) => setDraft({ ...draft, response })}
                  fields={schema?.responseFields ?? []}
                  transforms={schema?.transforms ?? []}
                />
              </SettingSection>              <SettingSection
                title="流式映射（可选）"
                action={
                  <Switch
                    checked={!!stream}
                    onCheckedChange={(checked) =>
                      setDraft({
                        ...draft,
                        response: {
                          ...(draft.response ?? {}),
                          stream: checked
                            ? { mode: 'delta', doneValues: ['[DONE]'], response: { body: {} } }
                            : undefined,
                        },
                      })
                    }
                  />
                }
              >
                {!stream ? (
                  <p className="py-2 text-xs text-muted-foreground">上游为 SSE 流式接口时开启。</p>
                ) : (
                  <div>
                    <SettingRow label="payloadPath" description="事件 JSON 内载荷路径">
                      <Input
                        className="w-52 font-mono text-xs"
                        value={stream.payloadPath ?? ''}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            response: { ...(draft.response ?? {}), stream: { ...stream, payloadPath: event.target.value } },
                          })
                        }
                      />
                    </SettingRow>
                    <SettingRow label="mode" description="delta：事件即增量；cumulative：事件为累计全文">
                      <Select
                        value={stream.mode || 'delta'}
                        onValueChange={(value) =>
                          setDraft({
                            ...draft,
                            response: { ...(draft.response ?? {}), stream: { ...stream, mode: value === 'delta' ? '' : value } },
                          })
                        }
                      >
                        <SelectTrigger className="w-44" aria-label="流模式">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="delta">delta（增量）</SelectItem>
                          <SelectItem value="cumulative">cumulative（累计）</SelectItem>
                        </SelectContent>
                      </Select>
                    </SettingRow>
                    <SettingRow label="events" description="只处理这些 SSE event 名，逗号分隔">
                      <Input
                        className="w-56 font-mono text-xs"
                        value={(stream.events ?? []).join(', ')}
                        placeholder="message"
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            response: {
                              ...(draft.response ?? {}),
                              stream: {
                                ...stream,
                                events: event.target.value.split(',').map((item) => item.trim()).filter(Boolean),
                              },
                            },
                          })
                        }
                      />
                    </SettingRow>
                    <SettingRow label="doneValues" description="终止标记，默认 [DONE]">
                      <Input
                        className="w-56 font-mono text-xs"
                        value={(stream.doneValues ?? []).join(', ')}
                        placeholder="[DONE]"
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            response: {
                              ...(draft.response ?? {}),
                              stream: {
                                ...stream,
                                doneValues: event.target.value.split(',').map((item) => item.trim()).filter(Boolean),
                              },
                            },
                          })
                        }
                      />
                    </SettingRow>
                    <p className="pt-2 text-2xs text-muted-foreground">
                      流事件的独立映射在嵌套的 response 中配置（body 构造树或 fields，与主返回体同规则）。
                    </p>
                  </div>
                )}
              </SettingSection>
            </div>
          )}

          {tab === 'models' && <ModelsDiscoveryEditor protocol={draft} onChange={setDraft} />}

          {tab === 'test' && (
            <PreviewTestPanel
              protocol={draft}
              onSaved={() => save()}
              sources={sources}
              models={models}
            />
          )}

          {tab === 'json' && (
            <SettingSection title="JSON 源码" description="协议完整 JSON，可直接粘贴外部编辑后的配置">
              <Textarea
                className="min-h-[320px] font-mono text-xs"
                spellCheck={false}
                value={jsonDraft}
                onChange={(event) => setJsonDraft(event.target.value)}
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  try {
                    const parsed = JSON.parse(jsonDraft || JSON.stringify(draft)) as CustomProtocolConfig
                    if (!parsed.id) throw new Error('缺少 id 字段')
                    setDraft(parsed)
                    toastSuccess('已应用', 'JSON 已载入编辑器')
                  } catch (error) {
                    toastError('JSON 无法解析', String(error))
                  }
                }}
              >
                应用到编辑器
              </Button>
            </SettingSection>
          )}
        </div>

        <DialogFooter className="shrink-0">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
          <Button type="button" onClick={() => void save()} disabled={saving}>
            {saving ? '保存中…' : '保存协议'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
