import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'

const sha = bytes => createHash('sha256').update(bytes).digest('hex')

/** Pin a completed local simulator build and the package files it will read. */
export async function loadConversionProvider(location) {
  const directory = resolve(location)
  const report = JSON.parse(await readFile(join(directory, 'report.json'), 'utf8'))
  if (report.status !== 'passed' || !['legacy', 'packages', 'production'].includes(report.settings.execution)) throw new Error('Provider requires a completed performance build')
  const binary = join(directory, report.platform === 'win32' ? 'server.test.exe' : 'server.test')
  const binarySHA256 = sha(await readFile(binary))
  if (binarySHA256 !== report.binarySHA256) throw new Error('Provider binary differs from its recorded build')
  const manifest = JSON.parse(await readFile(join(directory, 'source-manifest.json'), 'utf8'))
  const packages = manifest.filter(file => /^backend\/protocol\/builtin\/packages\/[^/]+\/(?:spec|mapping)\.json$/.test(file.path))
  for (const file of packages) {
    if (sha(await readFile(join(directory, 'source', file.path))) !== file.sha256) throw new Error(`Provider package differs from its recorded build: ${file.path}`)
  }
  if (report.settings.execution !== 'legacy' && packages.length === 0) throw new Error('Provider package sources are missing')
  return { directory, binary, binarySHA256, cwd: join(directory, 'source/backend/server'), execution: report.settings.execution,
    commit: report.commit, go: report.go, sourceSHA256: report.sourceSHA256, packages }
}

/** Select only the provider child; the gateway retains the measured execution. */
export function conversionProviderEnvironment(provider) {
  return {
    ELYSIA_CONVERSION_HTTP_PROVIDER_BINARY: provider.binary,
    ELYSIA_CONVERSION_HTTP_PROVIDER_CWD: provider.cwd,
    ELYSIA_CONVERSION_HTTP_PROVIDER_EXECUTION: provider.execution,
    ELYSIA_CONVERSION_HTTP_PROVIDER_COMMIT: provider.commit,
  }
}
