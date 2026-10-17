import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { ProtocolDocument } from '@/lib/protocol-document'
import type { ConversionPhase, ConversionRegistry } from '@/lib/conversion-policy'
import { Button } from '@/components/ui/button'
import { field, phases } from './conversion-fields'
import type { ConversionDocumentHandle } from './use-conversion-document'

type Props = { document: ConversionDocumentHandle; registry?: ConversionRegistry }

function readRuleOption(value: unknown, key: string): string {
  if (value && typeof value === 'object' && key in value) {
    const entry = (value as Record<string, unknown>)[key]
    if (typeof entry === 'string') return entry
  }
  return ''
}

/** Edit typed rule controls while keeping arbitrary values in the exact document. */
export function ConversionRuleEditor({ document, registry }: Props) {
  const { text, setText, policy, updateRule, updateFields, addRule, move } = document
  return (
    <details open className="border-t border-border/40 pt-4">
      <summary className="cursor-pointer text-xs font-semibold text-foreground">
        有序规则（{policy?.rules.length ?? 0}）
      </summary>
      <div className="divide-y divide-border/40 pt-2">
        {policy?.rules.map((rule, index) => (
          <section
            key={index}
            className="space-y-3 py-4 first:pt-2"
            aria-label={`规则 ${rule.id}`}
          >
            <div className="flex flex-wrap items-center gap-2.5">
              <label className="inline-flex cursor-pointer items-center gap-1.5 text-xs font-medium text-foreground">
                <input
                  type="checkbox"
                  className="accent-primary"
                  checked={rule.enabled}
                  onChange={(e) => updateRule(index, { enabled: e.target.checked })}
                />
                启用
              </label>
              <input
                aria-label={`规则 ${index + 1} ID`}
                className={field + ' max-w-xs font-mono text-xs'}
                value={rule.id}
                onChange={(e) => updateRule(index, { id: e.target.value })}
              />
              <div className="ml-auto flex items-center gap-1.5">
                <Button size="sm" variant="ghost" onClick={() => move(index, -1)}>
                  <ArrowUp className="h-3.5 w-3.5" aria-hidden />
                  上移
                </Button>
                <Button size="sm" variant="ghost" onClick={() => move(index, 1)}>
                  <ArrowDown className="h-3.5 w-3.5" aria-hidden />
                  下移
                </Button>
                <Button
                  size="sm"
                  variant="danger"
                  onClick={() => setText(new ProtocolDocument(text).remove(`/rules/${index}`))}
                >
                  <Trash2 className="h-3.5 w-3.5" aria-hidden />
                  移除规则
                </Button>
              </div>
            </div>

            <div className="grid gap-3 md:grid-cols-3">
              <label className="space-y-1 text-xs font-medium text-foreground">
                <span>阶段</span>
                <select
                  className={field}
                  value={rule.phase}
                  onChange={(e) => {
                    const next = e.target.value
                    if (next in phases) updateRule(index, { phase: next as ConversionPhase })
                  }}
                >
                  {registry?.phases.map((p) => (
                    <option key={p} value={p}>
                      {phases[p]}
                    </option>
                  ))}
                </select>
              </label>
              <label className="space-y-1 text-xs font-medium text-foreground">
                <span>动作</span>
                <select
                  className={field + ' font-mono text-xs'}
                  value={rule.action}
                  onChange={(e) => {
                    const action = e.target.value
                    const spec = registry?.actionSchemas?.[action]
                    updateRule(index, {
                      action,
                      ...(spec
                        ? {
                            phase: spec.phases.includes(rule.phase) ? rule.phase : spec.phases[0],
                            value:
                              spec.value?.default ??
                              (spec.value?.enum
                                ? typeof rule.value === 'string' &&
                                  spec.value.enum.includes(rule.value)
                                  ? rule.value
                                  : spec.value.enum[0]
                                : undefined),
                          }
                        : {}),
                    })
                  }}
                >
                  {registry?.actions.map((a) => (
                    <option key={a}>{a}</option>
                  ))}
                </select>
              </label>
              <label className="space-y-1 text-xs font-medium text-foreground">
                <span>顺序</span>
                <input
                  type="number"
                  className={field + ' font-mono text-xs'}
                  value={rule.order}
                  onChange={(e) => updateRule(index, { order: Number(e.target.value) })}
                />
              </label>
            </div>

            <div className="grid gap-2.5 md:grid-cols-3 lg:grid-cols-4">
              {(
                ['sourceId', 'targetId', 'model', 'operation', 'transport', 'nodeKind', 'path'] as const
              ).map((key) => (
                <label key={key} className="space-y-1 text-xs text-muted-foreground">
                  <span>匹配 {key}</span>
                  <input
                    className={field + ' font-mono text-xs'}
                    value={rule.match?.[key] ?? ''}
                    onChange={(e) => updateFields(`/rules/${index}/match`, { [key]: e.target.value })}
                  />
                </label>
              ))}
            </div>

            {registry?.actionSchemas?.[rule.action] && (
              <div className="space-y-2 rounded-md border border-border/50 bg-secondary/20 p-3">
                <p className="text-xs text-muted-foreground">
                  {registry.actionSchemas[rule.action].description}
                </p>
                {registry.actionSchemas[rule.action].value?.enum && (
                  <label className="block space-y-1 text-xs font-medium text-foreground">
                    <span>目标编码器</span>
                    <select
                      aria-label={`规则 ${index + 1} 目标编码器`}
                      className={field}
                      value={typeof rule.value === 'string' ? rule.value : ''}
                      onChange={(e) => updateRule(index, { value: e.target.value })}
                    >
                      <option value="">请选择</option>
                      {registry.actionSchemas[rule.action].value?.enum?.map((codec) => (
                        <option key={codec}>{codec}</option>
                      ))}
                    </select>
                  </label>
                )}
                {(rule.action === 'responses_storage' || rule.action === 'request_projection') && (
                  <div className="grid gap-3 md:grid-cols-2">
                    <label className="space-y-1 text-xs font-medium text-foreground">
                      <span>目标编码器</span>
                      <select
                        aria-label={`规则 ${index + 1} 目标编码器`}
                        className={field}
                        value={readRuleOption(rule.value, 'targetCodec')}
                        onChange={(e) =>
                          updateFields(`/rules/${index}/value`, { targetCodec: e.target.value })
                        }
                      >
                        {['responses', 'gemini', 'anthropic', 'openai-chat'].map((codec) => (
                          <option key={codec}>{codec}</option>
                        ))}
                      </select>
                    </label>
                    {rule.action === 'responses_storage' && (
                      <label className="space-y-1 text-xs font-medium text-foreground">
                        <span>无法保存响应时</span>
                        <select
                          aria-label={`规则 ${index + 1} 保存降级`}
                          className={field}
                          value={readRuleOption(rule.value, 'onUnsupported') || 'degrade'}
                          onChange={(e) =>
                            updateFields(`/rules/${index}/value`, { onUnsupported: e.target.value })
                          }
                        >
                          <option value="degrade">兼容模式继续，并记录降级</option>
                          <option value="reject">拒绝保存要求</option>
                        </select>
                      </label>
                    )}
                    {rule.action === 'request_projection' && (
                      <label className="space-y-1 text-xs font-medium text-foreground">
                        <span>来源编码器</span>
                        <select
                          aria-label={`规则 ${index + 1} 来源编码器`}
                          className={field}
                          value={readRuleOption(rule.value, 'sourceCodec')}
                          onChange={(e) =>
                            updateFields(`/rules/${index}/value`, {
                              sourceCodec: e.target.value || undefined,
                            })
                          }
                        >
                          <option value="">未知来源</option>
                          {['responses', 'gemini', 'anthropic', 'openai-chat'].map((codec) => (
                            <option key={codec}>{codec}</option>
                          ))}
                        </select>
                      </label>
                    )}
                  </div>
                )}
              </div>
            )}

            <div className="grid gap-3 md:grid-cols-2">
              <label className="block space-y-1 text-xs font-medium text-foreground">
                <span>动作字段路径</span>
                <input
                  className={field + ' font-mono text-xs'}
                  value={rule.path ?? ''}
                  onChange={(e) => updateRule(index, { path: e.target.value })}
                />
              </label>
              <label className="block space-y-1 text-xs font-medium text-foreground">
                <span>诊断原因</span>
                <input
                  className={field}
                  value={rule.reason ?? ''}
                  onChange={(e) => updateRule(index, { reason: e.target.value })}
                />
              </label>
            </div>
            <p className="text-xs text-muted-foreground">
              value、expression、字段存在条件和资源限制可在完整 JSON 中编辑。
            </p>
          </section>
        ))}
        <Button disabled={!policy} onClick={addRule}>
          <Plus className="h-3.5 w-3.5" aria-hidden />
          添加规则
        </Button>
      </div>
    </details>
  )
}
