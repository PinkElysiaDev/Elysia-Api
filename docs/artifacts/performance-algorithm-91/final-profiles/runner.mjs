import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { resolve, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'

const [beforePath,afterPath,destination] = process.argv.slice(2)
if (!destination) throw new Error('Usage: node structural-profile.mjs BEFORE_REPORT AFTER_REPORT NEW_OUTPUT')
const output=resolve(destination)
mkdirSync(output,{recursive:false})
const result={status:'running',sources:[resolve(beforePath),resolve(afterPath)],steps:[]}
try {
  for (const [index,path] of [beforePath,afterPath].entries()) {
    const report=JSON.parse(readFileSync(path));const label=index?'after':'before'
    if(report.status!=='passed')throw new Error('Accepted matrix required')
    const binary=report.steps.find(s=>s.name==='measure-chat-short').command
    if(createHash('sha256').update(readFileSync(binary)).digest('hex')!==report.binarySHA256)throw new Error('Binary mismatch')
    for(const name of ['chat-short','stream256-cross','image2','image31']) {
      const original=report.steps.find(s=>s.name===`measure-${name==='image31'?'image2':name}`)
      for(const mode of name==='image31'?['measure','memory','cpu']:['memory','cpu']) {
        const directory=join(output,`${label}-${mode}-${name}`);mkdirSync(directory)
        const env={...process.env,GOMAXPROCS:'4',GOGC:'100',GOMEMLIMIT:'2GiB'}
        for(const key of Object.keys(env))if(/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE)/.test(key))delete env[key]
        const settings={...original.environment,ELYSIA_FLAME_OUTPUT:directory,ELYSIA_FLAME_MODE:mode,ELYSIA_FLAME_SECONDS:'15',ELYSIA_FLAME_TIMELINE:mode==='measure'?'1':'',ELYSIA_FLAME_RECOVERY_MS:mode==='measure'?'1000':''}
        if(name==='image31')Object.assign(settings,{ELYSIA_CONVERSION_HTTP_MIB:'31',ELYSIA_FLAME_REQUESTS:'1',ELYSIA_FLAME_REPEATS:'3',ELYSIA_FLAME_WARMUP:'2',ELYSIA_FLAME_STREAM:''})
        Object.assign(env,settings)
        console.log(`${label} ${mode} ${name}`)
        const run=spawnSync(binary,original.argv,{cwd:resolve('backend'),env,encoding:'utf8',maxBuffer:32<<20,timeout:600000})
        writeFileSync(join(directory,'run.log'),(run.stdout??'')+(run.stderr??''))
        result.steps.push({label,name,mode,binary,binarySHA256:report.binarySHA256,sourceCommit:report.commit,command:binary,argv:original.argv,settings,code:run.status})
        if(run.status!==0)throw new Error(`Profile fixture failed: ${run.status}`)
        if(mode!=='measure')for(const sample of mode==='cpu'?['cpu']:['alloc_space','alloc_objects','inuse_space']) {
          const argv=['tool','pprof','-top','-nodecount=40',`-sample_index=${sample}`]
          if(sample.startsWith('alloc_'))argv.push('-base',join(directory,'alloc-before.pprof'))
          argv.push(join(directory,sample==='cpu'?'cpu.pprof':sample==='inuse_space'?'heap-drained.pprof':'alloc-after.pprof'))
          const top=spawnSync('go',argv,{encoding:'utf8',maxBuffer:16<<20})
          if(top.status!==0)throw new Error('pprof failed')
          writeFileSync(join(directory,`${sample}-top.txt`),top.stdout)
        }
      }
    }
  }
  result.status='passed'
}finally{writeFileSync(join(output,'report.json'),JSON.stringify(result,null,2)+'\n')}
