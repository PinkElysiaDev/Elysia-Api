import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { loadConversionProvider, conversionProviderEnvironment } from './conversion-provider.mjs'

const sha = bytes => createHash('sha256').update(bytes).digest('hex')

test('fixed providers bind a binary and the package snapshot, not mutable workspace files', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'elysia-provider-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const packagePath = 'backend/protocol/builtin/packages/test/spec.json'
  await mkdir(join(directory, 'source/backend/protocol/builtin/packages/test'), { recursive: true })
  await writeFile(join(directory, 'source', packagePath), '{}')
  await writeFile(join(directory, 'server.test'), 'binary')
  await writeFile(join(directory, 'source-manifest.json'), JSON.stringify([{ path: packagePath, sha256: sha('{}') }]))
  await writeFile(join(directory, 'report.json'), JSON.stringify({ status: 'passed', platform: 'linux', binarySHA256: sha('binary'), commit: 'fixture', sourceSHA256: 'source', go: 'toolchain', settings: { execution: 'packages' } }))
  const provider = await loadConversionProvider(directory)
  assert.equal(provider.cwd, join(directory, 'source/backend/server'))
  assert.equal(conversionProviderEnvironment(provider).ELYSIA_CONVERSION_HTTP_PROVIDER_EXECUTION, 'packages')
  assert.equal(conversionProviderEnvironment(provider).ELYSIA_CONVERSION_HTTP_EXECUTION, undefined)
  await writeFile(join(directory, 'source', packagePath), '{"changed":true}')
  await assert.rejects(loadConversionProvider(directory), /package differs/)
  await writeFile(join(directory, 'source', packagePath), '{}')
  await writeFile(join(directory, 'server.test'), 'changed')
  await assert.rejects(loadConversionProvider(directory), /binary differs/)
  const report = JSON.parse(await readFile(join(directory, 'report.json'), 'utf8'))
  report.status = 'running'
  await writeFile(join(directory, 'report.json'), JSON.stringify(report))
  await assert.rejects(loadConversionProvider(directory), /completed/)
})
