import { useEffect, useRef, useState } from 'react'
import type { RuntimeConfig, UsageLogRuntimeConfig } from '@/lib/types'

/** 目录同步周期的表单默认（与后端 ResolveModelCatalogInterval 默认一致）。 */
const defaultCatalogSyncMinutes = 1440

// 日志管理表单缺省值（后端 GET 返回生效值，老版本无该块时兜底）。
const defaultUsageLog: UsageLogRuntimeConfig = {
  persistEnabled: true,
  retentionDays: 0,
  maxStorageMB: 0,
  maxRecords: 0,
  bodyMaxKB: 1024,
  bodyOnErrorOnly: false,
  externalizeMedia: true,
  cleanupIntervalMinutes: 60,
}

// 远程访问表单缺省值（后端 enabled 缺省视为 true）。
const defaultAgentRemote = { enabled: true, publicUrl: '' }

/** 归一化后的表单类型：数据入口 effect 补齐 usageLog/modelCatalog/outbound/agentRemote 后必非空。 */
export type RuntimeConfigForm = Omit<
  RuntimeConfig,
  'usageLog' | 'modelCatalog' | 'outbound' | 'agentRemote'
> & {
  usageLog: UsageLogRuntimeConfig
  modelCatalog: NonNullable<RuntimeConfig['modelCatalog']>
  outbound: NonNullable<RuntimeConfig['outbound']>
  agentRemote: { enabled: boolean; publicUrl: string }
}

/**
 * 运行配置表单：数据入口一次性补默认块并快照 pristine 基线；更新走三个
 * 命名入口（顶层字段 / usageLog 子字段 / 出站禁止段文本）。保存时经
 * dirtyBlockPayload 只回传用户实际改过的块——页面开着期间 agent 工具或
 * 同事手改的配置不会被本页的旧快照悄悄覆盖回去。
 */
export function useRuntimeConfigForm(data: RuntimeConfig | undefined) {
  const [form, setForm] = useState<RuntimeConfigForm | null>(null)
  const pristineRef = useRef<RuntimeConfigForm | null>(null)

  useEffect(() => {
    if (data)
      setForm({
        ...data,
        usageLog: data.usageLog ?? defaultUsageLog,
        modelCatalog: data.modelCatalog ?? {
          enabled: true,
          url: '',
          syncIntervalMinutes: defaultCatalogSyncMinutes,
        },
        outbound: data.outbound ?? { deniedIpRanges: [] },
        agentRemote: {
          enabled: data.agentRemote?.enabled ?? defaultAgentRemote.enabled,
          publicUrl: data.agentRemote?.publicUrl ?? defaultAgentRemote.publicUrl,
        },
      })
    pristineRef.current = null
    setForm((prev) => {
      if (prev) pristineRef.current = { ...prev }
      return prev
    })
  }, [data])

  function update<K extends keyof RuntimeConfig>(key: K, value: RuntimeConfig[K]) {
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev))
  }

  function updateUsageLog<K extends keyof UsageLogRuntimeConfig>(key: K, value: UsageLogRuntimeConfig[K]) {
    setForm((prev) => (prev ? { ...prev, usageLog: { ...prev.usageLog, [key]: value } } : prev))
  }

  /** 禁止段编辑：textarea 一行一段，保留原始输入（保存时后端 trim 清洗）。 */
  function updateOutboundText(text: string) {
    setForm((prev) =>
      prev
        ? { ...prev, outbound: { ...prev.outbound, deniedIpRanges: text === '' ? [] : text.split('\n') } }
        : prev,
    )
  }

  /** 远程访问子字段更新。 */
  function updateAgentRemote<K extends 'enabled' | 'publicUrl'>(
    key: K,
    value: RuntimeConfigForm['agentRemote'][K],
  ) {
    setForm((prev) => (prev ? { ...prev, agentRemote: { ...prev.agentRemote, [key]: value } } : prev))
  }

  /** 恢复出站禁止段为服务端下发的默认段。 */
  function resetOutboundDefaults() {
    setForm((prev) =>
      prev
        ? { ...prev, outbound: { ...prev.outbound, deniedIpRanges: [...(prev.outbound.defaultDeniedIpRanges ?? [])] } }
        : prev,
    )
  }

  /** 脏块 payload：未改过的块不含在返回值里（调用方与恒发字段展开合并）。 */
  function dirtyBlockPayload() {
    if (!form || !pristineRef.current) return {}
    const pristine = pristineRef.current
    return {
      ...(JSON.stringify(pristine.outbound.deniedIpRanges) !== JSON.stringify(form.outbound.deniedIpRanges)
        ? {
            outbound: {
              deniedIpRanges: form.outbound.deniedIpRanges
                .map((entry) => entry.trim())
                .filter((entry) => entry !== ''),
            },
          }
        : {}),
      ...(JSON.stringify(pristine.usageLog) !== JSON.stringify(form.usageLog) ? { usageLog: form.usageLog } : {}),
      ...(pristine.modelCatalog.syncIntervalMinutes !== form.modelCatalog.syncIntervalMinutes
        ? { modelCatalog: { syncIntervalMinutes: form.modelCatalog.syncIntervalMinutes } }
        : {}),
      ...(pristine.agentRemote.enabled !== form.agentRemote.enabled ||
      pristine.agentRemote.publicUrl.trim() !== form.agentRemote.publicUrl.trim()
        ? { agentRemote: { enabled: form.agentRemote.enabled, publicUrl: form.agentRemote.publicUrl.trim() } }
        : {}),
    }
  }

  return {
    form,
    update,
    updateUsageLog,
    updateOutboundText,
    updateAgentRemote,
    resetOutboundDefaults,
    dirtyBlockPayload,
  }
}
