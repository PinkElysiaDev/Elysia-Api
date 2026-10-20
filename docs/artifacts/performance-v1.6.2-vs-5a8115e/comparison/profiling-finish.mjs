import { spawn } from 'node:child_process'
import { readFile, writeFile, readdir, copyFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { resolve, join } from 'node:path'

const root = resolve('.')
const before = join(root, '.tmp-dev/profiling-v1.6.2-8f50807')
const after = join(root, '.tmp-dev/profiling-current-5a8115e')
const beforeExtra = join(root, '.tmp-dev/profiling-v1.6.2-legacy')
const afterExtra = join(root, '.tmp-dev/profiling-current-legacy')
const comparison = join(root, '.tmp-dev/profiling-comparison-v1.6.2-5a8115e')
for (const directory of [before, after]) if (JSON.parse(await readFile(join(directory, 'report.json'))).status !== 'passed') throw new Error('Wait for main collections to complete')
async function run(script, args) {
  const child = spawn(process.execPath, [join(root, '.tmp-dev', script), ...args], { cwd: root, stdio: 'inherit' })
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
  if (code !== 0) throw new Error(`${script} failed: ${code}`)
}
await run('profiling-supplement.mjs', ['/tmp/opencode/elysia-profile-v1.6.2', before, beforeExtra])
await run('profiling-supplement.mjs', [root, after, afterExtra])
await run('profiling-compare.mjs', [before, after, beforeExtra, afterExtra, comparison])
await run('profiling-render.mjs', [before, after, beforeExtra, afterExtra, join(comparison, 'flamegraphs')])
for (const name of ['profiling-supplement.mjs', 'profiling-compare.mjs', 'profiling-render.mjs', 'profiling-finish.mjs']) await copyFile(join(root, '.tmp-dev', name), join(comparison, name))
const manifest = []
async function visit(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = join(directory, entry.name)
    if (entry.isDirectory()) await visit(file)
    else if (entry.name !== 'manifest.json') manifest.push({ path: file.slice(comparison.length + 1), sha256: createHash('sha256').update(await readFile(file)).digest('hex') })
  }
}
await visit(comparison)
await writeFile(join(comparison, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
console.log(`Complete comparison and flamegraphs: ${comparison}`)
