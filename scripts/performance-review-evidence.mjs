import { execFileSync } from 'node:child_process'
import { readFile, writeFile, mkdir, readdir } from 'node:fs/promises'
import { join, resolve, dirname } from 'node:path'
import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'

const root = resolve(process.argv[3] || '.tmp-dev/performance-review-2026-10-22')
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', windowsHide: true, maxBuffer: 128 << 20 })
const sha = bytes => createHash('sha256').update(bytes).digest('hex')
const save = async (path, value) => { await mkdir(dirname(path), { recursive: true }); await writeFile(path, typeof value === 'string' ? value : JSON.stringify(value, null, 2)) }
const archives = ['performance-v1.6.2-vs-5a8115e', 'performance-targeted-77', 'performance-structural-81', 'performance-pipeline-86', 'performance-algorithm-91']
if (process.argv[2] === 'historical') {
  const tar = join(root, 'historical.tar'), destination = join(root, 'historical')
  await mkdir(destination, { recursive: true })
  execFileSync('git', ['archive', '--format=tar', `--output=${tar}`, '6a16292d', ...archives.map(x => `docs/artifacts/${x}`)], { windowsHide: true })
  execFileSync('tar', ['-xf', tar, '-C', destination], { windowsHide: true })
  const results = []
  for (const name of archives) {
    const directory = join(destination, 'docs/artifacts', name)
    const files = await readdir(directory), manifest = files.includes('archive-manifest.json') ? 'archive-manifest.json' : 'manifest.json'
    const entries = JSON.parse(await readFile(join(directory, manifest)))
    const checked = []
    for (const entry of entries) {
      try { const bytes = await readFile(join(directory, entry.path)); checked.push({ ...entry, actualSHA256: sha(bytes), actualBytes: bytes.length, matches: sha(bytes) === entry.sha256 && (entry.bytes === undefined || bytes.length === entry.bytes) }) }
      catch (e) { checked.push({ ...entry, matches: false, error: e.code }) }
    }
    results.push({ name, revision: '6a16292d', manifest, files: checked.length, mismatches: checked.filter(x => !x.matches), checked })
  }
  await save(join(root, 'historical-verification.json'), results)
  console.log(results.map(({ name, files, mismatches }) => ({ name, files, mismatches: mismatches.length })))
} else if (process.argv[2] === 'identity') {
  const results = []
  const versions = { release: '8f50807c', merged: 'ba6f8563', normalized: '93509b13', current: '760b54af' }
  for (const file of (await readdir(join(root, 'identities'))).filter(x => x.endsWith('-binary.json'))) {
    const identity = JSON.parse(await readFile(join(root, 'identities', file)))
    const name = file.slice(0, -'-binary.json'.length), version = name.split('-')[0]
    // Older supplemental manifests omit the revision; prefer the captured base
    // identity before falling back to the original fixed review versions.
    const baseIdentity = identity.revision ? identity : JSON.parse(await readFile(join(root, 'identities', `${version}-binary.json`)))
    const revision = identity.revision || baseIdentity.revision || versions[version]
    if (!revision) throw new Error(`Unknown binary version: ${name}`)
    const suffix = name.slice(version.length + 1)
    const source = suffix ? join(root, `${suffix}-sources`, version) : join(root, 'sources', version)
    const original = git('ls-tree', '-r', '--format=%(objectname)%x09%(path)', revision, 'backend').trim().split('\n')
    const differences = [], missing = []
    for (const line of original) {
      const [hash, path] = line.split('\t')
      if (path.endsWith('_test.go')) continue
      try {
        const bytes = await readFile(join(source, path))
        const actual = createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex')
        if (actual !== hash) differences.push({ path, expected: hash, actual })
      } catch (e) { missing.push({ path, error: e.code }) }
    }
    const adapters = identity.adapters || JSON.parse(await readFile(join(root,'identities',`${version}-adapters.json`)))
    const adapterDifferences = []
    for(const adapter of new Map(adapters.map(a=>[a.file,a])).values()) {
      const path = adapter.file.startsWith('backend/') ? adapter.file : 'backend/'+adapter.file
      try {
        const actual = sha(await readFile(join(source,path)))
        if(actual !== adapter.sha256) adapterDifferences.push({path,expected:adapter.sha256,actual})
      } catch(e) { adapterDifferences.push({path,error:e.code}) }
    }
    const actualBinarySHA256 = sha(await readFile(identity.binary))
    const buildInfo = execFileSync('go', ['version', '-m', identity.binary], { encoding: 'utf8', windowsHide: true })
    await save(join(root, 'identities', `${name}-build-info.txt`), buildInfo)
    results.push({ name, revision, source, productionFiles: original.filter(x => !x.endsWith('_test.go')).length, differences, missing, adapterDifferences, actualBinarySHA256, binaryMatches: identity.sha256 === actualBinarySHA256 })
  }
  await save(join(root, 'identity-verification.json'), results)
  console.log(results)
} else if (process.argv[2] === 'inventory') {
  const commits = JSON.parse(await readFile(join(root, 'commits.json')))
  const classify = p => p.includes('/artifacts/') ? 'historical-artifact' : /(_test\.go|\.spec\.[jt]sx?|\/testdata\/|\/tests\/)/.test(p) ? 'test' : /go\.(mod|sum)$|lock\.|package\.json$/.test(p) ? 'dependency' : /^docs\/|\.md$/.test(p) ? 'documentation' : /generated|sqlcgen|source-snapshot\.json/.test(p) ? 'generated' : /^backend\/|^packages\//.test(p) ? 'production' : 'tooling'
  for (const commit of commits) {
    commit.paths = commit.files.map(line => { const [status, ...paths] = line.split('\t'); return { status, paths, category: classify(paths.at(-1)) } })
    commit.categories = Object.fromEntries([...new Set(commit.paths.map(x => x.category))].map(k => [k, commit.paths.filter(x => x.category === k).length]))
  }
  const net = (await readFile(join(root, 'net-paths.tsv'), 'utf8')).trim().split('\n').map(line => { const [status, ...paths] = line.split('\t'); return { status, paths, category: classify(paths.at(-1)) } })
  await save(join(root, 'commit-inventory.json'), { commits, net, categories: Object.fromEntries([...new Set(net.map(x => x.category))].map(k => [k, net.filter(x => x.category === k).length])) })
  const deletions = net.filter(x => x.status === 'D')
  const notes = deletions.map(x => {
    const path = x.paths[0]
    let responsibility = '需结合所属提交核对；不标记逐行审读完成'
    if (x.category === 'historical-artifact') responsibility = '历史证据移出源码树；本轮从 6a16292d 恢复并校验，完整性结果见 historical-verification.json'
    else if (/legacy|upgrade|migrate|preset|fixture_upgrade|protocol_import|custom_protocol\.go|log_migration/.test(path)) responsibility = '已退役升级／旧配置契约，删除不等价于性能优化；现有版本由 protocol_runtime、binding defaults、store/schema 与当前回归负责，不声称继续支持旧迁移'
    else if (/a2a_types|jsonrpc|mcp_tools/.test(path)) responsibility = '类型／协议职责迁入 SDK 和项目 SDK 适配器；mcp_sdk_test、a2a_sdk_test 覆盖当前契约'
    else if (/agent_cli_commands/.test(path)) responsibility = '路由职责转入 cli_cobra.go；cli_framework_test 与 agent_cli_contract_test'
    else if (/relay_retry/.test(path)) responsibility = 'internal/routing/scheduler.go 与 scheduler_test；调用方保留 gateway retry 规则'
    else if (/store_bridge/.test(path)) responsibility = 'route_cache.go 与 internal/routing 的快照读取替代 getGroups/findAccessToken；route_cache_identity_test、key_permissions_test、token_atomic_test 负责隔离'
    else if (/expr-evaluation/.test(path)) responsibility = '未采用实验原型；结论保留 docs/backend-expr-evaluation.md，不产生生产提速'
    else if (x.category === 'documentation') responsibility = '历史长文由 Git 保留；当前简报和复现工具替代发布位置，证据不能因清理而算作通过'
    else if (x.category === 'test') responsibility = '测试删减／合并单独登记；当前针对性测试通过不能证明被删测试语义全部等价'
    return { ...x, responsibility }
  })
  await save(join(root, 'deleted-path-responsibility.json'), deletions.map((x, i) => notes[i]))
  console.log({ commits: commits.length, nonMerge: commits.filter(x => !x.merge).length, paths: net.length, deleted: deletions.length })
} else if (process.argv[2] === 'manifest') {
  const entries = []
  const excluded = /^(sources|.*-sources|binaries|venv|python)(\/|$)|^historical\/docs\/artifacts\/|^ledger[^/]*\/backend\/|\.(tar|zip|exe)$/
  async function visit(directory, prefix = '') {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const path = prefix + entry.name
      if (path === 'archive-manifest.json' || excluded.test(path)) continue
      if (entry.isDirectory()) await visit(join(directory, entry.name), path + '/')
      else if (entry.isFile()) {
        const digest = createHash('sha256'); let bytes = 0
        for await (const chunk of createReadStream(join(directory, entry.name))) { digest.update(chunk); bytes += chunk.length }
        entries.push({ path, bytes, sha256: digest.digest('hex') })
      }
    }
  }
  await visit(root)
  await save(join(root, 'archive-manifest.json'), { generated: new Date().toISOString(), note: 'Raw runs, logs, figures, reports and verification manifests. Source/binary identities and original historical archives are verified separately; Python packages and temporary build trees are excluded.', files: entries })
  console.log({ files: entries.length, bytes: entries.reduce((n, x) => n + x.bytes, 0) })
} else throw new Error('Usage: performance-review-evidence.mjs historical|identity|inventory|manifest [OUTPUT]')
