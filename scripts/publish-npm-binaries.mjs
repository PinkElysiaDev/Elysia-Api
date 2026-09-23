// 从指定 tag 构建 6 平台独立后端二进制，组装为 npm 平台包并（可选）发布。
//
// 用法：
//   node scripts/publish-npm-binaries.mjs --tag v1.4.0            # 构建 + 组装 + dry-run 核对
//   node scripts/publish-npm-binaries.mjs --tag v1.4.0 --publish  # 构建 + 组装 + 发布到 npm
//
// 设计要点：
// - 在 OS 临时目录用 git worktree 检出干净 tag 构建，不触碰工作区未发版变更；
// - 平台包版本号与后端 tag 严格一致（v1.4.0 → 1.4.0），一眼对应；
// - os/cpu 字段隔离平台，npm/yarn/pnpm 安装时自动只装匹配当前机器的那一个
//   （esbuild/rollup 的 optionalDependencies 模式），供 koishi-plugin-elysia-api 消费；
// - 发布前逐包检查 registry，已存在的版本自动跳过（幂等重跑）。
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')

const args = process.argv.slice(2)
const publish = args.includes('--publish')
const keep = args.includes('--keep')
const tagIndex = args.indexOf('--tag')
const tag = tagIndex >= 0 ? args[tagIndex + 1] : ''
if (!/^v\d+\.\d+\.\d+(-[\w.]+)?$/.test(tag)) {
  console.error('用法: node scripts/publish-npm-binaries.mjs --tag v1.4.0 [--publish] [--keep]')
  process.exit(1)
}
const version = tag.replace(/^v/, '')

// goos/goarch → npm 的 os/cpu 标准值（注意不是 go 命名：win32/x64）。
const targets = [
  { suffix: 'windows-amd64', goos: 'windows', goarch: 'amd64', npmOs: 'win32', npmCpu: 'x64', source: `elysia-api-windows-amd64.exe`, binary: 'elysia-api.exe' },
  { suffix: 'windows-arm64', goos: 'windows', goarch: 'arm64', npmOs: 'win32', npmCpu: 'arm64', source: `elysia-api-windows-arm64.exe`, binary: 'elysia-api.exe' },
  { suffix: 'linux-amd64', goos: 'linux', goarch: 'amd64', npmOs: 'linux', npmCpu: 'x64', source: `elysia-api-linux-amd64`, binary: 'elysia-api' },
  { suffix: 'linux-arm64', goos: 'linux', goarch: 'arm64', npmOs: 'linux', npmCpu: 'arm64', source: `elysia-api-linux-arm64`, binary: 'elysia-api' },
  { suffix: 'darwin-amd64', goos: 'darwin', goarch: 'amd64', npmOs: 'darwin', npmCpu: 'x64', source: `elysia-api-darwin-amd64`, binary: 'elysia-api' },
  { suffix: 'darwin-arm64', goos: 'darwin', goarch: 'arm64', npmOs: 'darwin', npmCpu: 'arm64', source: `elysia-api-darwin-arm64`, binary: 'elysia-api' },
]

const buildRoot = join(tmpdir(), 'elysia-api-npm-binaries')
const worktree = join(buildRoot, tag)
const packagesRoot = join(buildRoot, `${tag}-packages`)

function log(message) {
  console.log(`==> ${message}`)
}

function run(command, args_, options = {}) {
  // Windows 下 npm 是 cmd 脚本，需经 cmd.exe 调用；其余直接 spawn。
  const invocation = process.platform === 'win32' && command === 'npm'
    ? { command: 'cmd.exe', args: ['/d', '/s', '/c', 'npm', ...args_] }
    : { command, args: args_ }
  const result = spawnSync(invocation.command, invocation.args, { stdio: 'inherit', ...options })
  if (result.status !== 0) {
    console.error(`命令失败（退出码 ${result.status}）: ${command} ${args_.join(' ')}`)
    process.exit(result.status ?? 1)
  }
}

function runQuiet(command, args_, options = {}) {
  const result = spawnSync(command, args_, { encoding: 'utf8', ...options })
  return { status: result.status, stdout: result.stdout ?? '' }
}

log(`准备 worktree: ${worktree}`)
runQuiet('git', ['worktree', 'remove', '--force', worktree], { cwd: repoRoot })
run('git', ['worktree', 'add', worktree, tag], { cwd: repoRoot, stdio: 'ignore' })

log('安装依赖（worktree）')
run('npm', ['install', '--no-audit', '--no-fund'], { cwd: worktree })

log('构建 6 平台二进制（worktree 内的 build-standalone 链）')
run('npm', ['run', 'build'], { cwd: worktree })

const standaloneDir = join(worktree, 'dist', 'standalone')
for (const target of targets) {
  const source = join(standaloneDir, target.source)
  if (!existsSync(source)) {
    console.error(`缺少构建产物: ${source}`)
    process.exit(1)
  }
}

rmSync(packagesRoot, { recursive: true, force: true })
mkdirSync(packagesRoot, { recursive: true })

for (const target of targets) {
  const name = `elysia-api-backend-${target.suffix}`
  const dir = join(packagesRoot, name)
  mkdirSync(join(dir, 'bin'), { recursive: true })
  cpSync(join(standaloneDir, target.source), join(dir, 'bin', target.binary))
  writeFileSync(join(dir, 'package.json'), `${JSON.stringify({
    name,
    version,
    description: `Elysia-API standalone backend binary (${target.suffix}).`,
    license: 'MIT',
    os: [target.npmOs],
    cpu: [target.npmCpu],
    files: ['bin'],
    repository: {
      type: 'git',
      url: 'git+https://github.com/PinkElysiaDev/Elysia-Api.git',
    },
    bugs: 'https://github.com/PinkElysiaDev/Elysia-Api/issues',
    homepage: 'https://github.com/PinkElysiaDev/Elysia-Api#readme',
  }, null, 2)}\n`)
  const sizeMB = (statSync(join(dir, 'bin', target.binary)).size / 1024 / 1024).toFixed(1)
  log(`组装 ${name}@${version}（${sizeMB} MB, os=${target.npmOs} cpu=${target.npmCpu}）`)
}

const dryRunArgs = publish ? ['publish'] : ['publish', '--dry-run']
for (const target of targets) {
  const name = `elysia-api-backend-${target.suffix}`
  if (publish) {
    const view = runQuiet('npm', ['view', `${name}@${version}`, 'version'])
    if (view.status === 0 && view.stdout.trim() === version) {
      log(`${name}@${version} 已存在于 registry，跳过`)
      continue
    }
  }
  run('npm', dryRunArgs, { cwd: join(packagesRoot, name) })
}

if (publish) {
  log(`全部完成：6 个平台包发布为 ${version}`)
} else {
  log(`dry-run 完成（未发布）。包目录: ${packagesRoot}`)
  log('确认无误后加 --publish 重新执行。')
}
if (!keep) {
  log('清理 worktree')
  run('git', ['worktree', 'remove', '--force', worktree], { cwd: repoRoot, stdio: 'ignore' })
}
