# 自动测试与报错收集

填齐 Chat、Responses、Anthropic、Gemini 四种真实渠道后，自动启动独立的 Elysia 测试实例，检查日常使用、协议转换、SDK 兼容和代码，并保存详细日志。默认运行全部六组，也能单独重跑一组。

## 开始使用

将整个 `protocol-audit` 文件夹放在 Elysia-Api 的 `scripts/` 下。需要 Node.js 22+、Go 1.25+ 和已安装项目依赖的源码。以下命令在**项目根目录**执行。

**1. 首次使用，复制示例配置：**

```sh
cp scripts/protocol-audit/config.example.json scripts/protocol-audit/config.local.json
```

**2. 编辑 `scripts/protocol-audit/config.local.json`：**

- 按[示例配置](config.example.json)，填齐四个渠道的 `baseUrl`、`apiKey`、`model`，替换全部示例地址和 `YOUR_...` 占位符。
- `baseUrl` 可带 `/v1`、`/v1beta` 或代理前缀，不要填完整推理端点（如 `/v1/chat/completions`）。Gemini 模型名不加 `models/`。
- 已有配置可直接复用；`config.local.json` 含真实密钥，请注意隐私安全。

**3. 首次安装测试依赖：**

```sh
npm install --prefix scripts/protocol-audit
```

**4. 运行测试：**

```sh
node scripts/protocol-audit/run.mjs
```

测试实例自动启动、关闭，临时数据库自动清理；一项失败后继续收集其他结果。测试会调用真实渠道并产生费用。结束后打开终端显示的 `report.md`。

**默认并发为 32**，在配置中修改 `"concurrency": 32` 即可调整，允许任意正整数，`1` 为串行。协议和 SDK 的独立用例跨渠道并行；同一对话的前后轮次、重启和渠道启停等操作保持顺序。并发较高时需留意渠道限流。

## 只运行一组

```sh
node scripts/protocol-audit/run.mjs --group protocol
```

| 分组 | 检查内容 |
|---|---|
| `daily` | 渠道和令牌、对话、模型列表、用量日志、会话隔离、流式响应 |
| `protocol` | 上游直连与 4×4 协议转换，包括多轮、系统指令、中文、历史和工具调用 |
| `sdk` | OpenAI、Anthropic、Google SDK 和 AI SDK 的实际调用 |
| `errors` | 错误令牌、无效模型、非法 JSON、取消请求及恢复 |
| `persistence` | 重启、修改配置、渠道启停、撤销令牌 |
| `code` | 后端测试和 go vet；前端类型检查、ESLint 和构建 |

不加 `--group` 就是全部。除单独的 `code` 组外，每个分组也都要求四种渠道配齐。仅 `code` 组不需要渠道配置；不运行 `sdk` 组就无需安装脚本的 SDK 依赖。代码检查仍需要项目自身的依赖。

## 暂停真实渠道时的本地 SDK 回放

以下 PowerShell 命令使用 Go 网关测试的模拟上游，保存网关实际生成的 16 个方向 JSON/SSE 输出，再由固定版本 SDK 完整读取。不会读取 `config.local.json`，不会请求真实渠道；此结果也不代表原环境验收。

```powershell
npm ci --prefix scripts/protocol-audit
$env:ELYSIA_AUDIT_CAPTURE = Join-Path (Get-Location).Path '.tmp-dev/audit-wire'
Push-Location backend
go test ./server -run '^TestGatewayAuditTextMatrix$' -count=1
Pop-Location
node scripts/protocol-audit/replay.mjs .tmp-dev/audit-wire
```

预期为 32 份响应、56 次 SDK 消费全部通过。回放不会修改正文或 SSE 字段；缺文件、SDK 格式错误、隐藏 error 事件和未完成流均视为失败。该命令只用于上述合成测试输出，不能代替真实签名、真实工具第二轮或插件测试。

异常流另行验证，不能把任意 SDK 报错当作预期错误已送达：

```powershell
Push-Location backend
go test ./server -run '^TestGatewayResponsesNativeFailureSequence$' -count=1
Pop-Location
node scripts/protocol-audit/replay-errors.mjs .tmp-dev/audit-wire
```

沿用上面的 `ELYSIA_AUDIT_CAPTURE`，覆盖截断、非法终止快照、上游嵌套及平铺错误四种场景。OpenAI 与 Vercel Responses SDK 分别读取实际网关输出；每个 SDK 必须保留错误前的正文、报告一次包含原始原因的错误，并且只发送一次请求。普通成功结束、无关格式异常均不能算通过。HTTP 200 表示流头已经发送，持久调用记录及 trailer 仍须表明失败。

Chat 的缺失角色及身份变化另用同一错误回放器检查：

```powershell
Push-Location backend
go test ./server -run '^TestGatewayChatWireContractFailure$' -count=1
Pop-Location
node scripts/protocol-audit/replay-errors.mjs .tmp-dev/audit-wire chat
```

四种场景分别是整轮缺少 assistant 角色、ID 变化、模型变化及创建时间变化。两个 SDK 应读到包含正文的分片，并得到网关指出的具体字段错误，不能以 SDK 自身的 `missing role` 或类型校验异常替代网关诊断。固定版本 OpenAI 高层 helper 在收到角色之前不会触发 `content` 回调；报告单独记录这一限制，不把收到分片等同于客户端已展示文字。

## 查看结果

每次结果保存在 `scripts/protocol-audit/results/` 下的新目录，终端会显示完整路径。

- **看结果：** 打开 `report.md`，点击证据链接查看具体请求、响应和报错。
- **看进度：** 运行过程中也能查看 `run.log`。
- **反馈问题：** 打包本次结果目录。日志会脱敏已知密钥，但分享前仍需检查其他私有内容，不要加入配置文件。

Ctrl+C 会保留已收集的结果并关闭临时实例。HTTP 200 也可能因响应结构或内容错误而未通过；结合直连结果判断是渠道问题还是网关转换问题。

可用性、重启和异常恢复检查接受结构正常的非空回答；指定文字、记忆码和工具结果仍由相应场景严格检查。协议组的汇总项不重复计入通过或失败数量。

---

## 高级用法

### 目录与分发

```text
scripts/protocol-audit/
  run.mjs                 运行入口
  README.md               使用说明
  config.example.json     可公开的四协议配置模板
  config.local.json       用户填写的配置（不上传）
  package.json            依赖和自测命令
  package-lock.json       固定依赖版本
  .gitignore              排除本地配置、依赖和结果
  src/                    测试执行代码
  tests/                  脚本自测及固定样例
  node_modules/           安装的依赖（不上传）
  results/                每次运行的报告和日志（不上传）
```

分发包统一放在 `scripts/protocol-audit.zip`，包内保留 `protocol-audit/` 这一层目录。只包含入口、说明、示例配置、依赖清单、`.gitignore`、`src/` 和 `tests/`；不包含用户配置、依赖、测试结果和系统杂项。

### 使用环境变量

在对应渠道中，将 `"apiKey": "你的密钥"` 替换为 `"apiKeyEnv": "AUDIT_CHAT_KEY"`，然后运行：

```sh
export AUDIT_CHAT_KEY='你的密钥'
node scripts/protocol-audit/run.mjs
```

其他渠道同理，`apiKey` 和 `apiKeyEnv` 二选一。脚本会在测试前检查变量是否已设置。

### 命令选项和独立使用

```sh
# 只校验配置和查看计划，不要求设置密钥环境变量，不运行命令或发请求
node scripts/protocol-audit/run.mjs --dry-run

# 指定配置或新的输出目录
node scripts/protocol-audit/run.mjs --config /path/to/config.json --out /path/to/new-result
```

输出目录必须不存在。退出码：`0` 通过，`1` 有失败，`2` 配置错误或未完成，`130` 中断。

复制整个脚本目录到项目外也能使用：在配置中增加 `"projectRoot": "/path/to/Elysia-Api"`，在脚本目录安装依赖并运行 `node run.mjs`。相对 `projectRoot` 按配置文件目录解析；省略时自动查找源码仓库。

### 可选配置

以下字段均可省略：

| 字段 | 默认值 / 含义 |
|---|---|
| `concurrency` | `32`，所有渠道共用的协议/SDK 并发数；正整数，无人为上限，`1` 为串行 |
| `projectRoot` | 自动查找源码目录 |
| 渠道的 `id` | 自动生成；填写可让报告名称更容易辨认 |
| `timeoutMs` | `180000`（3 分钟），单次 HTTP 请求总超时；SDK 和临时后端同步使用 |
| `maxRequests` | `512`，所有渠道的 **protocol 组**共享请求上限；其他组另有调用 |
| `maxResponseBytes` | `16777216`（16 MiB），单次 HTTP 响应或代码日志上限；超限会失败并保存已收集的部分 |
| `maxOutputTokens` | `32768`，单次生成预算；普通 HTTP、SDK 和工具后续轮次统一使用 |
| `requireUsage` | `true`，协议与日常调用要求返回用量计数；Gemini 允许省略可选分项，已返回的计数仍须有效 |
| `scenarios` | `models`, `text`, `multiturn`, `tools`, `tools_auto`, `system`, `chinese`, `history`；影响 protocol 组 |
| `streams` | `[false, true]`，protocol 和 sdk 组分别跑 JSON 与 SSE |
| `codeChecks` | 省略使用内置代码检查；非空数组可替换 |

没有密钥的服务可用 `"auth": "none"`。渠道使用各协议的标准密钥认证，暂不支持自定义认证请求头。

图片为可选场景：将 `image` 加入 `scenarios`，在支持图片的渠道上设置 `"vision": true`。脚本发送内置红色 PNG；未声明图片能力的路径标记不适用。

`tools` 强制指定 `audit_echo`，`tools_auto` 使用自动选择并通过提示要求调用同一个工具。两者分别记录，均要求真实工具调用及第二轮结果标记；自动模式不调用工具时仍算失败。若渠道在思考模式下拒绝强制选择，保留 `tools` 的直连失败证据，另运行 `tools_auto` 验证工具往返，不能将前者改记为通过。

默认 protocol 组每条路径 13 个用例、最多 17 次请求；四种上游的直连加 4×4 网关路径共 260 个用例、最多 340 次请求，其中 320 次为模型生成。完整运行全部六组约有 440 次模型生成，另有模型列表和管理请求；失败可能减少后续调用。脚本不自动重试；请求预算不约束网关内部行为或实际费用。

默认 **32,768 tokens** 用于给推理和输出预留空间，是可调整的测试预算，不是供应商的统一默认值。预算较大可能增加费用，达到上限被截断仍算失败。依据 2026-10-09 核对的[OpenAI 推理指南](https://developers.openai.com/api/docs/guides/reasoning)、[GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol)、[Gemini](https://ai.google.dev/gemini-api/docs/latest-model) 和 [Claude](https://platform.claude.com/docs/en/models/overview) 官方说明；具体预算和能力需按所填模型确认。

### 自定义代码检查

`codeChecks` 替换内置检查，`cwd` 相对于 `projectRoot`：

```json
{
  "codeChecks": [
    {
      "id": "backend-tests",
      "command": ["go", "test", "./...", "-count=1"],
      "cwd": "backend",
      "timeoutMs": 600000
    }
  ]
}
```

命令按参数数组直接执行，`&&`、`$VAR` 和 Shell 通配符不会自动展开。默认每条超时 10 分钟；非零退出、无法启动、超时或输出超限均会记录。内置代码检查清除继承的 Elysia live 测试开关，避免额外启用付费测试。

### 详细日志与状态

```text
report.md                 结果与证据链接
report.json               结构化结果、预期与实际、错误堆栈、SDK 版本
run.log                   测试起止、命令、HTTP 状态、耗时、具体报错
evidence/
  ...-http.json           管理和日常调用的请求、响应、状态、耗时
  ...-sdk-http.json       SDK 实际请求和原始响应，包括 SSE
  ...-backend.log         临时后端输出
  ...-backend-tests.log   代码检查输出；其他检查各自有日志
protocol/
  report.md               全部渠道直连与转换的详细结果
  run.log                 协议用例及请求轮次日志
  evidence/               协议请求响应
```

代码命令记录参数、工作目录、退出码和合并的标准输出/错误输出。后端日志超限会标记截断；HTTP 和代码输出超限会记录失败，并保留已收集内容。

`failed` 表示未通过；`blocked` 表示缺少配置、依赖或前置步骤；`skipped` 表示中断或请求预算不足；`not_applicable` 表示未声明相应能力。未执行不能算通过。

### 脚本自测

在脚本目录执行 `npm test`。自测使用固定配置和本地样例，不读取用户渠道配置，并拦截外部 API 请求。自测通过仅说明脚本的相关检查通过，不代表项目或真实渠道全部通过。

### 本地 SDK 工具两轮矩阵

`local-tools.mjs` 使用实际隔离网关、模拟上游及固定版本 SDK，检查 16 个方向的 JSON/SSE 并行函数工具第二轮。先在仓库根目录生成合成夹具：

```powershell
$env:ELYSIA_AUDIT_CAPTURE = Join-Path (Get-Location) '.tmp-dev/sdk-tools-wire'
Push-Location backend
go test ./server -run '^TestGatewayAuditParallelToolRoundTrips$' -count=1
Pop-Location
node scripts/protocol-audit/local-tools.mjs .tmp-dev/sdk-tools-wire
```

工具会编译当前后端，创建临时数据库，并通过管理 API 创建四条仅指向回环地址的测试源。不会读取 `config.local.json`。正常结果为 32 个场景、56 个 SDK 变体、112 次模拟上游请求；任一 SDK 格式错误、隐藏 error、重试、调用 ID 或参数／结果串线都会使检查失败。第二轮必须由 SDK 的第一轮结果构造，上游另行核对完整历史；调用记录在临时实例清理前查询并写入标准输出 JSON。

Responses 历史使用固定 SDK 的 `toResponseInputItems`，并断言未省略任何输出项；Chat 仅去掉 SDK 添加的 `parsed`／`parsed_arguments` 辅助属性，不改写网关响应。用例参数为安全整数 1、2，不能代替已有 Go 大整数原文保真测试，也不能证明 JavaScript SDK 的数值精度、供应商真实签名接受情况或真实渠道工具能力。

## 测试范围与限制

- 测试连接真实上游和自动启动的独立后端，使用临时数据库。后端准备失败时，仍尝试直连和代码检查。
- 不主动制造上游 429、503、超时或断流；实际发生就记录，未发生不代表已验证。
- 取消测试检查客户端取消及后续可用性，无法确认供应商停止生成或计费。流式记录首字节和总耗时，不能凭一次短响应判断是否存在缓冲。
- 管理操作通过 API 执行，不包含浏览器页面、长期压测和安装包。模型列表只检查第一页，也不等于完整供应商 Schema 认证。
- 模型能力限制应结合直连结果判断，不能直接归因于网关转换。例如 GPT-6.1 Sol 的工具调用支持需区分 Chat 与 Responses，参见上方官方文档。
- 只有脚本自测使用固定样例，不调用付费渠道；样例不参与实际测试报告。
# 本仓库接入后的补充验收

保持默认并发 32、每次最大输出 32768 tokens 和六组检查。`daily` 组另含系统角色、普通文本 reminder、工具第二轮和原 include/store/usage 故障专项，生成次数高于原始工具约 440 次的估计；负向类型和上下文用例应在本地拒绝。

启动与每次重启检查 `/api/admin/protocols` 的 `runtimeReady`、四个固定预置的激活与实际加载哈希。健康接口或页面上的四个名称不能替代该检查。失败保留结构化阶段和原因。

临时实例启用有界正文日志。每条生成响应的 `X-Elysia-Request-Id` 用于查找其持久调用记录，清理临时数据库前导出脱敏证据；同时保存激活修订、绑定、策略、诊断和事件顺序。没有调用 ID 的认证或发现请求明确标记不可关联，不靠时间猜测。SDK 自测完整执行，Anthropic 尾帧的必需 usage 单独检查。

报告区分直连失败、模型未遵循指令、限流、转换错误、SDK 消费错误和待分析情况。预期拒绝独立标注，限流或预算不足不能算通过。`daily` 的系统结构是人工边界样例，不代表已捕获的 Claude Code 实际流量。真实渠道通过仍不等于实际客户端或 VS Code 插件验收通过。本次用户未使用 Cherry Studio，不将其作为用户验收前提。

### 隔离的 Claude Code 插件取证

`plugin.mjs` 使用已安装的 VS Code 扩展，创建临时网关、独立配置及只有合成文本的工作区，不安装独立 CLI，不修改日常插件配置。它会使用配置中的 Anthropic 渠道；只有开始插件验收时才启动。

```powershell
node scripts/protocol-audit/plugin.mjs --config scripts/protocol-audit/config.local.json --extension "已安装的 anthropic.claude-code 扩展目录" --out .tmp-dev/plugin-audit-new
```

输出目录必须不存在。就绪后读取其中 `ready.json`，用对应 `profile`、`extensions`、`workspace` 启动 VS Code：

```text
code --user-data-dir PROFILE --extensions-dir EXTENSIONS --new-window WORKSPACE
```

脚本不会自行打开窗口。启动命令返回也不代表窗口或插件已成功加载，必须实际确认插件版本。在隔离窗口中依次发送“只回复 OK”、“读取 audit-note.txt 并告诉我 marker”、“沿用刚才的结果，再回复一次 marker”，审批读取该测试文件。创建输出目录下的 `STOP` 文件后，脚本导出证据并停止临时服务。

代理仅转发到本机临时网关，保持请求、响应原字节及 trailer；通过精确调用 ID 查询持久记录，写盘时脱敏。`report.json` 不自动宣称客户端验收成功，需结合插件显示、工具结果和记录确认。输出目录含隔离配置，必须放在已忽略的 `.tmp-dev/` 或 `scripts/protocol-audit/results/`，不能提交。

脱敏使用固定版本 `jsonc-parser` 定位敏感值，保留其他 JSON 字节、工具参数空格和大整数。复制工具目录后需要安装 lockfile 中的依赖。当前本地环境使用 `npm ci --ignore-scripts --prefix scripts/protocol-audit` 安装，不执行依赖生命周期脚本；全部 SDK 离线测试仍实际执行。
