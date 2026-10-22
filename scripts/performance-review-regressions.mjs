import { readFile, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'

const root=resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const data=JSON.parse(await readFile(join(root,'summary.json')))
const metrics=['cpuMS','latencyMS','bytes','heapMiB','rssMiB']
const entries=[]
for(const initial of data.summaries.filter(s=>/^(main|extended|latest)-/.test(s.name) && !/^latest-(confirm|image-confirm)-/.test(s.name))) {
  const scenario=initial.name.replace(/^(main|extended|latest)-/,'')
  for(const [edge, values] of Object.entries(initial.edges)) {
    if(edge==='merged->current') continue
    for(const metric of metrics) {
      const value=values[metric]
      if(!(value.ratioOfMediansPercent>10 || value.mixedDirection)) continue
      const labels=initial.name.startsWith('latest-')?['latest-confirm','latest-image-confirm','latest-confirm-rest']:['confirm','confirm-rest','confirm-final']
      const confirmations=labels.flatMap(label=>{
        const group=data.summaries.find(s=>s.name===label+'-'+scenario)
        const result=group?.edges?.[edge]?.[metric]
        return result?[{name:group.name,...result}]:[]
      })
      const latest=confirmations.at(-1)
      const status=!latest?'no-independent-confirmation':latest.mixedDirection?'uncertain-mixed-pairs':Math.sign(latest.ratioOfMediansPercent)!==Math.sign(value.ratioOfMediansPercent)?'direction-reversed':latest.ratioOfMediansPercent>10?'confirmed-cost-regression':latest.ratioOfMediansPercent<0?'confirmed-improvement':'confirmed-cost-increase-below-10-percent'
      entries.push({scenario:initial.name,edge,metric,initial:value,confirmations,status})
    }
  }
}
await writeFile(join(root,'regression-review.json'),JSON.stringify(entries,null,2))
const lines=['# 回退与方向反复登记','','原样保留初次结果；确认只补证据，不覆盖旧测量。成本增加超过10%或逐对方向不一致会进入下表。无独立确认的项不能宣称稳定。','','| 原场景 | 版本边 | 指标 | 初次中位数比值变化 | 独立确认 | 状态 |','|---|---|---|---:|---|---|']
for(const x of entries) lines.push(`| ${x.scenario} | ${x.edge} | ${x.metric} | ${x.initial.ratioOfMediansPercent?.toFixed(1)}% | ${x.confirmations.map(c=>`${c.name}: ${c.ratioOfMediansPercent?.toFixed(1)}%, mixed=${c.mixedDirection}`).join('; ')||'未补测'} | ${x.status} |`)
await writeFile(join(root,'regression-review.md'),lines.join('\n')+'\n')
console.log({entries:entries.length,statuses:Object.fromEntries([...new Set(entries.map(e=>e.status))].map(s=>[s,entries.filter(e=>e.status===s).length]))})
