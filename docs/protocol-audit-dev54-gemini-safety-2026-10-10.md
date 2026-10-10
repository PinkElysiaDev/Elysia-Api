# Gemini → Responses 请求修复与验证（dev.54，2026-10-10）

**本次日志中的 `/wire:gemini` 故障已定位并修复；本地回放和配置中的真实 Responses 渠道定向测试通过。用户日常实例尚未替换复测，不将此前全部四协议问题标记关闭。**

## 日志证据与根因

用户提供 `usage-log-req_1791634998542307000_4b74ba12.json`。原请求包含 `contents`、空 `generationConfig` 和五项 `safetySettings`，不含 `systemInstruction`。五种分类为 hate speech、dangerous content、harassment、sexually explicit、civic integrity，阈值均为 `BLOCK_NONE`。

失败发生在发往上游之前：HTTP 400，上游请求与响应均为零字节。Gemini 解码器未识别根层 `safetySettings`，将整个数组归入 `wire:gemini`；Responses 编码器按未知跨协议语义拒绝。原诊断只给集合路径，掩盖了具体触发字段。

脱敏结构保存在 `backend/protocol/builtin/testdata/gemini-disabled-safety.json`，仅将用户正文替换为固定测试句。旧 dev.52 Windows 二进制回放 JSON/SSE 均复现相同 400，模拟上游调用次数均为零。

先前独立发现的 `systemInstruction.role` 也是缺陷，但不是这份日志的原因。Google SDK `@google/genai` 2.28.0 会将字符串系统指令序列化为 `role:"user"` 加 `parts`；旧解码器把 wrapper 的 `role` 当作未声明字段。修复后权限仍由 `systemInstruction` 所在位置确定，不把系统文字降成 user，也不将普通文本提升权限。

## 修复行为

新增标准语义参数 `gemini_safety_settings`，共用入口解码与自定义映射后校验。处理发生在现有转换策略中：动作 `gemini_safety_settings`、规则 ID `gemini-safety-settings`、request 阶段、默认顺序 170。

| 输入及目标 | 行为 |
|---|---|
| Gemini 原生目标 | 保留原数组及存在性；未知字符串枚举、附加字段仍交给原生路径 |
| 缺失值 | 不产生诊断或额外设置 |
| 跨协议 null、空数组 | 规范化为无分类覆盖，信息级 `conversion_normalized`，保真分类 `preserved` |
| 五种已列分类，所有阈值为 `BLOCK_NONE` 或 `OFF` | 兼容模式移除目标无法表达的分类控制，记录 warning、`lossy_compatible`、规则 ID、策略哈希及 `/safetySettings`；目标自身审核继续生效 |
| 上述非空设置，严格模式 | 删除前拒绝，不调用上游 |
| 实际阻断阈值、未知分类、额外属性 | 跨协议拒绝，定位具体数组项及字段，不自动删除约束 |
| 非法类型、重复分类 | `invalid_input`，定位 `/safetySettings` 或其子路径 |
| 规则关闭 | 不再投影，目标编码器明确拒绝 `/parameters/gemini_safety_settings` |

这不代表 Responses 获得了 Gemini 的安全控制，也不承诺能够关闭目标审核。目标为已知内置编码模块时安装默认规则；旧自定义 ID、旧 feature 集和 `after` 映射自动受益。手写目标编码器不按 family 猜测删除参数。运行、预览和组合验证使用相同动作，原始请求副本不变。

同时识别并校验 `systemInstruction.role`，保留 Gemini 原生形态。未知扩展继续拒绝，诊断改为稳定排序的具体嵌套路径并正确转义 JSON Pointer，不输出字段值。没有加入忽略所有未知字段的兜底。

编译器语义版本提升至 `2.0.0-dev.54`，通过原有机制重验；不改写用户定义、草稿或手动配置。

## 真实渠道定向验证

使用用户现有 `config.local.json` 的 Responses 渠道；独立临时网关、自动重试关闭，单次最大输出 32768 tokens，超时 180000ms。只执行本次定向检查，没有重跑数百次的完整矩阵。凭据、原始用户日志和原始供应商正文未提交。

| 检查 | 结果 |
|---|---|
| Responses 直连 JSON / SSE | 2/2，HTTP 200，正文及格式检查通过 |
| Gemini 日志结构 → Responses JSON / SSE | 2/2，HTTP 200，原 `safetySettings` 故障消失，诊断落库 |
| Google SDK 2.28.0，系统指令 + 同一安全设置，JSON / SSE | 2/2，完整消费成功，数据库记录成功 |

真实请求将正文替换为固定句子，并在原本空的 `generationConfig` 中加入已获授权的 `maxOutputTokens:32768`；其余故障相关结构保持一致。空 `generationConfig` 原形态另由真实二进制连接模拟上游验证。没有把改过正文的请求描述为原始字节重放。

原日志与本轮请求的入口、上游修订相同：

- 入口：`5a8568d57e9111e3279d8408ed4ee300f0d53323cf1ed44551f8b4a692c8e26d`
- 上游：`592c9315ba3d6ae8a54e2c36619108d3d0c1539338ac947799c7de8a2e9a01d9`
- 本轮策略：`7fc8dd3073233650545b49623e302dfa662cf02a017a627c5487b4ab8a250c02`

[脱敏证据](protocol-audit-dev54-live-evidence-2026-10-10.json) 保存实际调用 ID、修订、策略、SDK 版本、诊断及事件顺序摘要。第一次直连探测误导入自测网络保护，两个请求均在本地被阻止；纠正后独立完成两项直连，网关四项不受影响、没有重复生成。报告保留这一区别，没有将最初探测记作上游故障。

## 本地检查与升级

| 检查 | 结果 |
|---|---|
| Go 全量 `go test ./... -count=1` | 全部通过，server 488.213s |
| `go vet ./...` | 通过 |
| 相关 protocol / builtin / server race | 通过 |
| 审计脚本自测 | 135/135，零跳过，SDK 实际执行 |
| 原结构模拟回放及 Google SDK JSON/SSE | 新二进制 4/4，旧二进制两项均复现原拒绝 |
| 严格、关闭规则、未知扩展、类型、旧自定义、手写编码器、预览与组合验证 | 定向回归通过 |
| TypeScript 与 WebUI 构建 | 六平台构建流程中通过 |
| 真实后端 Playwright | Chromium / WebKit 20/20；包含管理服务真实调用及明确的模拟界面用例 |
| dev.52 数据库升级至 dev.54 | 原自定义修订、未完成草稿保留，报告使用当前编译器，预览继承新增规则 |
| 十次无变更重启 | 修订、报告、草稿、激活、配置和备份稳定 |
| 预置损坏恢复后十次重启 | 通过，保留损坏原文与自定义数据 |
| Windows / Linux / macOS，amd64 / arm64 | 六目标构建通过；Windows 实际执行回放与升级 |

升级脚本增加 `--preserve-definition`，明确区分“修复过时定义”与“原定义不变、仅重验引擎语义”。前者仍要求新修订，后者明确要求保留修订，未放宽成两种结果都接受。当前通过的升级起点为 dev.52。额外尝试的 dev.33 不符合该脚本的 Anthropic 修订不变预期，未计入通过结果，不据此宣称该历史起点验收完成。

可复现本轮升级检查：

```powershell
node scripts/smoke-projection-upgrade.mjs <dev52-binary> <dev54-binary> --preserve-definition --preset-recovery
```

## 提交与产物

修改按根因提交本地，未推送：

- `b990481`：系统指令 wrapper 解码、具体扩展诊断及回归。
- `0382731`：Google SDK 系统指令真实序列化测试。
- `e4cfb6e`：安全分类转换动作、校验、schema、版本及网关落库回归。
- `f92fab0`：原安全设置与 SDK 请求专项审计。
- `376b9ef`：明确升级测试中定义是否应改变的预期。

六平台产物位于 `dist/standalone/`，本轮实测构建标识为 `f92fab0`、编译器 dev.54。后续提交只涉及升级脚本及文档，不改变产品代码。

Windows amd64：`elysia-api-windows-amd64.exe`，SHA-256：

```text
0c9078ee36adae44678333b15f72ff2ee4af38e3e200742b698a2f47d19613e4
```

本次状态为“本地回归通过、配置渠道定向测试通过；用户日常实例替换待确认”。没有执行 VS Code 插件交互，也不要求用户使用此前澄清从未使用过的 Cherry Studio。其他阈值、工具续传及全部四协议矩阵不因本次六项通过而扩大支持声明。
