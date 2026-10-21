import {readFileSync,writeFileSync,mkdirSync} from 'node:fs'
import {spawnSync} from 'node:child_process'
import {resolve,join} from 'node:path'
import {createHash} from 'node:crypto'
const root=resolve('.tmp-dev/perf-algorithm/final-cancellation');mkdirSync(root)
const results=[]
for(const [label,reportPath] of [['before','.tmp-dev/perf-algorithm/baseline/report.json'],['after','.tmp-dev/perf-algorithm/item-3-accepted/report.json']]){
 const report=JSON.parse(readFileSync(reportPath)),binary=report.steps.find(s=>s.name==='measure-image2').command
 const hash=createHash('sha256').update(readFileSync(binary)).digest('hex');if(hash!==report.binarySHA256)throw new Error('hash mismatch')
 const directory=join(root,label);mkdirSync(directory)
 const env={...process.env,GOMAXPROCS:'4',GOGC:'100',GOMEMLIMIT:'2GiB'}
 for(const key of Object.keys(env))if(/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE)/.test(key))delete env[key]
 const settings={ELYSIA_CONVERSION_HTTP_CANCEL:'1',ELYSIA_CONVERSION_HTTP_MIB:'2',ELYSIA_CONVERSION_HTTP_WORKERS:'4',ELYSIA_CONVERSION_HTTP_LOG:'off',ELYSIA_CONVERSION_HTTP_OUTPUT:directory}
 Object.assign(env,settings)
 const argv=['-test.run=^TestConversionHTTPCancellationIsolated$','-test.v','-test.timeout=5m']
 const run=spawnSync(binary,argv,{env,cwd:resolve('backend'),encoding:'utf8',maxBuffer:16<<20,timeout:300000})
 writeFileSync(join(directory,'run.log'),(run.stdout??'')+(run.stderr??''))
 console.log(label,run.status,run.stdout)
 if(run.status!==0)throw new Error('cancellation failed')
 results.push({label,binary,binarySHA256:hash,argv,settings})
}
writeFileSync(join(root,'report.json'),JSON.stringify({status:'passed',runs:results},null,2)+'\n')
