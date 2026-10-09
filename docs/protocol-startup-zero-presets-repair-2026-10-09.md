# 零预置旧库升级启动修复

本次修复针对“旧库没有预置，替换二进制后四个名称出现，但运行时未就绪”的故障。保留工作区已有转换修复；编译器仍为 `2.0.0-dev.25`，本次启动修复不再提升协议语义版本。

故障已在隔离数据库复现：旧备份的摘要只包含八张表，新检查增加了 `protocol_history`、`protocol_revisions`，却用新算法核对没有版本标记的旧摘要。启动在预置恢复之前返回 `revision_conflict`，页面上的四个固定入口并不代表预置实际加载。缺表日志来自被打开的历史备份，不能据此判断当前数据库也缺表。

现在启动和管理员“重新加载”共用以下流程：结构准备 → 检查并恢复当前二进制的四个预置 → 必要旧数据迁移及绑定验证 → 加载并确认运行时就绪。零预置时校验新内置定义，不需要旧定义、旧报告或有效历史备份。空库不再向 `custom_protocols` 播种旧格式预置。

存储层根据实际数据库记录再次确认写入类型：

| 实际变更 | 行为 |
|---|---|
| 只补缺失修订、证据、草稿、激活 | 明确 INSERT，事务内核对配置及证据基线，不访问历史备份 |
| 覆盖已有草稿、激活、绑定或修复损坏预置行 | 先取得匹配变更前状态的当前快照，再事务提交 |
| 没有差异 | 复用有效记录，不增加激活代数、报告或快照 |
| 并发改变待提交状态 | 返回真实 `revision_conflict`，不覆盖新数据 |

旧 `protocol_engine_v2_backup` 记录和文件原样保留，启动不再读取或解释它们。新快照使用独立 settings 键 `protocol_current_snapshot_v2`，记录格式版本 2、文件路径、SHA-256、创建时间、配置及证据基线。文件通过 SQLite `VACUUM INTO` 创建，校验只读可访问性、完整性和基线后原子发布；备份不运行协议功能验证，损坏的旧定义也能原样保存。迁移／刷新回执记录实际快照，纯新增记录 `backupMode: not_required`。

预置补建不顺带改变已有绑定，也不伪造旧迁移完成。后续必要步骤失败时，已补建数据保留，整体生成仍受就绪门槛保护。快照错误、存储错误和实际并发冲突分别报告，不再把历史摘要不匹配解释成用户保存冲突。没有新增数据库表、页面或公开端点。

现有 `/api/admin/protocols` 返回 `runtimeReady`、实际 `loaded` 修订，以及可选的 `startupFailure: {stage, code, message}`。页面区分“内置，尚未创建”“已创建，等待验证或加载”“已加载”。整体就绪还要求必要迁移与绑定验证完成；`/health` 返回 200 不是协议验收结论。

## 回归证据

新增测试通过实际 `server.New` 构造入口，覆盖旧摘要备份缺少 `protocol_history`、历史文件缺失／损坏／不可作为文件读取、损坏元数据、首次失败后的管理端重载、恢复后连接四种模拟上游，以及十次无变更重启。存储回归覆盖损坏原文保存、只读快照、元数据写入失败、并发插入冲突、文件被篡改及缺失。原有预置缺陷、事务回滚、自定义数据和显式未绑定保护测试继续保留。

| 验收项 | 本地结果及证据 |
|---|---|
| Go 全量测试 | 通过；server 包 433.738 秒，日志为 `.tmp-dev/startup-final-full.log`；随后新增的恢复后生成用例另行定向通过 |
| 实际恢复后的发现与生成 | 四个预置通过模拟上游，校验生成响应格式；`.tmp-dev/startup-generation.log` |
| `go vet ./...`、前端类型检查 | 通过；`.tmp-dev/startup-final-vet.log`、`startup-final-types.log` |
| 关键 race | storage、server 通过；`.tmp-dev/startup-race.log` |
| 连接真实后端的 Playwright | 10 项通过，覆盖启动状态、四协议模型发现、协议编辑生命周期及转换策略；`.tmp-dev/startup-playwright.log` |
| 六目标构建 | Windows/Linux/macOS 的 amd64、arm64 均通过；其余五目标为交叉编译，实际执行环境为 Windows amd64 |
| Windows 原故障结构重放 | 零预置、未完成迁移、旧摘要备份；首次启动 `runtimeReady:true`，四个预置实际加载；`.tmp-dev/startup-windows-replay.log` |
| 历史版本升级 | 旧二进制 dev.23 建库，再升级 dev.25，含损坏预置恢复；`.tmp-dev/startup-windows-upgrade.log` |
| 十次进程重启 | 修订、草稿、激活、证据保持稳定；零预置重放库四类记录各 4 条，激活代数均为 1，旧备份元数据未改动，新快照数为 0 |

复测命令（仓库根目录；两个冒烟脚本均操作隔离临时库）：

```powershell
node scripts/smoke-zero-presets.mjs dist/standalone/elysia-api-windows-amd64.exe <零预置复现库>
node scripts/smoke-projection-upgrade.mjs <历史二进制> dist/standalone/elysia-api-windows-amd64.exe --preset-recovery
node scripts/test-protocol-e2e.mjs startup-upgrade.spec.ts protocol-v2.spec.ts conversion-policy.spec.ts
```

产物位于 `dist/standalone/`，本次为包含工作区修改的本地构建，没有提交、推送或部署。应用版本中的 Git 短哈希仍来自当前 HEAD，辨认本次产物应使用文件 SHA-256，而不是仅比较启动日志的版本文字。

**交付状态：本地启动恢复已验证，原环境待确认。** 未访问或修改用户实际数据库。原环境需确认四个预置的实际加载修订、`runtimeReady:true` 和实际调用成功后，才能关闭用户报告的故障。
