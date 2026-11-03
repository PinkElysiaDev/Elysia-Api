import { useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { protocolAPI, type ConversionIssue, type ProtocolRevision } from '@/lib/protocol-v2'
import { cn } from '@/lib/utils'

/** 编辑器内原生 select 的统一样式（Input 体系不覆盖 select）。 */
const selectClass =
  'h-[34px] rounded-md border border-input bg-card px-2.5 text-sm text-foreground transition-[border-color,box-shadow] duration-200 focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash disabled:cursor-not-allowed disabled:opacity-50'

/** Review and activate immutable revisions independently of preview inputs. */
export function ProtocolRevisions({ id, revisions, activeHash, isActive, isDirty, run, onIssues, onLoad, onSaved }: {
  id: string
  revisions: ProtocolRevision[]
  activeHash: string
  isActive: boolean
  isDirty: boolean
  run: (work: () => Promise<void>) => Promise<void>
  onIssues: (issues: ConversionIssue[]) => void
  onLoad: (source: string) => void
  onSaved: (id: string) => Promise<void>
}) {
  const [selectedRevision, setSelectedRevision] = useState('')
  const [output, setOutput] = useState<unknown>()
  if (!isActive) return null

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3 border-b border-border/40 pb-4">
        <label className="space-y-1.5 text-xs font-medium text-foreground">
          <span className="block">选择已保存修订</span>
          <select
            aria-label="选择修订"
            className={cn('min-w-[260px] max-w-full font-mono', selectClass)}
            value={selectedRevision}
            onChange={(event) => setSelectedRevision(event.target.value)}
          >
            <option value="">请选择</option>
            {revisions.map((revision) => (
              <option key={revision.hash} value={revision.hash}>
                {revision.hash.slice(0, 16)} · {revision.createdAt}
              </option>
            ))}
          </select>
        </label>
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={!selectedRevision || !activeHash}
            onClick={() =>
              void run(async () =>
                setOutput(await protocolAPI.diff(id, activeHash, selectedRevision)),
              )
            }
          >
            比较启用版本
          </Button>
          <Button
            disabled={!selectedRevision || isDirty}
            onClick={() => {
              const revision = revisions.find((item) => item.hash === selectedRevision)
              if (revision) {
                onLoad(revision.definition)
              }
            }}
          >
            载入为草稿
          </Button>
          <Button
            disabled={!selectedRevision}
            onClick={() =>
              void run(async () => {
                const result = await protocolAPI.verifyRevision(id, selectedRevision)
                onIssues(result.issues)
                setOutput(result)
                if (!result.passed) return
                await protocolAPI.activate(id, selectedRevision, activeHash, true)
                await onSaved(id)
              })
            }
          >
            <RotateCcw className="h-3.5 w-3.5" aria-hidden />
            验证并回滚至所选修订
          </Button>
        </div>
      </div>

      <pre className="max-h-96 overflow-auto rounded-lg border border-border/60 bg-[var(--code-bg)] p-3.5 font-mono text-xs">
        {output === undefined
          ? ''
          : typeof output === 'string'
            ? output
            : JSON.stringify(output, null, 2)}
      </pre>
    </div>
  )
}
