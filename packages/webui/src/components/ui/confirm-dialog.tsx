import { useState, type ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './dialog'
import { Button } from './button'

interface ConfirmDialogProps {
  open: boolean
  title: string
  description?: ReactNode
  confirmText?: string
  cancelText?: string
  destructive?: boolean
  loading?: boolean
  onConfirm: () => void
  onOpenChange: (open: boolean) => void
}

/** 二次确认对话框：所有破坏性操作（删除 source/group/token、reset usage）必须经过它。 */
export function ConfirmDialog({
  open,
  title,
  description,
  confirmText = '确认',
  cancelText = '取消',
  destructive = true,
  loading = false,
  onConfirm,
  onOpenChange,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <div className="flex items-center gap-3">
            {destructive && (
              <span className="flex h-10 w-10 items-center justify-center rounded-full bg-destructive/12 text-destructive">
                <AlertTriangle className="h-5 w-5" />
              </span>
            )}
            <DialogTitle>{title}</DialogTitle>
          </div>
          {description && <DialogDescription className="pt-1">{description}</DialogDescription>}
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
            {cancelText}
          </Button>
          <Button variant={destructive ? 'destructive' : 'default'} onClick={onConfirm} disabled={loading}>
            {confirmText}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** 命令式确认 hook，简化页面里的状态管理。 */
export function useConfirm() {
  const [state, setState] = useState<{
    open: boolean
    title: string
    description?: ReactNode
    confirmText?: string
    destructive?: boolean
    resolve?: (value: boolean) => void
  }>({ open: false, title: '' })

  const confirm = (opts: {
    title: string
    description?: ReactNode
    confirmText?: string
    destructive?: boolean
  }) =>
    new Promise<boolean>((resolve) => {
      setState({ ...opts, open: true, resolve })
    })

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      state.resolve?.(false)
      setState((prev) => ({ ...prev, open: false }))
    }
  }

  const handleConfirm = () => {
    state.resolve?.(true)
    setState((prev) => ({ ...prev, open: false }))
  }

  const dialog = (
    <ConfirmDialog
      open={state.open}
      title={state.title}
      description={state.description}
      confirmText={state.confirmText}
      destructive={state.destructive}
      onConfirm={handleConfirm}
      onOpenChange={handleOpenChange}
    />
  )

  return { confirm, dialog }
}
