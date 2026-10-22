import { spawn } from 'node:child_process'
import { mkdir, writeFile, readdir } from 'node:fs/promises'
import { join, resolve } from 'node:path'

// Validation is explicitly separate from all performance/profile windows.
const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const repo = resolve('.')
const results = []
await mkdir(join(root, 'checks'), { recursive: true })
if ((await readdir(join(root, 'checks'))).length) throw new Error('Checks directory is not empty; preserve prior failures and use a new output directory')
async function check(name, command, args, cwd = repo) {
  console.log(new Date().toISOString(), name)
  const start = Date.now(), chunks = []
  const env = { ...process.env, GOMAXPROCS: '4', GOGC: '100', GOMEMLIMIT: '2GiB', CGO_ENABLED: '0' }
  for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|REVIEW_|LIVE_)/.test(key)) delete env[key]
  const child = spawn(command, args, { cwd, env, windowsHide: true, stdio: ['ignore','pipe','pipe'] })
  child.stdout.on('data', b => chunks.push(b)); child.stderr.on('data', b => chunks.push(b))
  const code = await new Promise((r,j) => { child.once('error',j); child.once('close',r) })
  await writeFile(join(root, 'checks', name+'.log'), Buffer.concat(chunks))
  const result = { name, command, args, cwd, code, status: code === 0 ? 'passed' : 'failed', elapsedMS: Date.now()-start }
  results.push(result)
  await writeFile(join(root, 'checks', 'results.json'), JSON.stringify(results,null,2))
  if (code !== 0) console.log('FAILED', name, Buffer.concat(chunks).toString().slice(-1500))
}
for (const file of (await readdir(join(repo,'scripts'))).filter(x => x.startsWith('performance-review') && x.endsWith('.mjs'))) await check('syntax-'+file, 'node', ['--check', join('scripts',file)])
await check('snapshot-boundary', 'node', ['--test','scripts/source-snapshot.test.mjs'])
await check('go-vet', 'go', ['vet','./...'], join(repo,'backend'))
await check('go-full', 'go', ['test','-pgo=off','-count=1','-timeout=10m','./...'], join(repo,'backend'))
console.log(results.map(r => ({ name:r.name, status:r.status })))
if (results.some(r => r.status !== 'passed')) process.exitCode = 1
