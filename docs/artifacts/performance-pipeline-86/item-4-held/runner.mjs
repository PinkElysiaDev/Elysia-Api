import {spawnSync} from 'node:child_process'
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs'
import {resolve,join} from 'node:path'
import {createHash} from 'node:crypto'
const [before,after,destination]=process.argv.slice(2)
const out=resolve(destination);mkdirSync(out,{recursive:false})
const result={status:'running',beforeCommit:'917041d',diagnostic:'four held 2 MiB streams, forced-GC retention only',order:['before','after','after','before'],runs:[]}
try{
 for(const [i,label] of result.order.entries()){
  const binary=resolve(label==='before'?before:after),dir=join(out,`${i}-${label}`);mkdirSync(dir)
  const env={...process.env,GOMAXPROCS:'4',GOGC:'100',GOMEMLIMIT:'2GiB'}
  for(const key of Object.keys(env))if(/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE|HELD_STREAM_)/.test(key))delete env[key]
  const settings={ELYSIA_HELD_STREAM_PROFILE:'1',ELYSIA_CONVERSION_HTTP_CANCEL:'1',ELYSIA_CONVERSION_HTTP_MIB:'2',ELYSIA_CONVERSION_HTTP_WORKERS:'4',ELYSIA_CONVERSION_HTTP_LOG:'off',ELYSIA_CONVERSION_HTTP_OUTPUT:dir}
  Object.assign(env,settings)
  console.log(label,i)
  const argv=['-test.run=^TestGatewayHeldStreamRetention$','-test.v','-test.timeout=5m']
  const run=spawnSync(binary,argv,{env,encoding:'utf8',timeout:300000,maxBuffer:16<<20})
  writeFileSync(join(dir,'run.log'),(run.stdout??'')+(run.stderr??''))
  if(run.status!==0)throw new Error(`Held experiment failed ${run.status}: ${run.stdout} ${run.stderr}`)
  const measurements=[0,1,2].map(n=>JSON.parse(readFileSync(join(dir,`held-${n}.json`))))
  result.runs.push({label,binary,binarySHA256:createHash('sha256').update(readFileSync(binary)).digest('hex'),argv,settings,measurements})
 }
 result.status='passed'
 const median=a=>{a.sort((x,y)=>x-y);return(a[2]+a[3])/2}
 result.results=['before','after'].map(label=>{const a=result.runs.filter(r=>r.label===label).flatMap(r=>r.measurements);return{label,heldHeapMiB:median(a.map(m=>m.Held.Heap/2**20)),heldRSSMiB:median(a.map(m=>m.Held.WorkingSet/2**20)),settledHeapMiB:median(a.map(m=>m.Settled.Heap/2**20))}})
 console.log(JSON.stringify(result.results,null,2))
}finally{writeFileSync(join(out,'report.json'),JSON.stringify(result,null,2)+'\n')}
