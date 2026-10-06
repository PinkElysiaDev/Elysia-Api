import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { createReadStream, createWriteStream } from 'node:fs'
import { access, mkdir, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { pipeline } from 'node:stream/promises'
import { Readable } from 'node:stream'

// Digest published with this exact asset by the official GitHub release API.
const release = {
  version: '20260922', name: 'llvm-mingw-20260922-ucrt-x86_64',
  url: 'https://github.com/mstorsjo/llvm-mingw/releases/download/20260922/llvm-mingw-20260922-ucrt-x86_64.zip',
  sha256: 'e3ad77d117a4bea19a7a3b333341824d79a5a371004a10e25b8504e7b3047666',
  metadata: 'https://api.github.com/repos/mstorsjo/llvm-mingw/releases/tags/20260922',
  assetURL: 'https://api.github.com/repos/mstorsjo/llvm-mingw/releases/assets/581762383',
}

async function digest(path) {
  const hash = createHash('sha256')
  for await (const bytes of createReadStream(path)) hash.update(bytes)
  return hash.digest('hex')
}

/** Prepares a pinned compiler in the workspace cache; never changes system PATH. */
export async function prepareRaceEnvironment(root) {
  if (process.platform !== 'win32') return { env: { ...process.env, CGO_ENABLED: '1' }, evidence: { kind: 'installed', cc: process.env.CC || 'cc' } }
  const cache = join(dirname(root), '.cache', 'toolchains')
  await mkdir(cache, { recursive: true })
  const archive = join(cache, `${release.name}.zip`)
  let isVerified = false
  try { isVerified = await digest(archive) === release.sha256 } catch (error) { if (error.code !== 'ENOENT') throw error }
  if (!isVerified) {
    console.log(`Downloading official toolchain ${release.version}`)
    try {
      const response = await fetch(release.url, { signal: AbortSignal.timeout(300000) })
      if (!response.ok) throw new Error(`Toolchain download failed: HTTP ${response.status}`)
      await pipeline(Readable.fromWeb(response.body), createWriteStream(archive))
    } catch {
      // The official asset API is also usable behind system-configured proxies.
      execFileSync('curl.exe', ['--fail', '--location', '--silent', '--show-error', '--retry', '1', '--connect-timeout', '20', '--max-time', '300', '-H', 'Accept: application/octet-stream', '-H', 'User-Agent: Elysia-local-verification', '--output', archive, release.assetURL], { windowsHide: true, timeout: 650000 })
    }
    if (await digest(archive) !== release.sha256) throw new Error('Official toolchain checksum mismatch; archive not extracted')
  }
  const bin = join(cache, release.name, 'bin')
  const cc = join(bin, 'x86_64-w64-mingw32-gcc.exe')
  try { await access(cc) } catch (error) {
    if (error.code !== 'ENOENT') throw error
    execFileSync('tar', ['-xf', archive, '-C', cache], { windowsHide: true, timeout: 180000 })
  }
  const env = { ...process.env, CGO_ENABLED: '1', CC: cc }
  const pathKey = Object.keys(env).find(key => key.toLowerCase() === 'path') || 'PATH'
  env[pathKey] = `${bin};${env[pathKey] || ''}`
  const version = execFileSync(cc, ['--version'], { env, encoding: 'utf8', windowsHide: true }).trim()
  const evidence = { ...release, archive, compiler: cc, compilerVersion: version, verified: true }
  await writeFile(join(cache, `${release.name}.evidence.json`), JSON.stringify(evidence, null, 2))
  return { env, evidence }
}
