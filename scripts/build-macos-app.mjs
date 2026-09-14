// Assemble the macOS app bundle (ElysiaApi.app) from the prebuilt darwin binaries.
//
// Prerequisites:
//   - macOS host (uses lipo / iconutil / codesign / hdiutil)
//   - `npm run build` has produced dist/standalone/elysia-api-darwin-{arm64,amd64}
//   - Command Line Tools with Swift runtime libraries for both target architectures
//
// Output:
//   dist/standalone/ElysiaApi.app      universal (arm64 + amd64)
//   dist/standalone/elysia-api-macos.dmg distribution image (app + /Applications link)
//
// The app embeds the backend binary and ships the React WebUI it carries; data
// (config / SQLite / master key / logs) lives in
// ~/Library/Application Support/ElysiaApi and survives updates.

import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readlinkSync, rmSync, rmdirSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { execFileSync, spawnSync } from 'node:child_process'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const releaseDir = join(repoRoot, 'dist', 'standalone')
const appDir = join(releaseDir, 'ElysiaApi.app')
const contentsDir = join(appDir, 'Contents')
const macosDir = join(contentsDir, 'MacOS')
const resourcesDir = join(contentsDir, 'Resources')
const sourceDir = join(repoRoot, 'scripts', 'macos-app')
const logoSource = join(repoRoot, 'packages', 'webui', 'public', 'logo.png')
const armBinary = join(releaseDir, 'elysia-api-darwin-arm64')
const amdBinary = join(releaseDir, 'elysia-api-darwin-amd64')
const dmgPath = join(releaseDir, 'elysia-api-macos.dmg')

function log(message) {
  console.log(`==> ${message}`)
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: repoRoot, stdio: 'inherit', ...options })
  if (result.status !== 0) {
    throw result.error ?? new Error(`${command} failed (${result.status ?? result.signal})`)
  }
}

function capture(command, args) {
  return execFileSync(command, args, { cwd: repoRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
}

function checkToolchain() {
  log('Checking Swift toolchain for macOS 12 (arm64 + x86_64)')
  console.log(`Compiler: ${capture('xcrun', ['--sdk', 'macosx', '--find', 'swiftc'])}`)
  console.log(`SDK:      ${capture('xcrun', ['--sdk', 'macosx', '--show-sdk-path'])}`)
  const probeDir = mkdtempSync(join(tmpdir(), 'elysia-toolchain-check-'))
  try {
    for (const arch of ['arm64', 'x86_64']) {
      const result = spawnSync('xcrun', ['--sdk', 'macosx', 'swiftc', '-target', `${arch}-apple-macos12.0`, '-o', join(probeDir, arch), '-'], {
        cwd: repoRoot,
        input: 'import Cocoa\nprint("ElysiaApi toolchain check")\n',
        encoding: 'utf8',
      })
      if (result.status !== 0) {
        throw new Error([
          `The selected Swift toolchain cannot link ${arch} for macOS 12.`,
          result.error?.message ?? result.stderr?.trim() ?? 'Compiler did not finish.',
          'Use a compatible Universal Command Line Tools package, or select an installed Xcode with DEVELOPER_DIR.',
          'Missing x86_64 Swift compatibility libraries cannot be fixed by changing the SDK alone.',
          'See docs/macos-testing.md. Existing app and DMG outputs have not been changed.',
        ].join('\n'))
      }
    }
  } finally {
    rmSync(probeDir, { recursive: true, force: true })
  }
}

function verifyApp(bundle) {
  run('plutil', ['-lint', join(bundle, 'Contents', 'Info.plist')])
  for (const name of ['ElysiaApi', 'elysia-api']) {
    const executable = join(bundle, 'Contents', 'MacOS', name)
    if (!(statSync(executable).mode & 0o111)) throw new Error(`Not executable: ${executable}`)
    run('lipo', [executable, '-verify_arch', 'arm64', 'x86_64'])
  }
  run('codesign', ['--verify', '--deep', '--strict', '--verbose=2', bundle])
}

function resolveVersion() {
  if (process.env.MACOS_APP_VERSION) return process.env.MACOS_APP_VERSION
  try {
    return capture('git', ['describe', '--tags', '--exact-match'])
  } catch {
    const pkg = JSON.parse(readFileSync(join(repoRoot, 'package.json'), 'utf8'))
    return `v${pkg.version}`
  }
}

if (process.platform !== 'darwin') {
  console.error('The macOS app bundle can only be assembled on macOS (needs lipo/iconutil/codesign/ditto).')
  process.exit(1)
}

// A missing cross-architecture runtime must fail before removing any previous bundle.
try {
  checkToolchain()
} catch (error) {
  console.error(error.message)
  process.exit(1)
}
if (process.argv.includes('--check-toolchain')) process.exit(0)

for (const binary of [armBinary, amdBinary]) {
  if (!existsSync(binary) || !statSync(binary).isFile()) {
    console.error(`Missing ${binary}. Run \`npm run build\` first.`)
    process.exit(1)
  }
}
if (!existsSync(logoSource)) {
  console.error(`Missing logo source: ${logoSource}`)
  process.exit(1)
}

const version = resolveVersion()

log(`Preparing app bundle for ${version}`)
rmSync(appDir, { recursive: true, force: true })
mkdirSync(macosDir, { recursive: true })
mkdirSync(resourcesDir, { recursive: true })

log('Creating universal backend binary (lipo)')
run('lipo', ['-create', '-output', join(macosDir, 'elysia-api'), armBinary, amdBinary])

log('Generating AppIcon.icns (white background) from WebUI logo')
const iconsetDir = join(releaseDir, 'AppIcon.iconset')
const iconGen = join(releaseDir, 'icon-gen')
rmSync(iconsetDir, { recursive: true, force: true })
run('xcrun', ['--sdk', 'macosx', 'swiftc', '-O', '-o', iconGen, join(sourceDir, 'icon-gen.swift')])
run(iconGen, [logoSource, iconsetDir])
rmSync(iconGen, { force: true })
run('iconutil', ['-c', 'icns', iconsetDir, '-o', join(resourcesDir, 'AppIcon.icns')])
rmSync(iconsetDir, { recursive: true, force: true })
cpSync(logoSource, join(resourcesDir, 'logo.png'))

log('Compiling native wrapper (swiftc, universal)')
// 目标系统版本与后端二进制对齐:Go 1.25 构建的 darwin 二进制最低要求 macOS 12
const wrapperArm = join(releaseDir, 'wrapper-arm64')
const wrapperAmd = join(releaseDir, 'wrapper-amd64')
const wrapperSources = [join(sourceDir, 'MacSupport.swift'), join(sourceDir, 'main.swift')]
run('xcrun', ['--sdk', 'macosx', 'swiftc', '-O', '-target', 'arm64-apple-macos12.0', '-o', wrapperArm, ...wrapperSources])
run('xcrun', ['--sdk', 'macosx', 'swiftc', '-O', '-target', 'x86_64-apple-macos12.0', '-o', wrapperAmd, ...wrapperSources])
run('lipo', ['-create', '-output', join(macosDir, 'ElysiaApi'), wrapperArm, wrapperAmd])
rmSync(wrapperArm, { force: true })
rmSync(wrapperAmd, { force: true })

log('Writing Info.plist and version.txt')
// CFBundleVersion 按苹果要求只能是 1-3 段纯数字,git describe 的
// "1.1.4-3-g41c77eb" 不可直接写入:短版本取其数字部分,构建号用提交数
const shortVersion = (version.match(/\d+(\.\d+){0,2}/) ?? ['0.0.0'])[0]
let buildNumber = '0'
try { buildNumber = capture('git', ['rev-list', '--count', 'HEAD']) } catch {}
const plist = readFileSync(join(sourceDir, 'Info.plist'), 'utf8')
  .replaceAll('@@SHORT_VERSION@@', shortVersion)
  .replaceAll('@@BUILD_NUMBER@@', buildNumber)
writeFileSync(join(contentsDir, 'Info.plist'), plist)
writeFileSync(join(resourcesDir, 'version.txt'), `${version}\n`)
chmodSync(join(macosDir, 'ElysiaApi'), 0o755)
chmodSync(join(macosDir, 'elysia-api'), 0o755)

log('Ad-hoc code signing')
run('codesign', ['--force', '--sign', '-', '--deep', appDir])
verifyApp(appDir)

log('Creating DMG image (app + /Applications shortcut)')
const stagingDir = join(releaseDir, 'dmg-staging')
rmSync(stagingDir, { recursive: true, force: true })
rmSync(dmgPath, { force: true })
mkdirSync(stagingDir, { recursive: true })
cpSync(appDir, join(stagingDir, 'ElysiaApi.app'), { recursive: true })
run('ln', ['-s', '/Applications', join(stagingDir, 'Applications')])
run('hdiutil', ['create', '-volname', 'ElysiaApi', '-format', 'UDZO', '-srcfolder', stagingDir, '-o', dmgPath])
rmSync(stagingDir, { recursive: true, force: true })
log('Verifying DMG and mounted contents')
run('hdiutil', ['verify', dmgPath])
const mountDir = mkdtempSync(join(tmpdir(), 'elysia-dmg-check-'))
let mounted = false
try {
  run('hdiutil', ['attach', '-readonly', '-nobrowse', '-mountpoint', mountDir, dmgPath])
  mounted = true
  if (readlinkSync(join(mountDir, 'Applications')) !== '/Applications') {
    throw new Error('DMG is missing the /Applications installation shortcut')
  }
  verifyApp(join(mountDir, 'ElysiaApi.app'))
} finally {
  // Never recursively delete a mount point if detach fails.
  if (mounted) run('hdiutil', ['detach', mountDir])
  rmdirSync(mountDir)
}
const sha256 = capture('shasum', ['-a', '256', dmgPath]).split(' ')[0]

console.log(`App bundle: ${appDir}`)
console.log(`DMG:        ${dmgPath}`)
console.log(`SHA256:     ${sha256}`)
