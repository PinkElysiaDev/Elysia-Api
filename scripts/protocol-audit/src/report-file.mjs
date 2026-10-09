import { writeFile, rename as renameFile } from 'node:fs/promises'
import { setTimeout as delay } from 'node:timers/promises'

// Callers serialize writes to each report. Windows scanners/readers can hold
// the destination briefly; retry only publishing the completed local file,
// never the HTTP call that produced it. Preserve the old report on failure.
export async function atomicFile(path, text, { platform = process.platform, rename = renameFile, wait = delay } = {}) {
  await writeFile(`${path}.tmp`, text, { mode: 0o600 })
  for (let attempt = 0; ; attempt++) {
    try { await rename(`${path}.tmp`, path); return }
    catch (error) {
      if (platform !== 'win32' || !['EPERM', 'EACCES', 'EBUSY'].includes(error.code) || attempt >= 7) throw error
      await wait(Math.min(25 * 2 ** attempt, 400))
    }
  }
}
