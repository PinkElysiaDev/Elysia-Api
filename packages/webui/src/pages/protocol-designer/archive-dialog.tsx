import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { protocolAPI, type EnabledProtocol, type ProtocolReferences } from '@/lib/protocol-v2'
import { ReferenceList } from './history-page'

const selectClass =
  'mt-1.5 h-[34px] w-full rounded-md border border-input bg-card px-2.5 text-sm text-foreground transition-[border-color,box-shadow] duration-200 focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash disabled:cursor-not-allowed disabled:opacity-50'

export function ProtocolArchiveDialog({
  id,
  onClose,
  onArchived,
}: {
  id: string
  onClose: () => void
  onArchived: () => Promise<void>
}) {
  const [references, setReferences] = useState<ProtocolReferences>()
  const [protocols, setProtocols] = useState<EnabledProtocol[]>([])
  const [mode, setMode] = useState<'block' | 'replace' | 'unbind'>('block')
  const [target, setTarget] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let current = true
    void Promise.all([protocolAPI.references(id), protocolAPI.enabled()])
      .then(([refs, items]) => {
        if (current) {
          setReferences(refs)
          setProtocols(items.filter((item) => item.id !== id))
        }
      })
      .catch((err: unknown) => {
        if (current) setError(String(err))
      })
    return () => {
      current = false
    }
  }, [id])

  const archive = async () => {
    if (!references || busy) return
    setBusy(true)
    setError('')
    try {
      await protocolAPI.archive(
        id,
        references.baseline,
        mode,
        mode === 'replace' ? target : undefined,
      )
      await onArchived()
      onClose()
    } catch (err) {
      setError(String(err))
      try {
        setReferences(await protocolAPI.references(id))
      } catch {
        /* Keep the original operation error visible. */
      }
    } finally {
      setBusy(false)
    }
  }

  const blocked = !!references?.references.length && mode === 'block'

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent hideClose={busy}>
        <DialogHeader>
          <DialogTitle>删除自定义协议</DialogTitle>
          <DialogDescription>
            删除 {id} 后，其草稿和所有保存的修订进入协议历史，可恢复为新的自定义协议。
          </DialogDescription>
        </DialogHeader>
        {!references ? (
          <p role="status" className="text-sm text-muted-foreground">
            正在检查引用…
          </p>
        ) : references.references.length > 0 ? (
          <div className="space-y-3">
            <div className="space-y-2 rounded-lg border border-border/70 bg-secondary/20 p-3">
              <p className="text-xs font-semibold text-foreground">
                协议仍被引用，请先选择处理方式
              </p>
              <ReferenceList items={references.references} />
              {references.affectedModels.length > 0 && (
                <details className="pt-1">
                  <summary className="cursor-pointer text-xs font-medium text-muted-foreground">
                    受影响的模型（{references.affectedModels.length}）
                  </summary>
                  <div className="mt-1.5">
                    <ReferenceList items={references.affectedModels} />
                  </div>
                </details>
              )}
            </div>
            <label className="block text-xs font-medium text-foreground">
              引用处理
              <select
                aria-label="引用处理"
                className={selectClass}
                value={mode}
                onChange={(event) => {
                  const next = event.target.value
                  if (next === 'block' || next === 'replace' || next === 'unbind') {
                    setMode(next)
                  }
                }}
                disabled={busy}
              >
                <option value="block">暂不处理（阻止删除）</option>
                <option value="replace">一键替换为其他协议</option>
                <option value="unbind">一键留空，取消绑定</option>
              </select>
            </label>
            {mode === 'replace' && (
              <label className="block text-xs font-medium text-foreground">
                替换协议
                <select
                  aria-label="替换协议"
                  className={selectClass}
                  value={target}
                  onChange={(event) => setTarget(event.target.value)}
                  disabled={busy}
                >
                  <option value="">请选择已启用协议</option>
                  {protocols.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name || item.id}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {mode === 'unbind' && (
              <p className="rounded-lg border border-border/70 bg-secondary/30 p-3 text-xs leading-relaxed text-muted-foreground">
                受影响模型将显示“未绑定协议”，无法继续调用，直到重新选择协议。模型源与模型记录保留。
              </p>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">没有模型源、模型或模型组引用，可以归档。</p>
        )}
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <DialogFooter>
          <Button disabled={busy} variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="destructive"
            disabled={busy || !references || blocked || (mode === 'replace' && !target)}
            onClick={() => void archive()}
          >
            {busy
              ? '验证并处理…'
              : mode === 'replace'
                ? '替换绑定并归档'
                : mode === 'unbind'
                  ? '取消绑定并归档'
                  : '删除并归档'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
