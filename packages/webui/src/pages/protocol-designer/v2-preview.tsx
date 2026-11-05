import { useState } from 'react'
import { CheckCircle2, Play } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { ProtocolDocument } from '@/lib/protocol-document'
import { protocolAPI, type ConversionIssue, type ProtocolSchema } from '@/lib/protocol-v2'
import { cn } from '@/lib/utils'

/** 编辑器内原生 select 的统一样式（Input 体系不覆盖 select）。 */
const selectClass =
  'h-[34px] rounded-md border border-input bg-card px-2.5 text-sm text-foreground transition-[border-color,box-shadow] duration-200 focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash disabled:cursor-not-allowed disabled:opacity-50'

/** Preview inputs and results belong to this panel and survive tab changes. */
export function ProtocolPreview({ source, schema, isActive, hasPendingSections, run, onIssues }: {
  source: string
  schema: ProtocolSchema
  isActive: boolean
  hasPendingSections: boolean
  run: (work: () => Promise<void>) => Promise<void>
  onIssues: (issues: ConversionIssue[]) => void
}) {
  const [direction, setDirection] = useState('decode_request')
  const [input, setInput] = useState('{}')
  const [isSequence, setSequence] = useState(false)
  const [previewMode, setPreviewMode] = useState('mapping')
  const [workflowSample, setWorkflowSample] = useState('')
  const [countModel, setCountModel] = useState('')
  const [taskOperation, setTaskOperation] = useState('')
  const [taskKind, setTaskKind] = useState('decode')
  const [taskPurpose, setTaskPurpose] = useState('status')
  const [output, setOutput] = useState<unknown>()
  const [target, setTarget] = useState('{}')
  if (!isActive) return null

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-4 border-b border-border/40 pb-4">
        <label className="space-y-1 text-xs font-medium text-foreground">
          <span className="block">预览类型</span>
          <select
            aria-label="预览类型"
            className={selectClass}
            value={previewMode}
            onChange={(event) => {
              setPreviewMode(event.target.value)
              if (event.target.value === 'models') setDirection('decode_response')
              if (event.target.value === 'count_tokens') setDirection('decode_request')
            }}
          >
            <option value="mapping">请求 / 响应 / 事件</option>
            <option value="session">完整双向会话</option>
            <option value="task">任务操作</option>
            <option value="agent">Agent settings</option>
            <option value="models">模型目录</option>
            <option value="count_tokens">输入 Token 计数</option>
          </select>
        </label>
        <label className="space-y-1 text-xs font-medium text-foreground">
          <span className="block">转换方向</span>
          <select
            aria-label="转换方向"
            className={selectClass}
            value={direction}
            onChange={(event) => setDirection(event.target.value)}
          >
            {(previewMode === 'models'
              ? ['decode_response', 'encode_response']
              : previewMode === 'count_tokens'
                ? ['decode_request', 'encode_request', 'decode_response', 'encode_response']
                : schema.directions).map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </label>
        <label className="inline-flex h-[34px] cursor-pointer items-center gap-2 text-xs font-medium text-foreground">
          <input
            type="checkbox"
            className="accent-primary"
            checked={isSequence}
            onChange={(event) => setSequence(event.target.checked)}
          />
          事件序列
        </label>
      </div>

      {(previewMode === 'models' || previewMode === 'count_tokens') && (
        <label className="block space-y-1.5 text-xs font-medium text-foreground">
          <span>{previewMode === 'models' ? '目录操作名（decode_response 发现／encode_response 客户端输出）' : '计数操作名（独立请求与结果映射）'}</span>
          <Input
            value={taskOperation}
            onChange={(event) => setTaskOperation(event.target.value)}
          />
        </label>
      )}
      {previewMode === 'session' && (
        <label className="block space-y-1.5 text-xs font-medium text-foreground">
          <span>会话样例 ID</span>
          <Input
            value={workflowSample}
            onChange={(event) => setWorkflowSample(event.target.value)}
          />
        </label>
      )}
      {previewMode === 'count_tokens' && (
        <label className="block space-y-1.5 text-xs font-medium text-foreground">
          <span>端点模型名（协议路径含模型时填写）</span>
          <Input value={countModel} onChange={(event) => setCountModel(event.target.value)} />
        </label>
      )}
      {previewMode === 'task' && (
        <div className="grid gap-3 md:grid-cols-3">
          <label className="space-y-1.5 text-xs font-medium text-foreground">
            <span>提交操作名</span>
            <Input
              value={taskOperation}
              onChange={(event) => setTaskOperation(event.target.value)}
            />
          </label>
          <label className="space-y-1.5 text-xs font-medium text-foreground">
            <span className="block">任务映射</span>
            <select
              className={cn(selectClass, 'w-full')}
              value={taskKind}
              onChange={(event) => setTaskKind(event.target.value)}
            >
              {['decode', 'encode', 'control'].map((kind) => (
                <option key={kind}>{kind}</option>
              ))}
            </select>
          </label>
          <label className="space-y-1.5 text-xs font-medium text-foreground">
            <span className="block">任务阶段</span>
            <select
              className={cn(selectClass, 'w-full')}
              value={taskPurpose}
              onChange={(event) => setTaskPurpose(event.target.value)}
            >
              {['submit', 'status', 'result', 'cancel'].map((purpose) => (
                <option key={purpose}>{purpose}</option>
              ))}
            </select>
          </label>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-2.5">
          <label className="block space-y-1.5 text-xs font-medium text-foreground">
            <span>输入 JSON</span>
            <Textarea
              aria-label="预览输入 JSON"
              rows={7}
              className="font-mono text-xs"
              value={input}
              onChange={(event) => setInput(event.target.value)}
            />
          </label>
          <Button
            disabled={hasPendingSections}
            onClick={() =>
              void run(async () => {
                new ProtocolDocument(input)
                const result =
                  previewMode === 'mapping'
                    ? await protocolAPI.preview(source, direction, input, isSequence)
                    : await protocolAPI.workflow(
                        source,
                        {
                          mode: previewMode,
                          direction: previewMode === 'models' || previewMode === 'count_tokens' ? direction : undefined,
                          sample: workflowSample,
                          operation: taskOperation,
                          conversionContext: previewMode === 'count_tokens' ? { model: countModel } : undefined,
                          kind: taskKind,
                          purpose: taskPurpose,
                        },
                        input,
                      )
                setOutput(result.exactJSON)
                onIssues(result.issues)
              })
            }
          >
            <Play className="h-3.5 w-3.5" aria-hidden />
            运行转换预览
          </Button>
        </div>

        <div className="space-y-2.5">
          <label className="block space-y-1.5 text-xs font-medium text-foreground">
            <span>目标协议完整 JSON</span>
            <Textarea
              aria-label="目标协议 JSON"
              rows={7}
              className="font-mono text-xs"
              value={target}
              onChange={(event) => setTarget(event.target.value)}
            />
          </label>
          <Button
            disabled={hasPendingSections}
            onClick={() =>
              void run(async () => {
                new ProtocolDocument(target)
                const result = await protocolAPI.combine(source, target)
                setOutput(result)
                onIssues(result.issues)
              })
            }
          >
            <CheckCircle2 className="h-3.5 w-3.5" aria-hidden />
            验证协议组合
          </Button>
        </div>
      </div>

      <pre
        aria-label="转换链路结果"
        className="max-h-96 overflow-auto rounded-lg border border-border/60 bg-[var(--code-bg)] p-3.5 font-mono text-xs leading-relaxed"
      >
        {output === undefined
          ? '预览将显示语义模型、输出与诊断。'
          : typeof output === 'string'
            ? output
            : JSON.stringify(output, null, 2)}
      </pre>
    </div>
  )
}
