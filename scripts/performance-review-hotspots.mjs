import { readFile, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'

const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const profiles = JSON.parse(await readFile(join(root, 'profile-index.json')))
const names = ['.(*typedMeter).scanRaw', '.(*semanticCache).normalizeDocument', '.needsSemanticCompaction', '.cloneSemantic', '.(*CompiledConversion).Request', '.(*CompiledConversion).Event', '.(*Compiled).EncodeRequestValue', '.(*Compiled).preserveMappedNative', '.appendLogprobValues', '.sanitizeBody', '.redactTurnStateText', 'regexp.(*Regexp).FindStringIndex', 'runtime.gcDrain', 'runtime.memmove', 'indexbytebody', 'countbody', 'unicode/utf8.ValidString']
const rows = []
for (const p of profiles) {
  const directory = join(root, 'runs', p.run)
  const sources = [await readFile(join(directory, 'cpu-top.txt'), 'utf8'), await readFile(join(directory, 'cpu-cumulative.txt'), 'utf8')]
  const functions = new Map()
  for (const source of sources) for (const line of source.split('\n')) {
    const match = line.match(/^\s*(\S+)\s+(\S+)%\s+(\S+)%\s+(\S+)\s+(\S+)%\s+(.+?)\s*$/)
    if (match) functions.set(match[6].replace(/ \(inline\)$/, ''), { flat: match[1], flatPercent: +match[2], cumulative: match[4], cumulativePercent: +match[5] })
  }
  for (const suffix of names) {
    const found = [...functions].find(([name]) => name.endsWith(suffix))
    rows.push({ run: p.run, version: p.version, totalCPUSeconds: p.seconds, function: found?.[0] || suffix, sampled: !!found, ...(found?.[1] || {}), note: found ? 'Parent cumulative share includes children; do not sum overlapping functions.' : 'Not present in retained top tables; not proof of zero cost.' })
  }
}
await writeFile(join(root, 'cpu-hotspots.json'), JSON.stringify(rows, null, 2))
console.log({ profiles: profiles.length, rows: rows.length })
