import { Context } from 'koishi'
import { spawn } from 'child_process'
import { Config, name } from './config'
import { StandaloneBackendManager } from './manager'

export { Config, name }

export const usage = `---

Elysia-API 独立后端入口插件。

本插件只负责调整后端的启动时行为，可绕过本插件手动控制后端。

### 命令

- elysia-api.backend.start：启动独立后端
- elysia-api.backend.stop：停止独立后端
- elysia-api.backend.restart：重启独立后端
- elysia-api.backend.status：查询后端状态
- elysia-api.backend.reload：写入 bootstrap config 并热重载/重启
- elysia-api.webui.url：显示 WebUI 地址
- elysia-api.webui.open：按配置命令打开 WebUI

---
`

export function apply(ctx: Context, config: Config) {
  const manager = new StandaloneBackendManager(ctx, config)

  ctx.on('ready', () => {
    if (config.autoStart) void manager.start()
  })

  // 后端已 daemon 化，独立于 Koishi 存活：dispose 时不再停止后端，
  // 仅放手即可。重启/停止后端请用对应指令或改配置触发。
  ctx.on('dispose', () => {})

  ctx.on('config', () => {
    manager.updateConfig(config)
    void manager.reloadOrRestart()
  })

  ctx.command('elysia-api', 'Elysia-API 管理指令', { authority: 3 })

  ctx.command('elysia-api.backend.start', '启动 Elysia-API 独立后端').action(async () => {
    await manager.start()
    return `Elysia-API 独立后端启动中：${manager.getAdminBaseURL()}`
  })

  ctx.command('elysia-api.backend.stop', '停止 Elysia-API 独立后端').action(async () => {
    await manager.stop()
    return 'Elysia-API 独立后端已停止'
  })

  ctx.command('elysia-api.backend.restart', '重启 Elysia-API 独立后端').action(async () => {
    await manager.restart()
    return `Elysia-API 独立后端已重启：${manager.getAdminBaseURL()}`
  })

  ctx.command('elysia-api.backend.reload', '写入 bootstrap config 并请求后端重载').action(async () => {
    const result = await manager.reloadOrRestart()
    return `Elysia-API 独立后端配置已处理：${result}`
  })

  ctx.command('elysia-api.backend.status', '查询 Elysia-API 独立后端状态').action(async () => {
    if (!(await manager.isRunning())) return 'Elysia-API 独立后端未在运行'
    try {
      const health = await manager.health()
      return `Elysia-API 独立后端运行中：${JSON.stringify(health)}`
    } catch (error) {
      return `Elysia-API 独立后端进程存在，但健康检查失败：${(error as Error).message}`
    }
  })

  ctx.command('elysia-api.webui.url', '显示 Elysia-API WebUI 地址').action(() => manager.getWebUIURL())

  ctx.command('elysia-api.webui.open', '打开 Elysia-API WebUI').action(() => {
    const url = manager.getWebUIURL()
    if (!config.webuiOpenCommand?.trim()) return url
    const child = spawn(config.webuiOpenCommand, [url], { stdio: 'ignore', detached: true, windowsHide: true })
    child.unref()
    return `已请求打开 WebUI：${url}`
  })
}
