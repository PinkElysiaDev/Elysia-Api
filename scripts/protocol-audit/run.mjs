#!/usr/bin/env node
import { readFile } from 'node:fs/promises'
import { existsSync, realpathSync } from 'node:fs'
import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

async function main() {
  const options = {}
  for (let i = 2; i < process.argv.length; i++) {
    const arg = process.argv[i]
    if (arg === '--help') {
      console.log('Usage: node run.mjs [--config config.local.json] [--group all|daily|protocol|sdk|errors|persistence|code] [--out NEW_DIRECTORY] [--dry-run]\nDefault: all groups; real configured channels through an isolated Elysia backend. Node.js >=22.\nExit: 0 passed, 1 failed, 2 incomplete/configuration, 130 interrupted.')
      return
    }
    if (arg === '--dry-run') options.dryRun = true
    else if (['--config', '--out', '--group'].includes(arg) && process.argv[i + 1] && !process.argv[i + 1].startsWith('--')) options[arg.slice(2)] = process.argv[++i]
    else throw new Error(`Unknown or incomplete option: ${arg}`)
  }
  if (Number(process.versions.node.split('.')[0]) < 22) throw new Error('Node.js >=22 is required')
  if (!options.config) {
    const local = join(dirname(fileURLToPath(import.meta.url)), 'config.local.json')
    if (existsSync(local)) options.config = local
    else if (options.group !== 'code') {
      const paths = [join(dirname(local), 'config.example.json'), local].map(path => "'" + path.replaceAll("'", "'\\''") + "'").join(' ')
      throw new Error(`缺少配置：${local}\n请先复制示例配置：\ncp ${paths}\n填写四种渠道的地址、密钥和模型后再运行。`)
    }
  }
  let config
  try { config = options.config ? JSON.parse(await readFile(resolve(options.config), 'utf8')) : {} }
  catch (error) { throw new Error(error.code ? `Cannot read config (${error.code})` : 'Config is not valid JSON') }
  const { mainSuite } = await import('./src/suite.mjs')
  await mainSuite(config, options)
}

if (process.argv[1] && existsSync(process.argv[1]) && fileURLToPath(import.meta.url) === realpathSync(resolve(process.argv[1]))) {
  main().catch(error => { console.error(error.message); process.exitCode = 2 })
}
