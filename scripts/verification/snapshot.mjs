import { execFileSync } from 'node:child_process'
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { createHash } from 'node:crypto'

/** Creates an isolated local source snapshot without changing the worktree. */
export async function snapshotRevision(root, directory, revision, overlays = []) {
  const commit = execFileSync('git', ['rev-parse', '--verify', `${revision}^{commit}`], { cwd: root, encoding: 'utf8', windowsHide: true }).trim()
  const checkout = join(directory, 'source')
  await mkdir(checkout, { recursive: true })
  const archive = join(directory, 'source.tar')
  execFileSync('git', ['archive', '--format=tar', `--output=${archive}`, commit, 'backend'], { cwd: root, windowsHide: true })
  execFileSync('tar', ['-xf', archive, '-C', checkout], { windowsHide: true })
  const overlayHashes = {}
  for (const file of overlays) {
    await copyFile(join(root, file), join(checkout, file))
    overlayHashes[file] = createHash('sha256').update(await readFile(join(checkout, file))).digest('hex')
  }
  await writeFile(join(directory, 'snapshot.json'), JSON.stringify({ commit, overlays: overlayHashes }, null, 2))
  return { checkout, commit, overlays: overlayHashes }
}
