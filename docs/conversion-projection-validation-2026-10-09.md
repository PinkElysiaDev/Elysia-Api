# dev.22 跨协议修复与验收

源码基线为 `efc010f`，本次修改位于工作区，未提交或部署远端。交付状态：**本地修复已验证，远端原故障待确认**。

## 结果

- Responses 请求里的 `include` 在语义转换阶段处理。Gemini 不再收到该参数；`reasoning.encrypted_content` 接入 Elysia 认证续传，未知选择项仍拒绝，类型错误准确定位。
- Gemini 思考用量返回 Anthropic 时，兼容策略省略目标无法表达的明细，保留输出总量和内部完整计量。工具提示明细、缓存 TTL 桶及 Gemini 缓存创建计数使用同一具名动作。
- 默认规则依据实际方向模块，旧自定义副本自动获得修复；未要求重新创建或手动启用。手写目标映射不按 family 猜测其能力。
- 编码器与组合比较器的隐藏缓存省略已移除。严格模式拒绝损失，未知扩展及生成约束继续受保护；待省略的非法计数不能躲过校验。
- 原始上游流式用量负责计量，客户端投影不回写。预览和组合验证共用转换动作，离线续传证明仅使用临时内存认证状态。
- 自定义验证失败被局部隔离，原定义、激活意图及草稿保留。失败报告保存于原修订；有效证据和失败证据都不在每次重启重复产生。

## 验证覆盖

| 项目 | 结果与边界 |
|---|---|
| Go | `go test ./... -count=1 -timeout=8m`；全量测试通过 |
| 静态检查 | `go vet ./...`、WebUI TypeScript 检查通过 |
| 并发 | protocol、builtin、storage、server 的投影、续传、网关及升级相关 `-race` 用例通过；使用仓库固定的 LLVM-MinGW 工具链 |
| 原故障 | 真实网关到模拟 Gemini，覆盖 Responses、Messages、Chat 的 JSON/SSE、晚到思考用量、客户端载体及实际 SQLite 落库 |
| 旧自定义 | 去除 feature 声明的历史副本、非预置 ID、`after` 映射、完全手写目标保护、自动重验与隔离 |
| 严格模式 | 思考、缓存及专用计数损失拒绝；服务端保存不能冒充客户端载体已经交付；无签名时不制造缺失错误 |
| 组合证据 | 新 include 和思考用量样例的静态期望先通过离线验证；兼容投影往返通过，严格或禁用规则失败 |
| 浏览器 | Chromium 6 项通过；转换预览、动作选择、策略验证/激活/409，以及协议设计器流程覆盖真实后端；部分展示用例使用模拟数据 |
| 真实客户端 | OpenAI Node SDK 7.30.1 的 Chat 和 Responses 普通/流式工具第二轮通过；Responses 实际发送 include。Claude Code 2.1.295 的受限 Read 工具第二轮通过 |
| 构建 | Windows/Linux/macOS × amd64/arm64 六目标构建通过；仅 Windows amd64 在本机执行 |
| 新二进制 | 新库启动、四个预置、嵌入页面和版本字段冒烟通过 |
| 历史二进制升级 | 从 `efc010f` 编译的 dev.21 二进制创建真实数据库和自定义协议，再替换 dev.22。原激活修订及未完成草稿保留，自动继承 include 修复 |
| 十次重启 | 历史库升级后连续十次启动新二进制，协议、报告、草稿、激活、配置及备份清单不变；另有失败自定义证据的重复刷新回归 |

客户端测试仅连接本地模拟上游。Claude 使用隔离目录、`--bare --restricted`、只读 Read 工具及既有显式 metadata/output_config 兼容规则；不代表所有客户端默认配置都已兼容。SDK 流式第二轮主动丢弃载体，验证稳定会话下的持久恢复；另有关闭数据库再恢复的 HTTP 测试。

## 复现与产物

产物位于 `dist/standalone/`，`conversion-projection-build-manifest.json` 记录平台、编译器版本、工作区源文件摘要与二进制 SHA-256。构建的提交字段仍为源码基线，辨认本次工作区构建应使用清单。

常用入口：

```text
go test ./... -count=1 -timeout=8m                 # backend 目录
go vet ./...                                     # backend 目录
node scripts/test-protocol-e2e.mjs
node scripts/build-standalone.mjs
node scripts/smoke-standalone.mjs
node scripts/smoke-projection-upgrade.mjs <旧二进制> <新二进制>
```

真实客户端入口沿用 `ELYSIA_TEST_OPENAI_MODULE`、`ELYSIA_TEST_CLAUDE_BIN` 和 `TestConversionClientToolRoundTrip`。升级脚本使用随机端口及临时目录，保留报告和日志；复制协议时保留超过 JavaScript 安全整数范围的数值原文。

## 尚未关闭的边界

没有远端此次故障的真实请求、协议修订和上游重试结果，不能将远端问题标记关闭。本次没有重构全部启动迁移、异步任务或 WebSocket 转换；历史二进制验收针对 dev.21→dev.22，不代表全部历史版本。

纯手写映射中无法确定含义的错误不会被猜测改写。`store`、`truncation`、`previous_response_id` 以及不支持的生成约束仍需各自的显式等价映射，不能通过这次兼容规则自动删除。
