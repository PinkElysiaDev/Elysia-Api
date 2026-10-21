import {readFile} from 'node:fs/promises'
import {createHash} from 'node:crypto'
import {gunzipSync} from 'node:zlib'
const root=new URL('./',import.meta.url)
const manifest=JSON.parse(await readFile(new URL('archive-manifest.json',root)))
for(const file of manifest){
  if(file.path.includes('..')||file.path.startsWith('/'))throw new Error('Invalid path')
  const data=await readFile(new URL(file.path,root))
  if(data.length!==file.bytes||createHash('sha256').update(data).digest('hex')!==file.sha256)throw new Error(file.path)
  if(file.path.endsWith('.json.gz'))JSON.parse(gunzipSync(data))
}
const summary=JSON.parse(await readFile(new URL('summary.json',root)))
const profiles=manifest.filter(f=>f.path.startsWith('final-profiles/')&&f.path.endsWith('.pprof'))
if(profiles.length!==summary.rawProfiles||summary.profiles.length!==8)throw new Error('Profile inventory mismatch')
for(const profile of summary.profiles){
  const text=await readFile(new URL(profile.path,root),'utf8')
  const seconds=Number(text.match(/Total samples = ([0-9.]+)s/)?.[1])
  if(!(seconds>10)||seconds!==profile.cpuSampleSeconds)throw new Error('Insufficient CPU samples')
}
console.log(JSON.stringify({status:'passed',files:manifest.length,profiles:profiles.length,cpuProfiles:summary.profiles.length}))
