import { useCallback, useState } from 'react'

/**
 * 实体编辑弹窗的通用状态:当前编辑项 + 弹窗开合 + 新建/编辑入口。
 * 收敛 sources/tokens/groups/protocol-designer 等页重复的
 * 「editing + formOpen + openCreate/openEdit」三件套。
 *
 * @param empty 新建时的空白实体。
 */
export function useEntityFormDialog<T>(empty: T) {
  const [item, setItem] = useState<T>(empty)
  const [open, setOpen] = useState(false)
  const [isNew, setIsNew] = useState(true)

  const openCreate = useCallback(() => {
    setItem(empty)
    setIsNew(true)
    setOpen(true)
  }, [empty])

  const openEdit = useCallback((target: T) => {
    setItem(target)
    setIsNew(false)
    setOpen(true)
  }, [])

  const close = useCallback(() => setOpen(false), [])

  return { item, setItem, open, setOpen, isNew, openCreate, openEdit, close }
}
