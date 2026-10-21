import {readFile} from 'node:fs/promises'
import {createHash} from 'node:crypto'
import {gunzipSync} from 'node:zlib'
const root=new URL('./',import.meta.url)
const files=JSON.parse(await readFile(new URL('archive-manifest.json',root)))
for(const file of files){
  if(file.path.includes('..')||file.path.startsWith('/'))throw new Error('Invalid path')
  const data=await readFile(new URL(file.path,root))
  if(data.length!==file.bytes||createHash('sha256').update(data).digest('hex')!==file.sha256)throw new Error(file.path)
  if(file.path.endsWith('.json.gz'))JSON.parse(gunzipSync(data))
}
console.log(JSON.stringify({status:'passed',files:files.length}))
