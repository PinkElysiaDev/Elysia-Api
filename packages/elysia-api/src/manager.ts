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
  httpTimeout: number
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

  /**
   * 后端已 daemon 化，与 Koishi 进程解耦：重启 Koishi 后 this.process 会丢失，
   * 但 daemon 仍在运行。因此存活判断以「端口健康探测」为准，而非进程句柄。
   */
  async isRunning(): Promise<boolean> {
    try {
      const response = await fetch(`${this.getAdminBaseURL()}/health`, {
        signal: AbortSignal.timeout(2000),
      })
      return response.ok || response.status === 401 // 401 也说明后端在监听
    } catch {
      return false
    }
  }

  writeBootstrapConfig() {
    const path = this.getConfigPath()
    mkdirSync(dirname(path), { recursive: true })
    const existing = this.readExistingConfig(path)
    const panelAccessToken = this.config.panelAccessToken?.trim() || existing.panelAccessToken || randomBytes(24).toString('base64url')
    // 合并而非覆写：保留后端通过 WebUI 写入的字段（enablePprof、databasePath 等），
    // 仅更新 Koishi 插件管控的 bootstrap 字段。
    const merged = {
      ...existing,
      host: this.config.host,
      port: this.config.port,
      panelAccessToken,
      httpTimeout: this.config.httpTimeout,
    }
    writeFileSync(path, JSON.stringify(merged, null, 2))
    this.lastConfigHash = this.buildRuntimeHash()
    return merged
  }

  async start() {
    if (!this.config.enabled) {
      this.ctx.logger.info('Elysia-API standalone backend is disabled')
      return
    }
    // 后端已 daemon 化：若已有实例在监听端口，则不接管、不重启（独立存活）。
    if (await this.isRunning()) {
      this.ctx.logger.info('Elysia-API standalone backend already running, leaving it as-is')
      return
    }
    this.writeBootstrapConfig()
    const binaryPath = this.resolveBinaryPath()
    this.ctx.logger.info(`Starting Elysia-API standalone backend (daemon): ${binaryPath}`)
    // detached + unref + stdio:ignore：子进程脱离 Koishi 进程组，
    // Koishi 退出/重启不再连坐杀掉后端。日志由后端自身处理，不再回流 pipe。
    const child = spawn(binaryPath, ['--config', this.getConfigPath()], {
      stdio: 'ignore',
      windowsHide: true,
      detached: true,
      env: { ...process.env },
    })
    child.on('error', error => {
      this.ctx.logger.error(`Elysia-API standalone backend spawn error: ${error.message}`)
    })
    child.unref()
    this.process = child
  }

  /**
   * 停止 daemon：通过 /__shutdown 端点请求后端优雅关停（loopbackOnly，仅本机可调），
   * 再轮询 /health 直到不再响应。不再用 process.kill —— daemon 可能不由本会话持有句柄。
   */
  async stop(timeoutMs = 10000) {
    if (!(await this.isRunning())) {
      this.process = null
      return
    }
    try {
      await fetch(`${this.getAdminBaseURL()}/__shutdown`, {
        method: 'POST',
        signal: AbortSignal.timeout(3000),
      })
    } catch (error) {
      // 关停请求本身可能因连接被切断而抛错，属正常；继续轮询确认。
      this.ctx.logger.debug(`shutdown request returned: ${(error as Error).message}`)
    }
    const stopped = await this.waitForStopped(timeoutMs)
    if (!stopped) {
      this.ctx.logger.warn(`Backend did not stop within ${timeoutMs}ms after /__shutdown`)
    }
    this.process = null
  }

  /** 轮询直到后端端口不再响应（已关停），或超时。 */
  private async waitForStopped(timeoutMs: number): Promise<boolean> {
    const deadline = Date.now() + timeoutMs
    while (Date.now() < deadline) {
      if (!(await this.isRunning())) return true
      await new Promise(r => setTimeout(r, 200))
    }
    return !(await this.isRunning())
  }

  async restart() {
    this.writeBootstrapConfig()
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
    if (await this.isRunning()) {
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
