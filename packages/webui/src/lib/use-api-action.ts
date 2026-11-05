import { useCallback, useRef, useState } from 'react'
import { useToast } from '@/components/ui/use-toast'

export interface ActionOptions {
  /** 成功提示;省略则只刷新不提示。 */
  success?: { title: string; description?: string }
  /** 成功后需要失效的 SWR 缓存(如 revalidate.models 或页面 mutate)。 */
  refresh?: Array<() => Promise<unknown>>
  /** 失败提示标题,默认「操作失败」。 */
  errorTitle?: string
}

/**
 * 执行动作、刷新缓存并报告结果。同一 key 的重复调用忽略，不同 key 可并行。
 * 需要确认的动作由调用方在 run 之前展示确认框。
 */
export function useApiAction() {
  const toast = useToast()
  const [busyKeys, setBusyKeys] = useState<ReadonlySet<string>>(() => new Set())
  const running = useRef(new Set<string>())

  const run = useCallback(
    async (key: string, action: () => Promise<void>, options: ActionOptions = {}): Promise<boolean> => {
      if (running.current.has(key)) return false
      running.current.add(key)
      setBusyKeys(new Set(running.current))
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
        setBusyKeys(new Set(running.current))
      }
    },
    [toast],
  )

  const isBusy = useCallback((key: string) => busyKeys.has(key), [busyKeys])

  return { run, isBusy }
}
