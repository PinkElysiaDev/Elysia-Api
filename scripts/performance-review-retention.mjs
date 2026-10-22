import { readFile, readdir, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'

// Natural idle observations are kept separate from diagnostic forced-GC heaps.
const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const sustained = [], snapshots = [], observations = []
for (const name of await readdir(join(root, 'runs'))) {
  const dir = join(root, 'runs', name)
  const files = await readdir(dir)
  if (files.includes('heap-active-observation.json')) observations.push({ run: name, ...JSON.parse(await readFile(join(dir, 'heap-active-observation.json'))) })
  if (files.includes('held-progress.json')) snapshots.push({ run: name, samples: JSON.parse(await readFile(join(dir, 'held-progress.json'))) })
  else if (files.includes('held.json')) snapshots.push({ run: name, samples: JSON.parse(await readFile(join(dir, 'held.json'))) })
  if (!name.startsWith('sustain-') || !files.includes('measurements.json')) continue
  const result = JSON.parse(await readFile(join(dir, 'result.json')))
  const rows = JSON.parse(await readFile(join(dir, 'measurements.json'))).filter(r => r.Phase !== 'first-request')
  sustained.push({ run: name, status: result.status, version: result.version, note: 'Repeated rounds in one process, not independent pairs. Idle observations use no forced GC and may include garbage.', rounds: rows.map(r => ({ repeat: r.Repeat, requests: r.Requests, peakHeapMiB: r.Metrics.PeakHeap / 2**20, peakWorkingSetMiB: r.Metrics.PeakWorkingSet / 2**20, allocatedMiB: r.Metrics.Allocated / 2**20, gcCycles: r.Metrics.GCCycles, idle: r.Recovery?.map(o => ({ elapsedMS: o.IdleMS, heapMiB: o.Metrics.Heap / 2**20, workingSetMiB: o.Metrics.WorkingSet / 2**20, active: o.Metrics.ActiveRequests, queue: o.Metrics.QueueDepth, payloadBytes: o.Metrics.QueuedPayloadBytes })) })) })
}
await writeFile(join(root, 'retention.json'), JSON.stringify({ sustained, observations, snapshots }, null, 2))
const lines = ['# 生命周期观察', '', '自然空闲堆包含尚未回收垃圾；单进程有限轮次不能证明长期无泄漏。慢流和取消的强制 GC 快照另列。', '', '| 同进程场景 | 轮数 | 每轮最后空闲堆 MiB | 每轮最后工作集 MiB | 活动 / 队列 / 排队字节是否均归零 |', '|---|---:|---|---|---|']
for (const run of sustained) {
  const idle = run.rounds.map(r => r.idle?.at(-1)).filter(Boolean)
  lines.push(`| ${run.run} | ${run.rounds.length} | ${idle.map(x => x.heapMiB.toFixed(2)).join(' / ')} | ${idle.map(x => x.workingSetMiB.toFixed(2)).join(' / ')} | ${idle.length === run.rounds.length && idle.every(x => x.active === 0 && x.queue === 0 && x.payloadBytes === 0)} |`)
}
lines.push('', `活动 profile 前后确认：${observations.filter(x => x.activeOnBothSides).length} / ${observations.length}。它们可能位于不同阶段，不能直接相除得到同阶段存活量改善。`, '', '慢流取消失败、活动数和强制 GC 结果见 retention.json 的 snapshots；失败不能按排空处理。')
await writeFile(join(root, 'retention.md'), lines.join('\n') + '\n')
console.log({ sustained: sustained.length, activeConfirmed: observations.filter(x => x.activeOnBothSides).length, activeObserved: observations.length, held: snapshots.length })
