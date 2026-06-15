import { Context } from 'koishi'
import { ChildProcess, spawn } from 'child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'fs'
import { dirname, isAbsolute, join, resolve } from 'path'
import { randomBytes } from 'crypto'
import { Config } from './config'

interface BootstrapConfig {
  host: string
  port: number
  panelAccessToken: string
  databasePath: string
  logLevel: string
  httpTimeout: number
  secretKeyPath?: string
  webuiDir?: string
  enablePprof?: boolean
  maxBodyBytes?: number
}

export class StandaloneBackendManager {
  private process: ChildProcess | null = null
  private lastConfigHash = ''

  constructor(private ctx: Context, private config: Config) {}

  updateConfig(config: Config) {
    this.config = config
  }

  getConfigPath() {
    return this.resolvePath(this.config.configPath)
  }

  getWebUIURL() {
    return `http://${this.config.host}:${this.config.port}/ui`
  }

  getAdminBaseURL() {
    return `http://${this.config.host}:${this.config.port}`
  }

  isRunning() {
    return this.process !== null && this.process.exitCode === null
  }

  writeBootstrapConfig() {
    const path = this.getConfigPath()
    mkdirSync(dirname(path), { recursive: true })
    const existing = this.readExistingConfig(path)
    const panelAccessToken = this.config.panelAccessToken?.trim() || existing.panelAccessToken || randomBytes(24).toString('base64url')
    const payload: BootstrapConfig = {
      host: this.config.host,
      port: this.config.port,
      panelAccessToken,
      databasePath: this.resolvePath(this.config.databasePath),
      logLevel: this.config.logLevel,
      httpTimeout: this.config.httpTimeout,
      secretKeyPath: this.config.secretKeyPath ? this.resolvePath(this.config.secretKeyPath) : undefined,
      webuiDir: this.config.webuiDir ? this.resolvePath(this.config.webuiDir) : undefined,
      enablePprof: this.config.enablePprof,
      maxBodyBytes: this.config.maxBodyBytes,
    }
    writeFileSync(path, JSON.stringify(payload, null, 2))
    this.lastConfigHash = this.buildRuntimeHash()
    return payload
  }

  async start() {
    if (!this.config.enabled) {
      this.ctx.logger.info('Elysia-API standalone backend is disabled')
      return
    }
    if (this.isRunning()) {
      this.ctx.logger.info('Elysia-API standalone backend is already running')
      return
    }
    this.writeBootstrapConfig()
    const binaryPath = this.resolveBinaryPath()
    this.ctx.logger.info(`Starting Elysia-API standalone backend: ${binaryPath}`)
    this.process = spawn(binaryPath, ['--config', this.getConfigPath()], {
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
      env: { ...process.env },
    })
    this.process.stdout?.on('data', data => this.ctx.logger.info(`[elysia-api] ${data.toString().trim()}`))
    this.process.stderr?.on('data', data => this.ctx.logger.warn(`[elysia-api] ${data.toString().trim()}`))
    this.process.on('exit', code => {
      this.ctx.logger.info(`Elysia-API standalone backend exited with code ${code}`)
      this.process = null
    })
    this.process.on('error', error => {
      this.ctx.logger.error(`Elysia-API standalone backend process error: ${error.message}`)
    })
  }

  async stop(timeoutMs = 5000) {
    const proc = this.process
    if (!proc) return
    // 已退出则直接清理
    if (proc.exitCode !== null || proc.signalCode !== null) {
      this.process = null
      return
    }
    await new Promise<void>(resolveExit => {
      let settled = false
      const finish = () => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        resolveExit()
      }
      // 等待进程真正退出（端口此时才会释放）再返回
      proc.once('exit', finish)
      // 兜底：超时未退出则强制结束，避免 restart 永久挂起
      const timer = setTimeout(() => {
        this.ctx.logger.warn(`Backend did not exit within ${timeoutMs}ms, forcing kill (pid=${proc.pid})`)
        proc.kill('SIGKILL')
      }, timeoutMs)
      proc.kill('SIGTERM')
    })
    this.process = null
  }

  async restart() {
    await this.stop()
    await this.start()
  }

  async reloadOrRestart(previousHash = this.lastConfigHash) {
    this.writeBootstrapConfig()
    const nextHash = this.buildRuntimeHash()
    const restartRequired = previousHash !== '' && previousHash !== nextHash
    if (restartRequired && this.config.restartOnConfigChange) {
      await this.restart()
      return 'restarted'
    }
    if (this.isRunning()) {
      await this.adminFetch('/api/admin/reload', { method: 'POST' }).catch(error => {
        this.ctx.logger.warn(`Backend reload request failed: ${(error as Error).message}`)
      })
    }
    return restartRequired ? 'restart-required' : 'reloaded'
  }

  async health() {
    return this.adminFetch('/api/admin/health')
  }

  private async adminFetch(path: string, init: RequestInit = {}) {
    const token = this.getPanelAccessToken()
    const response = await fetch(`${this.getAdminBaseURL()}${path}`, {
      ...init,
      headers: {
        ...(init.headers ?? {}),
        Authorization: `Bearer ${token}`,
      },
      signal: AbortSignal.timeout(5000),
    })
    const payload = await response.json().catch(() => ({}))
    if (!response.ok) {
      throw new Error(`${response.status} ${response.statusText}: ${JSON.stringify(payload)}`)
    }
    return payload
  }

  private getPanelAccessToken() {
    const configured = this.config.panelAccessToken?.trim()
    if (configured) return configured
    return this.readExistingConfig(this.getConfigPath()).panelAccessToken ?? ''
  }

  private readExistingConfig(path: string): Partial<BootstrapConfig> {
    if (!existsSync(path)) return {}
    try {
      return JSON.parse(readFileSync(path, 'utf8')) as Partial<BootstrapConfig>
    } catch {
      return {}
    }
  }

  private resolveBinaryPath() {
    if (this.config.backendBinaryMode === 'custom') {
      if (!this.config.backendBinaryPath) throw new Error('backendBinaryPath is required when backendBinaryMode=custom')
      return this.resolvePath(this.config.backendBinaryPath)
    }
    return join(__dirname, '../assets/bin', this.getBinaryName())
  }

  private getBinaryName() {
    if (process.platform === 'win32') return 'elysia-backend.exe'
    if (process.platform === 'darwin') return process.arch === 'arm64' ? 'elysia-backend-darwin-arm64' : 'elysia-backend-darwin-amd64'
    if (process.platform === 'linux') return 'elysia-backend-linux'
    return 'elysia-backend'
  }

  private resolvePath(path: string) {
    return isAbsolute(path) ? path : resolve(this.ctx.baseDir, path)
  }

  private buildRuntimeHash() {
    return JSON.stringify({
      binaryMode: this.config.backendBinaryMode,
      binaryPath: this.config.backendBinaryPath ?? '',
      configPath: this.getConfigPath(),
      host: this.config.host,
      port: this.config.port,
    })
  }
}
