# Elysia-API 深度 Bug 审计与 Debug 报告

审计日期：2026-09-18（Asia/Shanghai）。本次仅审计，不修复业务代码，不修改公共 API、类型定义或数据库结构。

## 1. 结论摘要

确认 **17 项缺陷：P1 8 项、P2 9 项**。其中 **10 项属于本次新增能力或预设的回归，7 项在基线已存在**；历史工具索引缺陷被新的分帧工具能力进一步暴露。未确认 P0 或 P3 问题，不代表不存在其他问题。

- P1：应优先处理的授权/凭据问题、标准交付阻断，以及核心中转路径的静默数据损坏或假成功。
- P2：特定输入、协议分支或管理操作下的功能错误，应在相关功能正式使用前修复。
- “运行复现”包括隔离的解码器、渲染器、存储、HTTP handler 或浏览器测试；不等同于已调用真实上游，也不等同于完整生产部署验证。
- 表中源码位置均相对仓库根目录，行号对应下文固定的 HEAD。详细调用链、输入和验证边界见各问题正文。

| 编号 | 级别 | 缺陷及主要影响 | 关键位置 | 归属 | 证据状态 |
| --- | --- | --- | --- | --- | --- |
| DBG-001 | P1 | 删除最后一个允许组后，受限 Token 变为可访问所有组 | `backend/storage/queries.go:456`；`backend/server/server.go:980` | 历史已有 | HEAD/基线：临时 SQLite + 授权函数复现 |
| DBG-002 | P1 | query 鉴权的上游 API Key 经转发错误泄露给下游调用者 | `backend/relay/custom_protocol.go:884`；`backend/server/custom_protocol.go:171` | 历史已有 | HEAD/基线：模拟上游 + handler 复现 |
| DBG-003 | P1 | Git 文本规范化破坏 gzip，干净源码构建失败 | `.gitattributes:1`；`scripts/login-trace/verify.mjs:23` | 历史已有 | 两版本资源校验失败；单变量替换对照成功 |
| DBG-004 | P1 | Docker 前端构建阶段缺少必需的校验脚本 | `Dockerfile:10`；`packages/webui/package.json:8` | 历史已有 | 静态依赖闭包 + 等价目录复现；未执行 Docker |
| DBG-005 | P1 | 多个流式工具复用下游 index，身份/参数可能串到同一调用 | `backend/relay/custom_stream_mapping.go:393` | 历史已有，本次扩大影响 | HEAD/基线旧配置复现；HEAD 分帧预设复现 |
| DBG-006 | P1 | Responses 预设混用 item_id/call_id，工具参数关联到错误身份 | `backend/server/presets/openai-responses.json:93` | 本次新增 | HEAD 解码器复现 + 引入提交差异 |
| DBG-007 | P1 | 预设互相重叠的帧谓词吞掉同帧正文、结束信号或 usage | `backend/server/presets/gemini-generate.json:43` | 本次新增 | HEAD 解码器及 Gemini handler 复现 |
| DBG-008 | P1 | 非流式自定义响应的已映射错误被返回为 HTTP 200 空答案 | `backend/server/custom_protocol.go:185` | 历史已有 | HEAD/基线：模拟上游 + handler 复现 |
| DBG-009 | P2 | Chat 预设只提取同一帧中的第一个工具 | `backend/server/presets/openai-chat.json:37` | 本次新增 | HEAD 解码器复现 |
| DBG-010 | P2 | 预设漏配上游失败帧，错误信息丢失；结束后的错误可变成成功 | `backend/server/presets/anthropic-messages.json:35` | 本次新增 | 解码器及尾部错误 handler 复现 |
| DBG-011 | P2 | “复制为新协议”可静默覆盖已经存在的副本 | `packages/webui/src/pages/protocol-designer/index.tsx:79` | 本次新增 | 浏览器捕获 PUT + 存储覆盖语义静态确认 |
| DBG-012 | P2 | JSON 编辑后第一次保存发送旧 draft，丢失本次修改 | `packages/webui/src/pages/protocol-designer/protocol-form-dialog.tsx:110` | 历史已有 | HEAD/基线浏览器复现 |
| DBG-013 | P2 | Anthropic 预设透传未转换的 tool_choice | `backend/server/presets/anthropic-messages.json:23` | 本次新增 | 与内置转换器进行同输入运行对照 |
| DBG-014 | P2 | 非流式块提取把 thinking/functionCall 对象串行化成正文 | `backend/relay/custom_mapping.go:449` | 本次新增预设触发旧提取行为 | 两种预设运行复现 |
| DBG-015 | P2 | 标准预设未配置嵌套 usage 别名，缓存/推理 token 明细归零 | `backend/relay/custom_protocol.go:1454` | 本次新增预设缺失配置 | Chat 预设运行复现 |
| DBG-016 | P2 | omitIf 比较原始输入而不是渲染后的值，默认值未被省略 | `backend/relay/custom_mapping.go:585` | 本次新增 | HEAD 请求渲染复现 |
| DBG-017 | P2 | 按原始下标依次删除条件数组项，后续项错位而漏删 | `backend/relay/custom_protocol.go:1141` | 本次新增 | HEAD 请求渲染复现 |

**总体判断：**现有 Go 测试、vet、lint、类型检查通过，并不能支持“本次没有回归”的结论。新增预设的测试输入覆盖不足，尤其没有正确区分工具的两类 ID、多个工具与复合帧。另有两项历史交付缺陷使“当前工作区能构建”与“提交内容可交付”产生明显差异。

## 2. 固定范围与审计方法

### 2.1 版本基线

实际仓库：`D:\DevProjects\Elysia-API\Elysia-Api`；分支：`deploy`。开始审计及写报告前工作区均无已跟踪修改或未跟踪文件。

- 基线：本地记录的 `origin/deploy` = `0fe77225c9ebd28f70a767f2ca490c2b090d3c42`。
- HEAD：`c982b788e4046dbe1eaa1d5a0f74cd0583cc3a87`。
- 范围：5 个已提交、相对上述本地远程跟踪分支未推送的提交，23 个文件，新增 2700 行、删除 121 行。
- 本次未 fetch，不把本地 `origin/deploy` 的值表述为远端服务器的实时状态；本次也不是对不存在的工作区未提交差异做审查。

| 顺序 | 完整提交哈希 | 日期 | 主要内容与审计关注 |
| --- | --- | --- | --- |
| 1 | `a7ad068f86bbecb0e3f9cdef459f3701337e9255` | 2026-09-18 | Match 条件原语、可配置终止；检查缺失/null/数值与结束状态 |
| 2 | `ee02476b6d1c45320f5a221c82a578813e13cc41` | 2026-09-18 | aliases、when/omitIf、分内容类型增量模式；引入 DBG-016/017 |
| 3 | `c8a4746be5a0bb9f46e3fff95cd96b36aeed250d` | 2026-09-18 | 谓词帧、分帧工具身份组装、stream path；扩大 DBG-005 暴露面 |
| 4 | `31ac1ebee49a54c952e5441e40547a57b700a6b4` | 2026-09-18 | 四种预设、首次播种、request.shape、复制 UI；引入其余 8 项新增缺陷 |
| 5 | `c982b788e4046dbe1eaa1d5a0f74cd0583cc3a87` | 2026-09-18 | 协议定义文档；用于交叉检查预期语义，不能代替运行证据 |

逐个检查提交差异，再审查叠加后的实现与调用链。阅读了 `docs/archive/code-review-2026-06.md` 并回查当前实现；不直接把旧报告中的问题重复列为当前缺陷。

### 2.2 隔离与证据约定

隔离审计目录：`C:\Users\xiaolan\AppData\Local\Temp\elysia-debug-20260918-c982b78`，下称 `<audit>`。

- `head/` 初始为 `git archive HEAD`，`baseline/` 为基线快照；没有通过修改原仓库业务代码来制造测试结果。
- 新增审计测试仅位于快照中。测试以“正确行为”为断言，因暴露 bug 而失败；必须与原项目既有测试的结果分开理解。
- 后端使用临时 SQLite、临时目录和 `httptest` 回环上游；前端管理 API 由 Playwright 拦截返回模拟数据。未读取生产凭据，未复用运行中的业务服务，未调用真实付费上游。
- 唯一用于凭据泄露复现的字符串为 `AUDIT_UPSTREAM_SECRET`，不是实际密钥。
- 前端复用已安装的 `node_modules`，不是全新依赖解析或供应链审计。
- 初始干净快照验证后，仅在 `<audit>/head` 中用本地完好的 gzip 替换损坏的归档资源，做 DBG-003 单变量对照。损坏原件保留在 `logs/submitted-trace.bin.gz`；后续浏览器对照结果必须带上该前提。
- 新能力在基线没有对应配置/类型时，不伪造“基线测试通过”；使用逐提交差异确认引入点。历史问题则尽可能用同一复现分别运行基线与 HEAD。

## 3. 已确认缺陷

### DBG-001 / P1：删除唯一授权组后，受限 Token 获得全组访问权

**位置与函数：**`backend/storage/queries.go:427` 的 `Store.DeleteGroup`、`backend/storage/queries.go:456` 的 `removeGroupFromTokens`、`backend/storage/queries.go:284` 的更新语句，以及 `backend/server/server.go:974` 的 `tokenAllowsGroup`。空列表被定义为“不限制”，见 `backend/storage/types.go:163`。

**触发条件：**一个已启用 Token 的 `allowedGroups` 只有组 A，系统另有原本不允许它访问的组 B；管理员删除 A。并不要求该 Token 自己有管理权限。

**预期与实际：**删除最后一项显式授权后，应不再授权任何组或禁用该 Token；实际持久化为 `[]`，鉴权函数按“不限制”放行 B 以及其他组。

**根因与调用链：**`adminDeleteGroup` → `DeleteGroup` → `removeGroupFromTokens` → `removeGroupName`，将受限列表删空；随后 `tokenAllowsGroup` 在长度为零时直接返回 true。`backend/server/admin.go:754` 会使路由缓存失效，因此不是只存在于数据库、要等重启才生效的问题。事务保证原子性，但无法修正这一授权语义冲突。

**最小复现与结果：**临时数据库创建 `allowed-group`、`restricted-group` 和仅允许前者的启用 Token，先断言后者不可访问，再删除前者、失效缓存并重新读取 Token。`TestAuditDeleteLastAllowedGroupDoesNotGrantAll` 在 HEAD 和基线均得到：

```text
before=[allowed-group] after=[] enabled=true restricted_group_allowed=true
```

**影响与归属：**历史安全缺陷；授权集合可能因正常管理操作扩大。运行验证覆盖持久化、缓存重新取值和实际授权函数；管理路由到这些函数的连接经静态检查，没有使用生产 Token 发起越权调用。

**修复方向与回归：**显式区分“不限制”与“没有任何允许组”；删除最后一项授权时应拒绝访问或禁用 Token，不能仅删除字符串。覆盖单组、多组、原本无限制 Token，以及缓存重载、重启后的相同行为。

### DBG-002 / P1：query 鉴权密钥经网络错误回显给中转调用者

**位置与函数：**`backend/relay/custom_protocol.go:884` 的 `SendCustomProtocolRequest` 将 API Key 放入 URL query，随后调用 HTTP client；`backend/server/custom_protocol.go:171` 将返回错误用 `%v` 拼入下游消息。流式路径同类拼接位于 `backend/server/custom_stream.go:58`。

**触发条件：**自定义协议使用 `auth.mode=query`，且上游发生返回包含请求 URL 的传输错误；在单候选或重试最终失败时，该错误传到下游。

**预期与实际：**调用者可以收到失败原因，但不应收到属于模型源的上游凭据。实际响应包含整个带 Key 的 URL。

**最小复现：**协议鉴权配置为 `{"mode":"query","query":"api_key"}`，模拟上游接受连接后直接关闭。`TestAuditQueryAuthSecretInDownstreamError` 通过中转 handler 收到 HTTP 502：

```text
failed to forward custom protocol request: Post "http://127.0.0.1:PORT/generate?api_key=AUDIT_UPSTREAM_SECRET": EOF
```

**根因与影响：**`net/http` 传输错误携带 URL，业务层把完整错误对象当成可公开文本。请求/响应 body 的脱敏不能覆盖该字符串。持有普通中转 Token 的调用者即可在上述失败条件下看到源 Key；这里没有把“须管理员配置协议”误当成“只有管理员能收到该错误”。

**验证与归属：**HEAD/基线均运行复现，属于历史缺陷；流式相同暴露路径为静态确认，未额外宣称所有网络错误类型都含 Key。

**修复方向与回归：**在错误进入响应、日志和重试记录前做统一结构化脱敏，尤其处理 `url.Error`、自定义 query 参数名及 URL 编码值；不要只做固定 `api_key` 文本替换。覆盖 EOF、连接错误、重定向失败，以及正常/流式两条路径。

### DBG-003 / P1：已提交的 gzip 被 Git 换行规范化破坏，干净构建不可用

**位置：**`.gitattributes:1` 强制 `* text eol=lf`，二进制例外不含 `.gz`；受损文件是 `packages/webui/public/assets/elysia-character-trace.bin.gz`（二进制，无文本行号）。清单为 `packages/webui/public/assets/elysia-character-trace.json:1`，校验失败点为 `scripts/login-trace/verify.mjs:23`，构建入口见 `packages/webui/package.json:8`。

**触发条件：**从提交对象取得源码、进行干净检出/归档构建，而不是使用恰好仍保留完好二进制原字节的开发工作区。

**预期与实际：**压缩资源应原字节提交并通过校验；实际 Git 对象中的 gzip 少了 45 字节，checksum 校验失败，直接解压也报 `incorrect data check`。

| 来源 | 字节数 | SHA-256 |
| --- | --- | --- |
| 本地原工作区完好资源 | 6859618 | `860dffc5551a43e9185cc1acfbb18d0684954c06c3b94ce2835c06c1901373a1` |
| HEAD 归档/提交资源 | 6859573 | `18a5618797f588b49b9ff5fc1d4be378d3a1a762cfe0adbab94cb03eb322917c` |

**根因证据：**对完好资源作纯字节 `CRLF → LF` 替换，结果与提交资源完全相等。可用 Node 的 latin1 无损字节映射复查该关系：

```js
Buffer.from(localBytes.toString('latin1').replace(/\r\n/g, '\n'), 'latin1').equals(submittedBytes)
```

**运行结果：**未改动的 HEAD 快照构建在 `payload checksum mismatch` 处失败，原有浏览器用例 19 通过、11 失败；失败涉及资源校验或 `data-trace-ready=false`。基线资源校验同样失败。本地原工作区构建却通过，且 Git 仍显示干净——文本 clean 规则使“工作区干净”不等于二进制原始字节一致。

**排除其他原因的对照：**只替换临时 HEAD 快照中的这一个 gzip 后，构建通过，原有 30 项浏览器测试全部通过。未将这次替换写回原仓库。11 个测试失败按同一根因合并，不计成 11 项 bug，也不据此宣称所有登录功能完全不可用。

**归属与影响：**历史交付缺陷，资源在 `27933aea`（2026-09-16）引入，早于本次基线；影响干净构建及依赖该资源的前端行为。

**修复方向与回归：**为 gzip 添加二进制属性，并重新加入完好的原字节；只改 `.gitattributes` 不能恢复已经损坏的 Git 对象。以新的干净检出执行 hash、gunzip、构建和浏览器校验，不能仅验证原工作区。

### DBG-004 / P1：Dockerfile 未复制前端构建现在依赖的脚本

**位置：**`Dockerfile:7` 起的前端构建阶段只复制根 package.json 与 `packages/webui/`，`Dockerfile:11` 即执行 build；`packages/webui/package.json:8` 已要求先执行 `../../scripts/login-trace/verify.mjs`，该脚本还读取标注等伴随文件，见 `scripts/login-trace/verify.mjs:22`。

**触发条件与实际行为：**按仓库 Dockerfile 构建镜像；容器内不存在 `/workspace/scripts/login-trace/verify.mjs`，Node 在进入 Vite/Go 构建前就找不到模块。

**最小复现与证据：**在 `<audit>/docker-stage` 仅准备 Dockerfile 相应 COPY 可带入的文件，执行同一相对路径脚本，得到 `MODULE_NOT_FOUND`。静态检查 COPY 输入集合与 package script 的依赖即可确认缺失。

**边界与归属：**当前环境没有 Docker CLI，没有实际执行 `docker build`，所以这里不是声称已重现完整镜像构建过程。缺失文件的构建必需性已由等价目录运行确认。此问题独立于 DBG-003：补齐脚本后仍可能遇到 gzip 校验失败；修好 gzip 也不会自动把脚本复制进镜像。引入脚本依赖的提交为 `27933aea`，基线同样存在。

**影响与修复方向：**标准单镜像交付被阻断。应在前端构建之前复制校验脚本及其读取的全部版本化资源/标注依赖；之后从干净上下文构建完整镜像，验证 Go 内嵌静态资源与启动路径。

### DBG-005 / P1：流内多个工具的下游 index 冲突

**位置与函数：**`backend/relay/custom_stream_mapping.go:307` 的 `frameToolItem` 用上游 index 查身份，但返回的 item 没有保留它；`backend/relay/custom_stream_mapping.go:393` 的 `contentEvents` 把“本帧 Output 数组下标”写成持久的 `ToolCallIndex`。下游 `backend/relay/maheshvara_stream_renderer_openai.go:104` 的 `writeOpenAIToolEvent` 按该 index 存储工具状态。

**触发条件：**同一条流中，两个工具分不同帧出现；每个帧映射出来的 Output 都只有一个工具。只要满足这个条件，即使上游给出了不同 ID 和 index，也可能同落到下游 index 0。

**最小复现（Anthropic 预设，每行为一个 SSE data）：**

```json
{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_a","name":"first","input":{}}}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_b","name":"second","input":{}}}
{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"A\"}"}}
{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"B\"}"}}
```

**预期与实际：**两组 added/arguments 事件各自有稳定且不同的下游 index；`TestAuditParallelFrameToolIndices` 得到 `map[call_a:0 call_b:0]`。身份查找本身拿到了不同 ID，但发送事件时丢了流级索引语义。

**历史对照：**使用不依赖新增 frames 的 `ToolCallsPath:"calls"`，依次输入 `{"calls":[{"id":"first","name":"first_tool","arguments":{}}]}` 与 `{"calls":[{"id":"second","name":"second_tool","arguments":{}}]}`；`TestAuditLegacyToolIndexCollision` 在 HEAD 和基线均得到 `map[first:0 second:0]`。因此不能误报为 `c8a4746` 首次引入的回归。

**影响：**已复现错误事件索引；进一步按下游 renderer 调用链静态确认，同一 index 的身份会被覆盖、参数进入同一状态。在按 index 组装工具的客户端中，可造成两个调用串联或身份错配。新增分帧能力和预设扩大了历史缺陷的可触发范围。

**修复方向与回归：**维护流级的稳定工具身份到下游 index 的映射，区分 wire index、output index 和 tool index；不要用每帧临时数组下标替代。覆盖工具交错、身份帧与参数帧分离、正文和工具混排、重复身份帧、无 ID 参数帧，并对最终下游线制断言，而不只检查 Maheshvara 事件数量。

### DBG-006 / P1：Responses 预设把 item_id 当成 call_id

**位置：**`backend/server/presets/openai-responses.json:93` 从身份帧提取 `item.call_id`，`backend/server/presets/openai-responses.json:100` 却把参数帧的 `item_id` 当成相同身份；两个规则都没有用 `output_index` 建立关联。相关解析函数为 `frameToolItem` 和 `contentEvents`。

**触发条件：**Responses 的 function-call item ID 与对外调用 ID 不相同。仓库内置解码器已经分别处理这两种标识，见 `backend/relay/maheshvara_stream_decoder_responses.go:15`、`backend/relay/maheshvara_stream_decoder_responses.go:77`。

**最小复现：**

```json
{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_item_1","type":"function_call","call_id":"call_1","name":"weather","arguments":""}}
{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_item_1","delta":"{\"city\":\"A\"}"}
```

**预期与实际：**参数应属于 `call_1/weather`；`TestAuditResponsesSplitIdentity` 实际产生三个事件：added `call_1/weather`、added `fc_item_1/空名称`、arguments delta `fc_item_1`。单工具即能重现，所以这不是 DBG-005 的索引冲突重复项。

**归属与影响：**`31ac1eb` 新增预设缺陷；函数调用和后续工具返回关联可能失效。既有预设测试在 `backend/server/preset_protocols_test.go:213` 附近将参数帧 `item_id` 写成与 `call_id` 相同的值，未覆盖真正需要关联的情况。本次证明的是转换结果错误，没有调用真实提供商。

**修复方向与回归：**用 `output_index` 或 item ID 建立身份关联，同时保留真正的对外 `call_id`；参数帧不能据 item_id 新建另一工具。测试必须让身份帧的 `item.id` 等于参数帧的 `item_id`，且二者不同于 `call_id`，并覆盖多工具交错与工具结果回传。

### DBG-007 / P1：预设的 first-match 分帧规则丢弃同帧其他有效信息

**位置与根因：**`backend/relay/custom_stream_mapping.go:267` 的 `matchFrame` 在首个命中后返回，这是明确的设计，不是本项单独指责的引擎 bug。问题在于 `backend/server/presets/gemini-generate.json:43` 的结束规则排在正文规则前，且只映射 usage/finish；`backend/server/presets/openai-chat.json:35` 起的工具、推理、usage、finish、正文规则也互不组合。

**触发条件：**一个 JSON 帧同时携带正文和结束/usage 等信息。最小 Gemini 帧：

```json
{"candidates":[{"content":{"role":"model","parts":[{"text":"final answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}
```

**预期与实际：**应输出 `final answer`，记录 usage 后正常结束。`TestAuditGeminiCombinedTerminalText` 实际只有 usage 与 completed，正文为空；`TestAuditPresetFailuresEndToEnd/gemini-final-text` 通过真实中转 handler 加模拟上游确认，下游只收到 finish chunk 和 `[DONE]`，没有正文，外观却是成功结束。

**Chat 对照输入与结果：**

```json
{"choices":[{"index":0,"delta":{"content":"last"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}
```

`TestAuditChatCombinedTextUsage` 只得到 usage，`terminal=false`，正文与结束标志都丢失。该用例证明预设无法处理这种兼容上游的复合帧，不声称所有 OpenAI 上游都会用这种组合发送。

**归属与影响：**`31ac1eb` 新增预设错误；可能丢掉最后一段甚至整段答案，也可能将已有结束信号的流误判为未正常结束。优先级主要依据已在 handler 层复现的 Gemini 成功空输出。

**修复方向与回归：**让选中的映射保留一帧内全部相关字段，或设计明确的组合处理；仅交换谓词顺序会把“丢正文”变为“丢结束/usage”，不能根治。覆盖同帧 text+finish+usage、reasoning+text、tool+finish、parts 数组混排及 `[DONE]` 后尾部 usage；并保持既有 first-match 契约清晰。

### DBG-008 / P1：非流式已映射错误被包装为成功的空 completion

**位置与函数：**`backend/relay/custom_protocol.go:998` 的响应转换可以生成 `MaheshvaraResponse.Error`，且有 Error 时允许没有正文；`backend/server/custom_protocol.go:185` 的共享非流式处理在转换后没有检查该字段，直接于 `backend/server/custom_protocol.go:193` 结算并渲染，最终在 `backend/server/custom_protocol.go:199` 返回 HTTP 200。

**触发条件：**自定义协议显式配置 `ErrorPath:"error"`，上游使用 HTTP 200 携带业务错误。这是 ErrorPath 应当处理的输入，而不是要求对所有任意 JSON 猜测错误。

**最小复现：**模拟上游返回 `{"error":{"message":"quota exhausted"}}`。`TestAuditCustomErrorResponseStatus` 通过 Chat 中转 handler 实际得到：

```json
{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}
```

HTTP 状态为 200，原错误不见。期望是明确的失败响应，并按适当的错误分类决定是否重试，而不是产生空答案成功。

**验证、归属与影响：**HEAD/基线均运行复现，历史已有。已直接证明 Chat 输出错误、共享处理路径记为成功；其他输出协议的最终错误保真应作为回归矩阵继续验证，不把它们都声称为已独立复现。调用方可能把失败作为有效答案，记录、重试决策和成功统计也进入错误分支。

**修复方向与回归：**在 settle/render 前统一处理映射出的 Response.Error，保留可公开错误信息并适配下游状态码；避免把有效的空成功输出误判为错误。覆盖四种下游、HTTP 2xx 业务错误、非 2xx 错误及多候选重试策略。

### DBG-009 / P2：Chat 预设只读取单帧工具数组的第 0 项

**位置：**`backend/server/presets/openai-chat.json:37` 的 indexPath、idPath、namePath、argumentsPath 全部硬编码 `choices[0].delta.tool_calls[0]`。`frameToolItem` 每次只返回一个工具。

**最小复现：**

```json
{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"first","arguments":"{}"}},{"index":1,"id":"call_b","type":"function","function":{"name":"second","arguments":"{}"}}]}}]}
```

**预期与实际：**应生成两个工具；`TestAuditChatMultipleToolsInFrame` 只收到 `call_a` 的 added/arguments，`call_b` 完全消失。不是“两个工具 index 相同”，而是在分配 index 之前就没有提取第二个工具，因此与 DBG-005 根因不同。

**归属与影响：**`31ac1eb` 新增预设缺陷；影响同一 delta 包含多个工具的流式调用，未触发工具不会执行，下游也得不到丢失提示。

**修复方向与回归：**以工具数组为单位遍历，保留每项 index、身份和未经补全的参数片段。覆盖单帧多工具、后续只带 index 的参数帧、空参数、重复帧，并与内置 Chat 解码路径做同输入对照。

### DBG-010 / P2：预设没有识别失败帧，尾部错误可被吞成正常结束

**位置：**`backend/server/presets/anthropic-messages.json:35` 的 frames 没有 error 分支；`backend/server/presets/openai-responses.json:72` 起只列出正常事件，没有 response.failed 等失败映射。`backend/relay/custom_stream_mapping.go:156` 在无命中时直接忽略该帧；这属于预设遗漏，不能把所有未知事件都一概判成错误。

**最小解码输入：**

```json
{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}
{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"failed"}}}
```

这两行分别送入对应预设。`TestAuditPresetErrorFrames` 均得到 `events=null terminal=false`，没有失败事件或上游错误消息。

**handler 级复现：**在 Anthropic 模拟流中先发正文，再发带 `end_turn` 的 message_delta，最后发上述 error。`TestAuditPresetFailuresEndToEnd/anthropic-trailing-error` 得到正文、正常 stop 和 `[DONE]`，没有 error；服务端本来用于识别 drain 阶段错误的代码（`backend/server/custom_stream.go:120` 附近）拿不到被解码器丢掉的错误事件。

**边界、归属与影响：**`31ac1eb` 新增预设遗漏。尾部错误序列是故障注入，不声称真实提供商必然如此发送。若错误发生在任何终止帧之前，服务端仍可能因缺少终止而返回通用流错误；不能把这种情况误报为“一律返回成功”。已确认的是错误本体被丢弃，以及已终止状态下的故障注入被当作正常完成。

**修复方向与回归：**为已知失败事件显式映射 ErrorPath/失败状态，保留经脱敏的 code、message；对 incomplete 类事件按目标协议语义处理。覆盖首帧错误、部分正文后错误、终止后错误、终止后合法 usage 和未知非错误事件。

### DBG-011 / P2：“复制为新协议”静默覆盖已有的 -copy 协议

**位置与调用链：**`packages/webui/src/pages/protocol-designer/index.tsx:76` 起的复制逻辑固定使用 `${id}-copy`；表单在 `packages/webui/src/pages/protocol-designer/protocol-form-dialog.tsx:119` 无论 isNew 与否都调用 upsert；`backend/storage/custom_protocol.go:54` 的 `ON CONFLICT(id) DO UPDATE` 明确覆盖同 ID 配置。

**触发条件：**列表已存在 `audit-protocol` 和用户定制过的 `audit-protocol-copy`；再次对前者点击“复制为新协议”，不手改 ID，直接保存。

**预期与实际：**应生成唯一的新 ID，或明确阻止/确认覆盖。实际仍 PUT 到 `audit-protocol-copy`，payload 的 name 为原件的 `Original`，而不是已有副本的 `Existing customized copy`。UI 的“新建”标志主要影响标题和 ID 输入状态，没有新增语义约束。

**证据与边界：**Playwright 用例 `audit copy as new must not overwrite an existing copy` 在 HEAD 捕获了上述 PUT；后端按 ID 覆盖是 SQL 静态确认。本用例使用模拟管理 API，没有在真实业务数据库里实际覆盖配置。

**归属与影响：**`31ac1eb` 新增复制入口引入；第二次复制即可触发，可能丢失副本定制，并影响引用这个协议 ID 的模型源。

**修复方向与回归：**生成未占用的新 ID并明确冲突提示；还需考虑浏览器列表过时或并发复制造成的竞态，可配合服务端 create-only/显式覆盖语义。覆盖已有 `-copy`、连续多次复制、并发创建以及原件和已有副本不变。

### DBG-012 / P2：手改 JSON 后第一次保存仍使用旧 React 状态

**位置与函数：**`packages/webui/src/pages/protocol-designer/protocol-form-dialog.tsx:92` 的 `applyPendingJsonDraft` 解析 JSON 后在第 97 行调用 `setDraft(parsed)`；`save` 在第 110 行调用它后，又在第 119 行读取本轮闭包中的旧 `draft` 发送请求。

**触发条件：**进入协议 JSON 标签修改有效 JSON，不先点击“应用到编辑器”，直接点击“保存协议”。

**最小复现：**原 name 为 `Original`，在 textarea 中改成 `Edited JSON value`。Playwright 用例 `audit JSON draft saves the newly edited value on the first click` 捕获的首个 PUT 仍为：

```json
{"id":"audit-protocol","name":"Original"}
```

这里仅摘录关键字段；完整 payload 存在审计日志。预期首个请求就含有 `Edited JSON value`，实际 UI 可以提示保存成功但持久化旧版本。

**根因与归属：**React 状态更新不会同步改写当前 save 闭包引用。HEAD/基线浏览器均复现，属于历史问题；相关代码可追溯至 `19f54dae`（2026-09-17），不是本次五个提交的新增状态问题。

**影响与排除项：**直接保存 JSON 的修改会丢失，影响不止 name。普通编辑后保存的控制用例通过：样本中的 `request.shape`、`aliases.textKeys`、`stream.finishWhen` 完整保留，因此没有把本问题扩大为“编辑器一律删除所有新增字段”。

**修复方向与回归：**解析得到局部 `nextDraft`，同一对象同时用于校验与 API 调用，setState 仅负责更新 UI。覆盖首次保存、连续保存、ID 修改、非法 JSON 阻止请求、保存后重新打开核对实际内容。

### DBG-013 / P2：Anthropic 预设没有将公共 tool_choice 转为目标形状

**位置：**`backend/server/presets/anthropic-messages.json:23` 直接取 `tool_choice`；`backend/relay/custom_protocol.go:1069` 的 shape 处理只转换输入消息和工具定义，Anthropic 分支位于 `backend/relay/custom_protocol.go:1103`。内置转换路径的对应处理见 `backend/relay/maheshvara_convert.go:435`。

**触发条件：**公共输入带 `tool_choice:"required"` 和一个普通 function 工具，经 `custom:anthropic-messages` 发往上游。

**运行对照：**`TestAuditAnthropicToolChoiceShape` 给自定义预设路径与内置 Anthropic 路径传入相同 Maheshvara 请求，结果关键字段如下：

```text
custom:  "tool_choice":"required"
builtin: "tool_choice":{"type":"any"}
```

**预期与实际：**预设应生成与目标协议选择策略一致的对象，实际只是换了 messages/tools，选择策略仍是输入线制的原值。当前 `request.shape` 文档本身只承诺部分形状转换，因此本项归类为新增标准预设配置/适配不完整，不错误扩大为“shape 承诺转换所有字段却未实现”。

**归属与影响：**`31ac1eb` 新增；强制或指定工具的请求存在不兼容。已经确认输出结构与项目内置转换器不一致，没有调用真实 Anthropic 服务，不能把“真实上游返回某个 400”当成已经观察到的结果。

**修复方向与回归：**给预设提供目标协议的 tool-choice 转换结果，或扩展有明确定义的 shape 上下文字段。对 auto、none、required、指定名称及与 thinking 选项的组合做契约测试；与内置路径比较语义，而非只比较消息正文。

### DBG-014 / P2：非流式预设把非文本对象变成文本输出

**位置与共同机制：**`backend/relay/custom_mapping.go:449` 的 `customTextValueWithKeys` 对对象找不到默认文本键时，在 `backend/relay/custom_mapping.go:471` 回退为整对象 JSON。新增预设没有正确限制块类型/字段，使标准协议的对象落入这个兜底。

**案例 A：Anthropic thinking。**`backend/server/presets/anthropic-messages.json:29` 选择 thinking 块，但没有配置用于读取 `thinking` 字段的文本键；默认键见 `backend/relay/custom_mapping.go:482`。输入：

```json
{"content":[{"type":"thinking","thinking":"secret reasoning","signature":"sig"},{"type":"text","text":"answer"}],"stop_reason":"end_turn"}
```

`TestAuditAnthropicNonStreamThinking` 预期 reasoning_text 为 `secret reasoning`；实际为 `{"signature":"sig","thinking":"secret reasoning","type":"thinking"}`。`sig` 是测试哑值，问题是对象被当成内容，不据此声称泄露了服务端秘密。

**案例 B：Gemini functionCall。**`backend/server/presets/gemini-generate.json:30` 用 `thought isFalse` 选择正文；没有 thought 的 functionCall 块也能通过。输入：

```json
{"candidates":[{"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"A"}}}]},"finishReason":"STOP"}]}
```

`TestAuditGeminiToolBecomesText` 实际同时得到合法 function_call 和一个多余 message，其 text 是整个 functionCall JSON。预期工具专用响应不应凭空出现这段用户可见正文。

**归属与影响：**两例均由 `31ac1eb` 新预设触发历史通用提取器的宽松行为；合并为一个块提取语义缺陷，而不是重复统计。影响 `custom:` 预设非流式结果，不声称内置 Anthropic/Gemini 路径有相同问题。

**修复方向与回归：**明确筛选“确为文本/推理”的块，配置正确字段键；标准预设不应把任意对象 JSON 当正文兜底。测试纯文本、纯思考、纯工具、混合块和 signature 保留位置，并核对最终四种下游输出。

### DBG-015 / P2：Chat 预设丢失 nested usage 明细

**位置与函数：**`backend/server/presets/openai-chat.json:30` 只设置 `usagePath`，未设置嵌套别名；`backend/relay/custom_protocol.go:1439` 的 `customUsageAtWithAliases` 默认只从顶层候选字段读取 cached/reasoning，具体见 `backend/relay/custom_protocol.go:1454` 和 `backend/relay/custom_protocol.go:1455`。

**最小 usage：**

```json
{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":6},"completion_tokens_details":{"reasoning_tokens":5}}
```

将其放进 Chat 响应的 usage。`TestAuditPresetUsageDetails` 得到 InputTokens=10、OutputTokens=8、TotalTokens=18，但 CachedInputTokens=0、ReasoningTokens=0；预期后两者应为 6 和 5。

**根因、归属与影响：**`31ac1eb` 新增标准预设未使用已有 alias 能力填补通用默认字段的限制。已经证明映射结果丢失细分统计；不把总量 18 正确的情况描述成总 token 计算错误，也没有证明金额结算或所有数据库字段都一定因此错误。

**修复方向与回归：**例如为 cached、reasoning 分别设置 `prompt_tokens_details.cached_tokens`、`completion_tokens_details.reasoning_tokens`；其他三种预设分别按自己的 usage 结构核对，不能照搬 Chat 路径。覆盖流式尾部 usage、非流式 usage、零值、缺失值和下游明细保真；缓存创建量等未复现类别另行验证。

### DBG-016 / P2：omitIf 未比较最终渲染值

**位置与契约：**`backend/relay/custom_mapping.go:579` 的 `customOmitRuleHits` 在 `backend/relay/custom_mapping.go:585` 从原始 `maheshvara.<field>` 取值；调用点 `backend/relay/custom_protocol.go:1141` 已经有渲染结果，却没有把该字段最终值传入。`docs/protocol-definition-reference.md:48` 明确描述为“渲染后值类型化等于给定字面量”。

**最小请求体定义：**

```json
{"flag":{"field":"seed","default":0,"omitIf":0}}
```

**触发条件与结果：**请求有 model 但没有 seed。`TestAuditOmitIfRenderedDefault` 预期 `{}`，实际 `{"flag":0}`。模板默认值已经生效，省略规则却因为原始字段不存在而不命中。

**归属与影响：**`ee02476` 新增回归；使用默认值或值转换的映射可能向上游发送本应省略的控制参数。本次运行复现的是 default=0，其他转换组合为建议回归项，而不是未经测试的已确认分支。

**修复方向与回归：**让 omitIf 对最终渲染字段求值；when 仍对约定的请求上下文求值，避免混淆两种语义。覆盖缺失、显式 null、false、0、字符串 "0"、默认值和 mode 转换，并断言校验阶段与执行阶段一致。

### DBG-017 / P2：条件省略数组项时下标移动，后面的项漏删

**位置与函数：**`backend/relay/custom_protocol.go:1141` 的 `renderCustomTemplate` 按编译得到的原路径依次执行删除；`backend/relay/custom_mapping.go:618` 的 `deleteCustomPathValueForce` 删除数组项会立即压缩数组。

**最小请求体定义：**

```json
{"items":[{"field":"model","when":{"path":"stream","op":"isTrue"}},{"field":"model","when":{"path":"stream","op":"isTrue"}}]}
```

**触发条件与结果：**请求 model="m"、stream=false。`TestAuditConditionalArrayDeletion` 预期 `{"items":[]}`，实际 `{"items":["m"]}`。删除原 index 0 后，原 index 1 已移到 0，但下一条规则仍访问 index 1，因越界无操作而漏删。

**归属与影响：**`ee02476` 新增回归；本来被条件禁止的数组内容仍会发给上游。本项是路径删除顺序问题，不是 DBG-016 的值比较问题。

**修复方向与回归：**优先在树渲染阶段决定是否保留元素，或按同一父数组从后往前应用删除并处理嵌套路径；避免所有数组一概靠字符串路径排序。覆盖连续删除、交替删除、嵌套数组以及 omitIfEmpty 与 when/omitIf 同时启用时的路径稳定性。

## 4. 待验证风险与明确排除项

下列条目不计入 17 项已确认缺陷，不授予未经验证的安全影响或生产发生概率。

| 风险 | 位置与现有证据 | 尚缺条件、下一步 |
| --- | --- | --- |
| R-01：缺失/null 与 isEmpty 的契约不够一致 | `backend/relay/custom_match.go:115` 的注释与 `backend/relay/custom_match.go:128` 的提前返回存在解释差异；缺失值除特定操作外直接不成立 | 明确缺失、null、空串是否应同义；现有部分测试/说明体现了有意区分，不能仅凭直觉判 bug。补齐操作符真值表和校验/执行对照 |
| R-02：大整数比较精度 | `backend/relay/custom_match.go:219` 将 json.Number 转 Float64 比较 | 对超过 2^53 的相邻整数作边界测试，并确认产品是否要求精确整数比较；本轮未建立端到端误匹配复现，不宣称可绕过授权 |
| R-03：慢速模型刷新可能覆盖刚轮换的多 Key 列表 | `backend/server/model_refresh.go:168` 用刷新开始时的 source.APIKeys 整体写回；`backend/storage/store.go:630` 整列更新 | 需要用同步屏障让刷新暂停，在管理接口完成轮换/禁用后再释放，断言更新是否丢失；确认所有写入和锁的路径后再定性 |
| R-04：后台刷新排队和关停预算 | `backend/server/refresh_jobs.go:90` 先等待信号量，之后才在 `backend/server/refresh_jobs.go:93` 建立超时上下文 | 需要满队列、关停、慢速源的联合测试，确认是否有任务长期等待或在 store 关闭后继续工作；未做完整 goroutine/任务退出验证 |
| R-05：SSE 首包前的 TLS/响应头等待可能不受读取 idle timeout 覆盖 | `backend/relay/secure_dial.go:61` 创建的 Transport 没有显式 TLSHandshakeTimeout/ResponseHeaderTimeout；`backend/relay/custom_protocol.go:917` 的 Do 发生在 SSE 读取前 | 用本地阻塞 TLS/响应头上游和有界测试 deadline，验证外层请求超时、客户端取消能否覆盖全部路径；不能把普通 SSE 空闲超时用例通过当成证据 |
| R-06：注册表克隆未深拷贝全部新增字段 | `backend/relay/custom_protocol.go:1546` 的 cloneCustomProtocol 与 `backend/relay/custom_protocol.go:1580` 的 cloneCustomStreamMapping 中，新嵌套指针/切片存在共享的可能 | 需证明生产调用者会修改取得的配置，并建立并发读写复现；当前仅浅拷贝迹象不足以声称真实 data race |
| R-07：预设的其他目标契约差异 | tool-choice 对照同时观察到非流式输出 `stream:null`；`backend/relay/custom_protocol.go:786` 的模型发现请求只采用 models 自己的 headers，而 Anthropic 预设 `backend/server/presets/anthropic-messages.json:50` 未带生成请求中的版本头 | 明确目标提供商对 null 和模型发现头的要求，进行无付费操作的契约验证。未观察真实 HTTP 拒绝，不并入 DBG-013 已确认范围 |

**本轮明确没有作为 bug 上报的项目：**

- 预设只在协议表为空时播种，是 `backend/server/protocol_presets.go:47` 所定义的行为；已有用户协议时不强行覆盖，不能当作“升级没有全部预设”的缺陷。对删除后重播种等产品策略应另行明确。
- first-match 帧选择是明确约定；DBG-007 指向标准预设对复合帧的适配，而不是要求所有规则都执行。
- 候选重排曾作为疑点验证，`TestAuditCandidateReorderPreservesSet` 在 HEAD/基线均通过，没有重复/丢失候选的复现。
- 普通编辑保存的高级字段样本完整保留；没有证据支持“前端全局静默丢弃所有新字段”。但并未跑遍每个嵌套字段的载入、预览、复制、保存、重开组合。
- CGO/race 工具链失败是环境限制，不计为产品 bug；原有 Playwright 的 11 个资源相关失败则有提交字节与单变量对照证据，属于 DBG-003，而不是笼统归为环境波动。

## 5. 覆盖范围、执行结果与限制

### 5.1 实际源码覆盖

“全项目检查”采用风险导向的调用链检查，不代表每个未变更文件逐行证明正确。backend、前端 src、scripts、.github 范围共枚举约 277 个版本化文件；本次重点深读以下 23 个变更文件，并在相邻链路做静态抽查与既有测试验证。相邻仓库、第三方依赖源码和生成物未纳入逐行审查。

| 变更组 | 已核对文件 |
| --- | --- |
| 条件、模板、提取与流状态机 | `backend/relay/custom_mapping.go:1`、`backend/relay/custom_match.go:1`、`backend/relay/custom_protocol.go:1`、`backend/relay/custom_protocol_mapping.go:1`、`backend/relay/custom_stream_mapping.go:1` |
| 配套 relay 测试 | `backend/relay/custom_configurability_test.go:1`、`backend/relay/custom_frames_advanced_test.go:1`、`backend/relay/custom_match_test.go:1`、`backend/relay/custom_stream_features_test.go:1`、`backend/relay/custom_stream_frames_test.go:1` |
| 预设与服务端接入 | `backend/server/presets/anthropic-messages.json:1`、`backend/server/presets/gemini-generate.json:1`、`backend/server/presets/openai-chat.json:1`、`backend/server/presets/openai-responses.json:1`、`backend/server/protocol_presets.go:1`、`backend/server/protocol_assistant.go:1`、`backend/server/server.go:1` |
| server 配套测试 | `backend/server/custom_stream_drain_test.go:1`、`backend/server/preset_protocols_test.go:1` |
| 前端及文档 | `packages/webui/src/lib/types.ts:1`、`packages/webui/src/pages/protocol-designer/index.tsx:1`、`docs/custom-protocol-dashscope.md:1`、`docs/protocol-definition-reference.md:1` |

| 模块/调用链 | 实际检查内容与证据层级 | 主要限制 |
| --- | --- | --- |
| 自定义请求构造 | Match 运算、缺失/null/零值、别名路径、when/omitIf、默认值、request.shape、鉴权、URL 构造；静态深读 + 既有测试 + 针对性复现 | 数值极值、所有嵌套路径与转换排列未穷尽 |
| 自定义 SSE | 事件名、payloadPath、谓词帧、映射继承、分类型 delta/cumulative、工具关联、finish/EOF/drain；运行了原有流测试及新增多工具/复合帧/尾部错误复现 | 真实网络断连矩阵、长时并发、所有重复/累计参数序列未穷尽 |
| 四种预设 | 请求/认证配置、普通响应、SSE、usage、工具；与内置转换器及 decoder 调用链交叉检查 | 已复现问题集中在 custom 预设路径；未对四种线制所有多模态组合做 4×4 端到端对照 |
| 播种、注册及存储 | 首次空表播种、用户编辑不覆盖、注册/克隆、持久化调用链及既有测试 | 未做真实桌面/服务进程的完整重启循环；R-06 尚缺并发验证 |
| 请求与安全 | server/admin 路由鉴权、Token 组权限、Key 处理、出站限制、大小限制、错误/日志链路；安全相关既有测试参与全量运行 | 确认 DBG-001/002；不构成完整渗透测试，也未证明不存在其他 SSRF/凭据泄露 |
| 路由与运行状态 | 候选选择/重试、流开始后的失败、额度预留与结算、route cache、热重载、后台刷新/关停；静态风险抽查及全量 Go 测试 | 并发 Key 更新、刷新排队、race 和长时资源泄漏尚未完成独立验证 |
| SQLite/usage/config | 事务和删除级联、迁移、加密入口、聚合统计、usage 异步写入/缓存/保留清理、配置读写；临时库测试 | 不读取真实库；未做大数据量迁移/磁盘故障/崩溃恢复演练 |
| 前端管理 | auth/API/hooks、源表单、协议设计器与字段树、保存/复制、现有导航筛选测试；实际浏览器请求断言 | 预览、每种高级字段和每类表单的完整往返未逐一建立新用例 |
| 构建与交付 | Dockerfile、静态资源校验/嵌入、standalone 构建脚本、release workflow；归档与原工作区构建对照 | 没有 Docker CLI；未执行完整镜像/安装包交付 |
| macOS 原生 | `scripts/macos-app/main.swift:1`、`scripts/macos-app/MacSupport.swift:1` 及构建/更新验证、回滚相关代码的静态抽查 | Windows 上不能运行 Swift/Cocoa、签名、公证、原生更新和 native tests，不宣称这些路径运行安全 |

### 5.2 工具链与已执行命令

环境为 Windows/amd64；Go `go1.26.5`（项目 go.mod 声明 1.25），Node `v24.18.0`，npm `11.16.0`。默认 `CGO_ENABLED=0`；有 LLVM clang，但没有找到 gcc 或 Docker CLI。

下表“原有测试”均指新增临时审计用例之前，或显式排除审计 spec 的运行结果。

| 位置/阶段 | 命令 | 结果与解释 |
| --- | --- | --- |
| 未改动 HEAD 快照 `backend/` | `go test ./... -count=1 -timeout=10m` | 通过；config、relay、server、storage 全部通过，main/webui 无测试；不是 race 结果 |
| 未改动 HEAD 快照 `backend/` | `go vet ./...` | 通过，无诊断输出 |
| 未改动 HEAD 快照根目录 | `npm.cmd run lint --workspace @root/webui` | 通过，未使用 --fix |
| 未改动 HEAD 快照根目录 | `node node_modules/typescript/bin/tsc --project packages/webui/tsconfig.json --noEmit` | 通过 |
| 未改动 HEAD 快照根目录 | `npm.cmd run build:webui` | 失败，gzip payload checksum mismatch，DBG-003 |
| 原工作区根目录 | `npm.cmd run build:webui` | 通过；只产生忽略的构建产物，不能替代干净提交构建结果 |
| 未改动 HEAD 快照根目录 | `npm.cmd run test:e2e --workspace @root/webui` | 原有 30 项：19 通过、11 失败；共同资源原因见 DBG-003 |
| 临时 HEAD 快照仅替换 gzip 后 | `npm.cmd run build:webui` | 通过；这是控制实验，不代表提交已修好 |
| 同上 | `npm.cmd run test:e2e --workspace @root/webui -- login-motion.spec.ts navigation-and-filters.spec.ts` | 原有 30 项全部通过，38.3 秒；显式未运行 audit-protocol.spec.ts |
| 临时快照 `backend/` | `go test ./relay ./server -run '^TestAudit' -count=1 -v -timeout=2m` 及补充单项执行 | 针对性断言暴露本报告缺陷；候选重排控制用例通过。最终工具索引历史对照另有单项日志 |
| 基线快照 `backend/` | `go test ./server -run '^TestAudit' -count=1 -v -timeout=2m` | 4 个历史缺陷断言失败：授权扩大、Key 泄露、错误假成功、工具索引；重排控制通过 |
| 临时 HEAD 快照前端 | `npm.cmd run test:e2e --workspace @root/webui -- audit-protocol.spec.ts --project=chromium` | 2 个缺陷断言失败，1 个高级字段保留控制通过 |
| 基线快照前端 | 同一 audit spec，限定 JSON 保存与字段保留用例 | JSON 保存断言失败；字段保留控制通过；不运行基线不存在的复制入口 |
| 基线快照根目录 | `node scripts/login-trace/verify.mjs` | 同样 payload checksum mismatch |
| 最小 Docker COPY 等价目录 | `node ../../scripts/login-trace/verify.mjs` | MODULE_NOT_FOUND；仅验证脚本依赖缺失，不是实际 docker build |
| 临时 HEAD 快照 `backend/`，设 CGO=1、CC=clang | `go test -race ./relay -run '^TestCustomMatchEvalOps$' -count=1` | 未进入测试，编译失败：clang 不支持 MSVC target 下的 `-mthreads`；无法提供 race 结论 |

Playwright 设置 `CI=1`，不复用已有服务；`ELYSIA_DEV_HOST=127.0.0.1`，`ELYSIA_DEV_PROXY=http://127.0.0.1:1`，避免开发代理误触本机实际后端。浏览器为已安装 Chromium（构建号 1243），涉及管理接口的新增用例全部模拟响应。

### 5.3 证据文件与复查方式

所有下列文件仅在 `<audit>` 临时目录；不作为本次原仓库补丁的一部分。上文已保留关键输入、输出与调用链，因此即使临时目录后续清理，报告仍能用于重新编写最小复现。

| 文件（相对 `<audit>`） | 内容 |
| --- | --- |
| `head/backend/server/audit_debug_test.go` | 四种预设的映射/请求对照及两个 handler 流式复现 |
| `head/backend/server/audit_legacy_test.go` | 四个历史缺陷及候选重排控制；同文件复制至 baseline 运行 |
| `head/backend/relay/audit_debug_test.go` | omitIf 默认值与数组条件删除 |
| `head/packages/webui/tests/audit-protocol.spec.ts` | JSON 首次保存、复制冲突、字段保留控制；基线复用适用用例 |
| `logs/go-test.txt`、`logs/go-race-probe.txt` | 原有 Go 全量通过结果、race 工具链失败 |
| `logs/audit-go-head-complete.txt`、`logs/audit-go-head-final.txt`、`logs/audit-legacy-index-head.txt`、`logs/audit-group-head.txt`、`logs/audit-go-baseline.txt` | HEAD 针对性测试与基线归属对照；complete 为所有当前 Go 审计用例的最终合并运行。不要把这些刻意暴露 bug 的失败计成原有 Go 测试失败 |
| `logs/audit-e2e.txt` | 模拟上游经 Go handler 后的最终 SSE 输出 |
| `logs/webui-lint.txt`、`logs/webui-build.txt`、`logs/webui-build-worktree.txt`、`logs/webui-build-asset-control.txt` | lint、归档失败、本地构建及单资源替换对照 |
| `logs/playwright.txt`、`logs/playwright-asset-control.txt` | 原有浏览器测试的 19/11 与 30/0 对照 |
| `logs/audit-playwright.txt`、`logs/audit-playwright-baseline.txt` | 设计器缺陷的首个 PUT payload 与基线对照 |
| `logs/baseline-assets.txt`、`logs/docker-stage-missing-script.txt`、`logs/submitted-trace.bin.gz` | 基线坏资源、缺失脚本和提交原始损坏资源 |

原有 go vet 和 TypeScript 检查没有错误输出；不虚构不存在的诊断日志。PowerShell 保存的部分文本日志为 UTF-16，可用 `Get-Content` 自动识别。

在临时测试文件仍保留的情况下，可按以下方式重跑部分已确认缺陷；这些断言在未修复快照中预期失败：

```powershell
$audit = 'C:\Users\xiaolan\AppData\Local\Temp\elysia-debug-20260918-c982b78'
Push-Location "$audit\head\backend"
go test ./relay -run '^TestAudit(OmitIfRenderedDefault|ConditionalArrayDeletion)$' -count=1 -v
go test ./server -run '^TestAudit(QueryAuthSecretInDownstreamError|DeleteLastAllowedGroupDoesNotGrantAll|CustomErrorResponseStatus|LegacyToolIndexCollision)$' -count=1 -v
Pop-Location
Push-Location "$audit\baseline\backend"
go test ./server -run '^TestAudit' -count=1 -v
Pop-Location
```

注意当前 `<audit>/head` 已包含用于控制实验的完好 gzip。若要重新证明干净提交的资源缺陷，应从未改动的归档或 `logs/submitted-trace.bin.gz` 在另一临时目录验证，不要把坏字节复制到原工作区。

### 5.4 未执行项目与结论边界

- 没有真实付费上游调用，也没有真实提供商返回状态码的实测；协议契约判断主要依据项目内置转换/解码器、固定输入、字段关系和预设声明。本轮在线官方文档检索未取得可引用正文，因此不声称已逐项完成外部最新规范核验。
- 没有实际 Docker 构建、macOS 原生运行、签名/公证、自动更新安装或完整发布流水线；相关部分仅静态审查和可移植的局部验证。
- 没有 race 检查结果、压力测试、长时间泄漏观察、真实崩溃恢复或生产数据迁移；普通测试通过不等于并发安全或数据恢复安全。
- 基线只运行适用的缺陷归属用例和资源检查，没有对基线重新执行全部后端/前端测试矩阵。
- 没有 fresh npm install、第三方依赖源码逐行检查或依赖漏洞扫描；不能从本报告推断依赖供应链安全。
- 使用原工作区构建仅验证“本地完好资源会掩盖问题”；最终交付仍以版本化内容为准。原仓库业务代码、API、类型和数据库结构均未修改。

## 6. 建议修复顺序与交付验收

1. **先阻止权限和凭据风险：DBG-001/002。**补充权限语义及错误脱敏的回归测试；必要时在正式修复前避免删除受限 Token 的最后一个组，并避免向不受信任调用者开放 query-key 协议的原始错误文本。
2. **恢复可复现交付：DBG-003/004。**分别修正版本化二进制和 Docker 构建输入；两项独立验证，以新干净检出为准，而非当前能成功的工作目录。
3. **修正中转正确性：DBG-005/006/007/008，再处理 DBG-009/010。**重点以最终线制断言测试工具身份、索引、正文、结束与错误，不能仅验证内部 decoder 不报错。
4. **修正编辑与剩余映射：DBG-011 至 DBG-017。**对首次保存、复制冲突、目标工具选择、块提取、usage 与数组省略建立最小回归；逐项修复，不借审计扩大为无关重构。
5. **补足高价值验证缺口。**在兼容的 C 工具链、Docker 与 macOS runner 中补测，优先验证 R-03/R-04/R-05；对无法证实的候选风险维持独立跟踪，不提前当作已确认漏洞发布。

本次交付物仅为 `docs/debug-report-2026-09-18.md`。不提交、不推送，不向原仓库保留审计测试或业务修复；所有后续修复建议均未在本次执行。交付检查确认 HEAD 未改变，原仓库仅新增本报告；报告 UTF-8、问题编号、引用文件/行号范围及尾随空白检查通过，已跟踪文件的 `git diff --check` 无异常。
