import { Context } from 'koishi'
import { spawn } from 'child_process'
import { Config, name } from './config'
import { StandaloneBackendManager } from './manager'

export { Config, name }

export const usage = `---

Elysia-API 独立后端入口插件。

本插件只负责 bootstrap config、后端进程和 WebUI 入口，不聚合模型、不配置模型组、不依赖 aggregator/orchestrator。
旧 aggregator/orchestrator 可以继续并行运行；默认配置目录为 data/elysia-api-standalone，避免覆盖旧 orchestrator 的 data/elysia-api/config.json。

### 命令

- elysia-api.standalone.backend.start：启动独立后端
- elysia-api.standalone.backend.stop：停止独立后端
- elysia-api.standalone.backend.restart：重启独立后端
- elysia-api.standalone.backend.status：查询后端状态
- elysia-api.standalone.backend.reload：写入 bootstrap config 并热重载/重启
- elysia-api.standalone.webui.url：显示 WebUI 地址
- elysia-api.standalone.webui.open：按配置命令打开 WebUI

---
`

export function apply(ctx: Context, config: Config) {
  const manager = new StandaloneBackendManager(ctx, config)

  ctx.on('ready', () => {
    if (config.autoStart) void manager.start()
  })

  ctx.on('dispose', () => {
    void manager.stop()
  })

  ctx.on('config', () => {
    manager.updateConfig(config)
    void manager.reloadOrRestart()
  })

  ctx.command('elysia-api.standalone.backend.start', '启动 Elysia-API 独立后端').action(async () => {
    await manager.start()
    return `Elysia-API 独立后端启动中：${manager.getAdminBaseURL()}`
  })

  ctx.command('elysia-api.standalone.backend.stop', '停止 Elysia-API 独立后端').action(async () => {
    await manager.stop()
    return 'Elysia-API 独立后端已停止'
  })

  ctx.command('elysia-api.standalone.backend.restart', '重启 Elysia-API 独立后端').action(async () => {
    await manager.restart()
    return `Elysia-API 独立后端已重启：${manager.getAdminBaseURL()}`
  })

  ctx.command('elysia-api.standalone.backend.reload', '写入 bootstrap config 并请求后端重载').action(async () => {
    const result = await manager.reloadOrRestart()
    return `Elysia-API 独立后端配置已处理：${result}`
  })

  ctx.command('elysia-api.standalone.backend.status', '查询 Elysia-API 独立后端状态').action(async () => {
    if (!manager.isRunning()) return 'Elysia-API 独立后端进程未由本插件启动'
    try {
      const health = await manager.health()
      return `Elysia-API 独立后端运行中：${JSON.stringify(health)}`
    } catch (error) {
      return `Elysia-API 独立后端进程存在，但健康检查失败：${(error as Error).message}`
    }
  })

  ctx.command('elysia-api.standalone.webui.url', '显示 Elysia-API WebUI 地址').action(() => manager.getWebUIURL())

  ctx.command('elysia-api.standalone.webui.open', '打开 Elysia-API WebUI').action(() => {
    const url = manager.getWebUIURL()
    if (!config.webuiOpenCommand?.trim()) return url
    const child = spawn(config.webuiOpenCommand, [url], { stdio: 'ignore', detached: true, windowsHide: true })
    child.unref()
    return `已请求打开 WebUI：${url}`
  })
}
