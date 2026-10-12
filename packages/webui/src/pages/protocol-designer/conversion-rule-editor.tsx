import { ProtocolDocument } from '@/lib/protocol-document'
import type { ConversionPhase, ConversionRegistry } from '@/lib/conversion-policy'
import { Button } from '@/components/ui/button'
import { field, phases } from './conversion-fields'
import type { useConversionDocument } from './use-conversion-document'

type Props = { document: ReturnType<typeof useConversionDocument>; registry?: ConversionRegistry }

/** Edit typed rule controls while keeping arbitrary values in the exact document. */
export function ConversionRuleEditor({ document, registry }: Props) {
  const { text, setText, policy, updateRule, updateFields, addRule, move } = document
  return (
    <details open>
      <summary className="cursor-pointer">有序规则</summary>
      <div className="space-y-3 pt-3">
        {policy?.rules.map((rule, index) => (
          <section key={index} className="space-y-2 rounded-md border p-3" aria-label={`规则 ${rule.id}`}>
            <div className="flex flex-wrap gap-2">
              <label>
                <input
                  type="checkbox"
                  checked={rule.enabled}
                  onChange={(e) => updateRule(index, { enabled: e.target.checked })}
                />
                启用
              </label>
              <input
                aria-label={`规则 ${index + 1} ID`}
                className={field + ' max-w-xs'}
                value={rule.id}
                onChange={(e) => updateRule(index, { id: e.target.value })}
              />
              <Button size="sm" onClick={() => move(index, -1)}>
                上移
              </Button>
              <Button size="sm" onClick={() => move(index, 1)}>
                下移
              </Button>
              <Button
                size="sm"
                variant="danger"
                onClick={() => setText(new ProtocolDocument(text).remove(`/rules/${index}`))}
              >
                移除规则
              </Button>
            </div>
            <div className="grid gap-2 md:grid-cols-3">
              <label>
                阶段
                <select
                  className={field}
                  value={rule.phase}
                  onChange={(e) => updateRule(index, { phase: e.target.value as ConversionPhase })}
                >
                  {registry?.phases.map((p) => (
                    <option key={p} value={p}>
                      {phases[p]}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                动作
                <select
                  className={field}
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
                                ? typeof rule.value === 'string' && spec.value.enum.includes(rule.value)
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
              <label>
                顺序
                <input
                  type="number"
                  className={field}
                  value={rule.order}
                  onChange={(e) => updateRule(index, { order: Number(e.target.value) })}
                />
              </label>
            </div>
            <div className="grid gap-2 md:grid-cols-3">
              {(['sourceId', 'targetId', 'model', 'operation', 'transport', 'nodeKind', 'path'] as const).map(
                (key) => (
                  <label key={key} className="text-xs">
                    匹配 {key}
                    <input
                      className={field}
                      value={rule.match?.[key] ?? ''}
                      onChange={(e) => updateFields(`/rules/${index}/match`, { [key]: e.target.value })}
                    />
                  </label>
                ),
              )}
            </div>
            {registry?.actionSchemas?.[rule.action] && (
              <div className="space-y-2">
                <p className="text-xs text-muted-foreground">
                  {registry.actionSchemas[rule.action].description}
                </p>
                {registry.actionSchemas[rule.action].value?.enum && (
                  <label className="block text-xs">
                    目标编码器
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
                  <div className="grid gap-2 md:grid-cols-2">
                    <label>
                      目标编码器
                      <select
                        aria-label={`规则 ${index + 1} 目标编码器`}
                        className={field}
                        value={(rule.value as { targetCodec?: string })?.targetCodec ?? ''}
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
                      <label>
                        无法保存响应时
                        <select
                          aria-label={`规则 ${index + 1} 保存降级`}
                          className={field}
                          value={(rule.value as { onUnsupported?: string })?.onUnsupported ?? 'degrade'}
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
                      <label>
                        来源编码器
                        <select
                          aria-label={`规则 ${index + 1} 来源编码器`}
                          className={field}
                          value={(rule.value as { sourceCodec?: string })?.sourceCodec ?? ''}
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
            <label className="block text-xs">
              动作字段路径
              <input
                className={field}
                value={rule.path ?? ''}
                onChange={(e) => updateRule(index, { path: e.target.value })}
              />
            </label>
            <label className="block text-xs">
              诊断原因
              <input
                className={field}
                value={rule.reason ?? ''}
                onChange={(e) => updateRule(index, { reason: e.target.value })}
              />
            </label>
            <p className="text-xs text-muted-foreground">
              value、expression、字段存在条件和资源限制可在完整 JSON 中编辑。
            </p>
          </section>
        ))}
        <Button disabled={!policy} onClick={addRule}>
          添加规则
        </Button>
      </div>
    </details>
  )
}
