import { Context } from 'koishi'
import { spawn, ChildProcess } from 'child_process'
import { writeFileSync, mkdirSync, existsSync } from 'fs'
import { join, dirname } from 'path'
import { ModelGroupConfig, ServerConfig, AccessToken, Capability } from './config'
import { Model } from '@elysia-api/shared'

interface BackendConfig {
  server: { host: string; port: number }
  tokens: Array<{ token: string; name: string; enabled: boolean }>
  heartbeatTimeout?: number  // 心跳超时时间（秒）
  httpTimeout?: number  // HTTP 请求超时时间（秒），0 为不限制
  debugMode?: boolean     // 调试模式
  verboseLog?: boolean    // 详细日志模式
  modelGroups: Array<{
    id: string
    name: string
    enabled: boolean
    models: Array<{
      id: string
      name: string
      baseUrl: string
      apiKey: string
      platform: string
    }>
    strategy: string
    maxRetries: number
    retryInterval: number
    maxConcurrency?: number
    dailyLimitMaxRequests?: number
    dailyLimitMaxTokens?: number
    type: string
    maxTokens?: number
    visionCapable?: boolean
    toolsCapable?: boolean
    structuredOutput?: boolean
    thinkingMode?: string
  }>
}

export class BackendManager {
  private process: ChildProcess | null = null
  private configPath: string
  private heartbeatInterval: NodeJS.Timeout | null = null
  private heartbeatUrl: string
  private heartbeatTimeoutSec: number  // 后端心跳超时时间（秒）

  constructor(
    private ctx: Context,
    private serverConfig: ServerConfig,
    private tokens: Record<string, AccessToken>,
    private modelGroups: ModelGroupConfig[],
    private heartbeatIntervalSec: number = 60,  // 心跳发送间隔（秒）
    heartbeatTimeout?: number,  // 后端心跳超时时间（秒）
    private httpTimeout: number = 120,  // HTTP 请求超时时间（秒），0 为不限制
    private debugMode: boolean = false,  // 调试模式
    private verboseLog: boolean = false,  // 详细日志模式
  ) {
    this.configPath = join(ctx.baseDir, 'data/elysia-api/config.json')
    this.heartbeatUrl = `http://${serverConfig.host}:${serverConfig.port}/__heartbeat`
    this.heartbeatTimeoutSec = heartbeatTimeout ?? 300  // 默认 300 秒
  }

  /**
   * 根据当前平台获取正确的二进制文件名
   */
  private getBinaryName(): string {
    const platform = process.platform
    const arch = process.arch

    this.ctx.logger.info(`Platform: ${platform}, Architecture: ${arch}`)

    switch (platform) {
      case 'win32':
        return 'elysia-backend.exe'
      case 'linux':
        return 'elysia-backend-linux'
      case 'darwin':
        // macOS 需要根据架构选择
        if (arch === 'arm64') {
          return 'elysia-backend-darwin-arm64'
        } else {
          return 'elysia-backend-darwin-amd64'
        }
      default:
        this.ctx.logger.warn(`Unknown platform: ${platform}, falling back to default binary name`)
        return 'elysia-backend'
    }
  }

  async start(): Promise<void> {
    // 检查是否已在运行
    if (this.isRunning()) {
      this.ctx.logger.info('Backend is already running')
      this.startHeartbeat()
      return
    }

    // Ensure config exists
    this.writeConfig()

    const binaryName = this.getBinaryName()

    // 详细日志：输出路径信息
    if (this.verboseLog) {
      this.ctx.logger.info(`[VERBOSE] ctx.baseDir = ${this.ctx.baseDir}`)
      this.ctx.logger.info(`[VERBOSE] __dirname (plugin lib) = ${__dirname}`)
      this.ctx.logger.info(`[VERBOSE] binaryName = ${binaryName}`)
    }

    // 生成候选路径，按优先级查找
    const candidates: string[] = []

    // 候选 0: 打包在插件内的二进制文件
    // 发布到 npm 后，二进制文件位于 assets/bin/ 目录
    candidates.push(join(__dirname, '../assets/bin', binaryName))

    // 详细日志：输出候选路径
    if (this.verboseLog) {
      this.ctx.logger.info(`[VERBOSE] Searching for backend binary in ${candidates.length} candidate path...`)
      this.ctx.logger.info(`[VERBOSE] Candidate: ${candidates[0]}`)
    }

    // 查找第一个存在的路径
    let binaryPath: string | null = null
    for (const candidate of candidates) {
      if (this.verboseLog) {
        this.ctx.logger.info(`[VERBOSE] Trying: ${candidate}`)
      }
      if (existsSync(candidate)) {
        binaryPath = candidate
        break
      }
    }

    // 详细日志：输出最终选择的路径
    if (this.verboseLog) {
      this.ctx.logger.info(`[VERBOSE] Selected path: ${binaryPath || 'none'}`)
    }

    // Check if binary exists
    if (!binaryPath) {
      this.ctx.logger.warn(`Backend binary not found! Tried ${candidates.length} locations:`)
      candidates.forEach(p => this.ctx.logger.info(`  - ${p}`))
      this.ctx.logger.info(`Please build the backend for your platform, or configure the correct path.`)
      this.ctx.logger.info(`Expected binary: ${binaryName}`)
      this.ctx.logger.info(`Available platforms: Windows (.exe), Linux (-linux), macOS AMD64 (-macos-amd64), macOS ARM64 (-macos-arm64)`)
      throw new Error(`Backend binary "${binaryName}" not found in any of the candidate locations`)
    }

    this.ctx.logger.info(`Starting backend with binary: ${binaryPath}`)

    // 详细日志：输出配置文件路径和 PID
    if (this.verboseLog) {
      this.ctx.logger.info(`[VERBOSE] Backend config path: ${this.configPath}`)
    }

    this.process = spawn(binaryPath, ['--config', this.configPath], {
      stdio: ['ignore', 'pipe', 'pipe'],
    })

    if (this.verboseLog) {
      this.ctx.logger.info(`[VERBOSE] Backend process spawned with PID: ${this.process.pid}`)
    }

    this.pipeLogs()
    this.startHeartbeat()

    this.process.on('exit', (code) => {
      this.ctx.logger.info(`Backend exited with code ${code}`)
      this.process = null
      this.stopHeartbeat()
    })

    this.process.on('error', (err) => {
      this.ctx.logger.error(`Backend process error: ${err.message}`)
    })
  }

  async stop(): Promise<void> {
    this.stopHeartbeat()
    if (this.process) {
      this.process.kill('SIGTERM')
      this.process = null
    }
  }

  async reloadConfig(): Promise<void> {
    // 只写入配置文件，后端通过文件监控自动重载
    this.writeConfig()
    this.ctx.logger.info('Config file updated, backend will reload automatically')
  }

  private startHeartbeat() {
    if (this.heartbeatInterval) return

    // 每 heartbeatIntervalSec 秒发送一次心跳
    this.heartbeatInterval = setInterval(async () => {
      try {
        const response = await fetch(this.heartbeatUrl, {
          method: 'GET',
          signal: AbortSignal.timeout(5000),
        })
        if (response.ok) {
          const status = await response.json()
          this.ctx.logger.debug(`Backend heartbeat: ${status.seconds_since}s since last`)
        }
      } catch (err) {
        this.ctx.logger.warn(`Backend heartbeat failed: ${(err as Error).message}`)
        // 心跳失败可能表示后端已停止
        if (this.process && this.process.exitCode !== null) {
          this.process = null
          this.stopHeartbeat()
        }
      }
    }, this.heartbeatIntervalSec * 1000)

    this.ctx.logger.info('Heartbeat started')
  }

  private stopHeartbeat() {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval)
      this.heartbeatInterval = null
    }
  }

  /**
   * 将 capabilities 数组转换为对应的布尔字段
   */
  private capabilitiesToBooleans(capabilities?: Capability[]) {
    if (!capabilities) return {}
    return {
      visionCapable: capabilities.includes('visionCapable' as Capability),
      toolsCapable: capabilities.includes('toolsCapable' as Capability),
      structuredOutput: capabilities.includes('structuredOutput' as Capability),
    }
  }

  writeConfig() {
    // Ensure directory exists
    if (!existsSync(dirname(this.configPath))) {
      mkdirSync(dirname(this.configPath), { recursive: true })
    }

    // Get models from aggregator service (通过 inject 注入的服务)
    const models = this.ctx['elysia-api-aggregator']?.getAll() ?? []
    const modelMap = new Map(models.map(m => [m.id, m]))

    if (this.debugMode || this.verboseLog) {
      this.ctx.logger.info(`[writeConfig] Aggregator 提供了 ${models.length} 个模型`)
      for (const m of models) {
        this.ctx.logger.info(`[writeConfig] 可用模型: id="${m.id}", name="${m.name}"`)
      }
    }

    // 将 tokens dict 转换为数组（供后端使用）
    const tokensArray = Object.entries(this.tokens).map(([name, token]) => ({
      name,
      token: token.token,
      enabled: token.enabled,
    }))

    const backendConfig: BackendConfig = {
      server: this.serverConfig,
      tokens: tokensArray,
      heartbeatTimeout: this.heartbeatTimeoutSec,
      httpTimeout: this.httpTimeout,
      debugMode: this.debugMode,
      verboseLog: this.verboseLog,
      modelGroups: this.modelGroups
        .filter(g => g.enabled)
        .map(group => {
          // models 现在是 string[]（模型 ID 数组）
          // 添加防御性检查：如果 models 未定义，使用空数组
          const configuredModelIds = group.models || []

          if (this.debugMode || this.verboseLog) {
            this.ctx.logger.info(`[writeConfig] 模型组 "${group.name}" 配置了 ${configuredModelIds.length} 个模型`)
            this.ctx.logger.info(`[writeConfig] 配置的模型 ID: ${JSON.stringify(configuredModelIds)}`)
          }

          const groupModels = configuredModelIds
            .map(modelId => {
              const model = modelMap.get(modelId)
              if (this.debugMode && !model) {
                this.ctx.logger.warn(`[writeConfig] 模型 ID "${modelId}" 在 aggregator 中未找到`)
              }
              return model
            })
            .filter((m): m is Model => m !== undefined)
            .map(m => ({
              id: m.id,
              name: m.name,
              baseUrl: m.baseUrl,
              apiKey: m.apiKey,
              platform: m.platform,
            }))

          if (this.debugMode || this.verboseLog) {
            this.ctx.logger.info(`[writeConfig] 模型组 "${group.name}" 匹配到 ${groupModels.length} 个有效模型`)
          }

          // 将 capabilities 转换为布尔字段
          const capabilityBooleans = this.capabilitiesToBooleans(group.capabilities)

          return {
            id: group.id,
            name: group.name,
            enabled: group.enabled,
            models: groupModels,
            strategy: group.strategy,
            maxRetries: group.maxRetries,
            retryInterval: group.retryInterval,
            maxConcurrency: group.enableRateLimit ? group.maxConcurrency : undefined,
            dailyLimitMaxRequests: group.enableRateLimit ? group.dailyLimitMaxRequests : undefined,
            dailyLimitMaxTokens: group.enableRateLimit ? group.dailyLimitMaxTokens : undefined,
            type: group.type ?? 'llm',
            maxTokens: group.maxTokens,
            ...capabilityBooleans,
            thinkingMode: group.thinkingMode,
          }
        })
        .filter(g => g.models.length > 0),
    }

    writeFileSync(this.configPath, JSON.stringify(backendConfig, null, 2))
    this.ctx.logger.info(`Backend config written to ${this.configPath}`)
  }

  private pipeLogs() {
    this.process?.stdout?.on('data', (data) => {
      this.ctx.logger.info(`[backend] ${data.toString().trim()}`)
    })

    this.process?.stderr?.on('data', (data) => {
      this.ctx.logger.warn(`[backend] ${data.toString().trim()}`)
    })
  }

  isRunning(): boolean {
    return this.process !== null && this.process.exitCode === null
  }
}
