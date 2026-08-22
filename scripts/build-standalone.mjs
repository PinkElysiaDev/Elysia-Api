import { cpSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const releaseDir = join(repoRoot, 'dist', 'standalone')
const backendDir = join(repoRoot, 'backend')
const webuiDist = join(repoRoot, 'packages', 'webui', 'dist')
const embeddedWebuiDist = join(backendDir, 'webui', 'dist')

// 与主机无关，四个目标全部交叉编译。darwin 二进制同时是 macOS DMG 的组装输入；
// DMG 只能在 macOS 上组装，发布时由 CI 产出（本地可用 npm run build:macos-app）。
const targets = [
  { goos: 'windows', goarch: 'amd64', output: 'elysia-api-windows-amd64.exe' },
  { goos: 'linux', goarch: 'amd64', output: 'elysia-api-linux-amd64' },
  { goos: 'darwin', goarch: 'amd64', output: 'elysia-api-darwin-amd64' },
  { goos: 'darwin', goarch: 'arm64', output: 'elysia-api-darwin-arm64' },
]

function run(command, args, options = {}) {
  const invocation = commandForPlatform(command, args)
  const result = spawnSync(invocation.command, invocation.args, {
    cwd: repoRoot,
    stdio: 'inherit',
    ...options,
  })
  if (result.status !== 0) {
    process.exit(result.status ?? 1)
  }
}

function commandForPlatform(command, args) {
  if (process.platform !== 'win32') return { command, args }
  if (command === 'npm') return { command: 'cmd.exe', args: ['/d', '/s', '/c', 'npm', ...args] }
  if (command === 'go') return { command: 'go.exe', args }
  return { command, args }
}

function log(message) {
  console.log(`==> ${message}`)
}

function stripTrailingWhitespace(dir) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    const stat = statSync(path)
    if (stat.isDirectory()) {
      stripTrailingWhitespace(path)
      continue
    }
    if (!/\.(css|html|js)$/.test(entry)) continue
    const content = readFileSync(path, 'utf8')
    const normalized = content.replace(/[ \t]+$/gm, '')
    if (normalized !== content) {
      writeFileSync(path, normalized)
    }
  }
}

log('Building WebUI')
run('npm', ['run', 'build', '--workspace', '@root/webui'])

log('Syncing WebUI assets into backend/webui/dist')
rmSync(embeddedWebuiDist, { recursive: true, force: true })
mkdirSync(embeddedWebuiDist, { recursive: true })
cpSync(webuiDist, embeddedWebuiDist, { recursive: true })
stripTrailingWhitespace(embeddedWebuiDist)
// 恢复 embed 占位文件：go:embed all:dist 依赖目录非空，该文件被 git 跟踪。
writeFileSync(join(embeddedWebuiDist, '.gitkeep'), '')

log('Preparing standalone release directory')
rmSync(releaseDir, { recursive: true, force: true })
mkdirSync(releaseDir, { recursive: true })

for (const target of targets) {
  log(`Building ${target.output} (${target.goos}/${target.goarch})`)
  run('go', ['build', '-ldflags', '-s -w', '-o', join(releaseDir, target.output), '.'], {
    cwd: backendDir,
    env: {
      ...process.env,
      CGO_ENABLED: '0',
      GOOS: target.goos,
      GOARCH: target.goarch,
    },
  })
}

log(`Standalone release built: ${releaseDir}`)
