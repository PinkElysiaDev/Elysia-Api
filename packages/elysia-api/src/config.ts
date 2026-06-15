import { Schema } from 'koishi'

export interface Config {
  enabled: boolean
  backendBinaryMode: 'bundled' | 'custom'
  backendBinaryPath?: string
  configPath: string
  host: string
  port: number
  panelAccessToken?: string
  httpTimeout: number
  autoStart: boolean
  restartOnConfigChange: boolean
  webuiOpenCommand?: string
}

export const name = 'elysia-api'

export const Config: Schema<Config> = Schema.intersect([
  Schema.object({
    enabled: Schema.boolean().default(true).description('启用独立 Elysia-API 后端入口插件'),
  }).description('基础'),
  // 后端二进制来源：tagged union，仅当选择「自定义」时才显示路径输入项。
  Schema.intersect([
    Schema.object({
      backendBinaryMode: Schema.union([
        Schema.const('bundled' as const).description('使用插件内置后端二进制'),
        Schema.const('custom' as const).description('使用自定义后端二进制路径'),
      ]).default('bundled' as const).description('后端二进制来源'),
    }).description('后端程序'),
    Schema.union([
      Schema.object({
        backendBinaryMode: Schema.const('custom' as const).required(),
        backendBinaryPath: Schema.string().description('自定义后端二进制路径'),
      }),
      Schema.object({}),
    ]),
  ]),
  Schema.object({
    configPath: Schema.string().default('data/elysia-api-standalone/config.json').description('独立后端 bootstrap config.json 路径'),
    host: Schema.string().default('127.0.0.1').description('后端监听地址'),
    port: Schema.number().default(18765).description('后端监听端口'),
    panelAccessToken: Schema.string().role('secret').description('WebUI / 管理 API 访问令牌；留空时插件启动时自动生成写入 bootstrap config'),
    httpTimeout: Schema.number().default(120).description('后端上游 HTTP 超时秒数，0 表示不限制'),
  }).description('后端启动配置'),
  Schema.object({
    autoStart: Schema.boolean().default(true).description('Koishi ready 后自动启动后端'),
    restartOnConfigChange: Schema.boolean().default(true).description('host/port/configPath/binary 变化时自动重启，否则仅写入配置'),
    webuiOpenCommand: Schema.string().description('可选打开 WebUI 的命令，例如 xdg-open/open/start；留空时只返回 URL'),
  }).description('进程管理'),
]) as Schema<Config>
