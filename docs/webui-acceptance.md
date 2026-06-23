# WebUI Acceptance Checklist

## 基础运行

- [ ] 不安装任何外部启动器，直接运行后端，WebUI 可以登录并完成全部配置。
- [ ] 默认 `config.json` 旁边生成 SQLite 数据库与主密钥文件。
- [ ] 内嵌 WebUI 在 `/ui/` 可访问。
- [ ] 修改 `logLevel` 与 `httpTimeout` 后可热重载生效。
- [ ] 修改 `host`、`port`、`databasePath` 或 `enablePprof` 后明确提示需要重启。

## 模型源覆盖

- [ ] OpenAI source 可自动拉取模型。
- [ ] OpenAI-compatible source 可自动拉取模型。
- [ ] Claude source 拉取失败时可看到错误提示并写入系统日志。
- [ ] Gemini source 可自动拉取模型。
- [ ] 手动 source 可新增多个模型并进入模型缓存。
- [ ] 禁用 source 后不会参与刷新与选择。

## 模型组覆盖

- [ ] 可创建 LLM group。
- [ ] 可配置 round-robin、sequential、random。
- [ ] 可配置 maxRetries、retryInterval。
- [ ] 可配置 maxConcurrency、dailyLimitMaxRequests、dailyLimitMaxTokens。
- [ ] `/v1/models` 和 `/v1beta/models` 能返回启用的 group。
- [ ] 请求 group name 能转发到组内模型。

## Token 与安全

- [ ] Relay API Token 可创建、禁用、删除。
- [ ] 未授权访问 `/api/admin/*` 返回 401。
- [ ] 未授权访问 `/v1/*` 返回 401。
- [ ] token 和 API key 不在列表、日志、usage 明文泄漏。
- [ ] SQLite 敏感字段在配置主密钥后以密文保存。

## Usage 与日志

- [ ] streaming 和 non-streaming 请求都会写入 usage。
- [ ] usage logs 支持分页和过滤。
- [ ] usage stats 与日志范围一致。
- [ ] reset usage 后统计清零。
- [ ] 系统日志页能显示刷新模型、错误等事件。

## 独立发行

- [ ] `yarn build` 生成 `dist/standalone`。
- [ ] 仓库根目录保留 `config.json.example`，发行目录不复制配置模板。
- [ ] Windows、Linux、macOS 二进制均嵌入最新 WebUI。
- [ ] 发行目录不包含本地 SQLite、WAL、主密钥或运行时 config。
