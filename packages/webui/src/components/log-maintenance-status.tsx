import type { LogMaintenance } from '@/lib/types'

export function LogMaintenanceStatus({ status, error }: { status?: LogMaintenance; error?: Error }) {
  if (error) return <p role="alert" className="text-xs text-ember">无法更新空间回收状态：{error.message}</p>
  if (!status || status.state === 'idle') return null
  const states = {
    idle: '尚未运行', queued: '空间回收已排队', running: '日志维护与空间回收中',
    waiting: '空间回收等待重试', completed: '空间回收完成', failed: '日志维护或空间回收失败，将自动重试',
  }
  const phases = { idle: '', retention: '清理过期或超额日志', assets: '回收无引用附件', reclaim: '释放数据库空闲页', checkpoint: '收缩 WAL 文件' }
  const sum = (value: LogMaintenance['usageDeleted']) => value.byTTL + value.byRecords + value.byContent
  return (
    <div role="status" aria-live="polite" className="text-xs text-muted-foreground space-y-1 py-2">
      <p className={status.state === 'failed' ? 'text-ember' : ''}>{states[status.state]}{status.pending ? ' · 有后续任务排队' : ''}</p>
      {status.state !== 'completed' && <p>{phases[status.phase]} · 剩余 {status.remainingFreePages.toLocaleString()} 个空闲页</p>}
      <p>本轮删除请求日志 {sum(status.usageDeleted)} 条、系统日志 {sum(status.systemDeleted)} 条、附件 {status.assetsRemoved} 个</p>
      {status.checkpointBlocked && <p>读取事务暂时阻止 WAL 回收，结束后会自动重试。</p>}
      {status.lastError && <p className="text-ember">{status.lastError}</p>}
    </div>
  )
}
