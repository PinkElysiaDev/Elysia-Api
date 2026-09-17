import { useCallback, useRef, useState } from 'react'
import { useToast } from '@/components/ui/use-toast'

export interface ActionConfirmOptions {
  title: string
  description?: string
  confirmText?: string
}

export interface ActionOptions {
  /** 二次确认文案;省略则直接执行(开关类操作)。 */
  confirm?: ActionConfirmOptions
  /** 成功提示;省略则只刷新不提示。 */
  success?: { title: string; description?: string }
  /** 成功后需要失效的 SWR 缓存(如 revalidate.models 或页面 mutate)。 */
  refresh?: Array<() => Promise<unknown>>
  /** 失败提示标题,默认「操作失败」。 */
  errorTitle?: string
}

/**
 * 管理动作的统一外壳:确认弹窗(可选)→ busy 记录 → 调用 → 成功/失败
 * toast → 缓存刷新。收敛此前散布 sources/groups/tokens/protocol-designer
 * 等页的六连抄「confirm→删除→toast」与四连抄「busy→开关→toast」样板。
 *
 * busy 的粒度由调用方给定 key(通常是 id):同一 key 重复触发直接忽略,
 * 不同 key 可并行(批量启停场景)。confirm 需要调用方把 useConfirm 的
 * confirm 注入(见 wrapConfirm),使弹窗 UI 与本 hook 解耦。
 */
export function useApiAction() {
  const toast = useToast()
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const running = useRef(new Set<string>())

  const run = useCallback(
    async (key: string, action: () => Promise<void>, options: ActionOptions = {}): Promise<boolean> => {
      if (running.current.has(key)) return false
      running.current.add(key)
      setBusyKey(key)
      try {
        await action()
        if (options.refresh?.length) await Promise.all(options.refresh.map((refresh) => refresh()))
        if (options.success) toast.success(options.success.title, options.success.description)
        return true
      } catch (error) {
        toast.error(options.errorTitle ?? '操作失败', error instanceof Error ? error.message : String(error))
        return false
      } finally {
        running.current.delete(key)
        setBusyKey(null)
      }
    },
    [toast],
  )

  const isBusy = useCallback((key: string) => busyKey === key, [busyKey])

  return { run, isBusy, busyKey }
}
