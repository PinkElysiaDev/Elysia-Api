# Changelog

本文件记录 Elysia-API 每个版本的完整发布说明，新版本在前。每个版本小节由
两部分组成：`<!-- release-details -->` 标记之前是面向使用者的简明发布正文
（发版时由 `.github/workflows/release.yml` 提取为 GitHub Release 正文），
标记之后是完整技术细节。发版前必须先在此写下该版本的小节，否则发布流程
会失败（这是有意为之，避免发布空说明）。

v1.1.0 及更早版本的说明先于本文件存在，未收录于此；自 v1.1.1 起的全部
发布说明已合并进来，根目录不再保留按版本拆散的 `RELEASE_NOTES_v*.md`。

## Unreleased

### 多 Key 源两处边缘修复

核验确认客户端转发主路径已正确实现「哪个 key 拉取到的模型就由哪个 key 服务、多 key 命中按配置策略在许可池内调度」。本次补齐两个边缘缺口：

- **AI 助手按许可选 key**：助手调用此前固定用 models 行冗余列（首个有效 key），
  多 key 源里目标模型只被其他 key 拉到时必失败；现按装配层同款
  KeyAllowsModel 判定选 key，无 key 可服务时给出明确错误。
- **减 key 后清残留权限**：多 key 减为单 key 后，旧 per-key 拉取集不再更新，
  上游新增模型会被挡在组外；单 key 刷新成功即清残留（含停用 key），恢复
  「不限制」语义。

### AI 助手缺陷修复（工作过程/交互闭环/压缩回归）

上一节功能的审查修复，四个补丁各自可独立编译：

- **ask_user 与方案确认打通**：方案型待批不再被恢复守卫与存储读回双双丢弃
  （确认按钮曾必 409）；脱敏副本保留 kind/question/plan，前端能选对卡型；
  审批端点透传用户作答；作答后同批其余调用合成取消结果，杜绝悬挂
  tool_calls 引发的上游 400；恢复完成即清待批动作；方案无变化时定稿标志
  仍然生效；allowCustom=false 不再被序列化吞掉。
- **摘要压缩两处纠偏**：切点回退到最近的用户消息边界，保留段的工具结果
  永远跟着自己的 tool_calls；摘要移到轮次开始做，边界 seq 精确等于被摘要
  前缀，下一轮不再静默丢失未摘要的近期上下文（顺带消除轮内每轮全表读）。
- **前端交互**：跳底浮标钉在视口（原先随内容滚走）；发消息恢复自动跟随；
  压缩提示随轮次结束清除；现场工具卡失败自动展开；耗时走本地平滑计时；
  助手消息的工具 chips 用中文名；方案「需要修改」不再强制备注。
- **防御**：未声明超时的工具获得 120 秒默认硬顶，阻塞在 Execute 里的调用
  不再把会话钉死在 running；并行组写明「不得修改共享会话态」契约。

### AI 助手：工作过程、上下文与交互闭环

借鉴编码代理的工具过程展示与上下文管理，补齐助手的执行可见性与长会话成本：

- **安全出口**：工具参数里的密钥类字段（含 authorization/credential）在 SSE
  事件与会话视图脱敏，落库的待批动作仍保留原文供批准后执行；门控权限键
  常量化，注册未知键直接失败；SSE 每 15 秒发注释心跳。
- **工具执行**：工具声明只读/可并行/风险/结果预算；同批只读工具并行（上限 4）；
  超限结果按头或尾截断并保持合法 JSON；执行超过 5 秒发耗时心跳。
- **工作过程**：聊天里的工具行统一为动作名 + 状态动词 + 耗时，运行中用渐变
  文字，失败可复制错误；审批卡可补充测试凭证；方案更新在对话里给一行提示。
- **上下文**：每次模型调用上报水位；较早的大工具结果在高水位时换成占位；
  超过窗口九成且历史够长时，把最早一半摘要落库并在后续轮次替代原文。
  已有草稿后不再重复注入完整协议范例。
- **交互闭环**：ask_user 让模型暂停提问，用户点选项或自定义作答后继续；
  计划模式的方案可标记定稿，确认后关闭计划模式并按方案执行，修改意见回注模型。

### 全项目代码质量轮（行为保持重构）

四波 14 个提交的可读性/可维护性治理，全部以「纯重构、测试全绿」为门禁：

- **死代码与字面量常量化**：删除零引用的类型/字段/函数（Registry.Names、
  SSEEvent.ID/Retry、chatUsageToResponsesUsage 等）；散落的超时/轮询/哨兵
  字面量集中为命名常量；localStorage 键名统一进 storage-keys.ts。
- **重复消除**：用量别名四表合一（usage_aliases.go）、九路径字段枚举表驱动
  （response_direct_fields.go）、工具参数累积与 prefix-diff 共享实现、
  normalizeToolChoice 合并两转换器；server 侧 failResult/drainUpstreamError/
  newSSEWriter/toolStore 等样板收敛；agent.ToolError 替换 86 处手写字面量；
  webui 提取 JsonBlock/Collapse/defaultGroup/agentUsageToTurn 等共享原子。
- **兜底与守卫治理**：删除不可能触发的兜底（nil-receiver、恒真守卫、上游
  已校验的下游重复校验）；八连 ALTER 与指针补丁链改表驱动/清单循环；权限
  规范化收敛到写入单点；webui SourceForm 类型收窄一次性消除 20+ 处 `?? []`。
- **长函数与文件重组**：maheshvara_convert.go（3767 行）按请求入/出、响应、
  用量、共享助手五拆；四个流解码器按事件族方法化；server 提取
  openUpstreamStream/buildTargetBody/expandModelRef；agent 引擎拆出
  appendUserMessage/resumeApprovalPrefix/handleCallFailure；storage 按域拆出
  migrate/sources/models_groups/usage_logs/usage_aggregates/assets/system_logs；
  webui 拆出 source-form 辅助层、log-detail 五模块与
  useDraggablePanelWidth/useRuntimeConfigForm/useComposerAttachments 钩子。
- **顺手修复**：usage reset 成功响应统一 admin 封套；agent 测试 fakeStore
  补齐 SessionStateUpdate.Plan 契约；retention TTL 边界测试去偶发（同毫秒
  平局被严格 `<` 保留）。

### 预置协议完整能力（与内置四线等价）

- **shape 全量整形**：自定义协议的 `request.shape` 除消息/工具外，现在把
  思考/推理配置一并按平台整形进模板上下文——anthropic budget 量化与思考态
  温度强制、chat reasoning_effort、gemini thinkingConfig、responses effort
  省略规则与 encrypted_content include 联动。模板作者不再需要复述平台特例。
- **DSL 新能力**：响应映射新增 signature/encryptedContent/refusal/citations
  路径与 signatureProvider 常量；流帧新增 toolDone（参数完成信号，终态统一
  补发）；签名/拒答/引用注解事件补全；工具别名支持 thoughtSignature；
  用量别名支持 cache_creation/cache_read。思考签名跨轮回传自此可行。
- **四份预置升到 v2**：chat +reasoning_effort/stop/parallel_tool_calls/user
  与拒答/签名帧；anthropic +thinking/output_config/签名/引用/参数完成帧与
  缓存用量；gemini +thinkingConfig/toolConfig/topK/stopSequences、
  thoughtSignature、**模型发现配置**（此前完全缺失）；responses +include
  联动与拒答/签名/参数完成帧。
- **预置版本升级机制**：未被用户改动（内容哈希匹配上一版）的预置行启动时
  自动升级；改过的保持不动，删除后重启即获新版。
- **parity 金样测试**：全特征请求体、四平台流转录、非流响应在内置线与
  预置协议之间逐项对照等价；agent 端到端覆盖 anthropic 预置工具轮与两轮
  思考签名。

### 深度审计修复：24 项（引擎/调用方/前端/存储）

**引擎正确性**
- 超限工具结果截断产出非法 JSON 导致落库失败，历史留下无结果的 tool_calls、
  会话后续每轮被上游 400 拒绝——截断改为合法 JSON 信封。
- 审批补交的 APIKey 只落库未同步内存，首次批准的实测拿旧/空 key 跑。
- 批量调用一次审批后整批绕过 never 权限与计划模式（如 never 的
  save_protocol 跟着批准一起执行）——批准只豁免 ask 暂停，禁令逐调用复核。
- Stop 与轮次启动的 cancel 数据竞争（假成功窗口）；崩溃遗留的 running
  会话启动时复位（此前永卡）；轮次主体全程 panic 防护（此前直接崩进程）；
  approval_required 事件改用可靠送达通道（慢客户端下丢失后流静默结束）。
- create_model_source 重名 slugID 静默覆盖既有源（含 API key）——改为拒绝。

**模型调用方**
- OpenAI 线终态即返回，规范要求的 usage 尾帧永远读不到（token 统计为空）
  ——终态后排水到 EOF 只吸收用量；测试与冒烟 mock 同步改为独立尾帧。
- Responses 平台被注入 Chat 专属 stream_options（严格上游 400 不重试）。
- 自定义协议终态后的排水帧违反契约，重复文本/迟到失败会污染结果。

**前端**
- 工具卡与落库结果双渲染（turn_done 不清现场残留）；waiting_approval
  刷新/切会话后审批卡丢失导致永久无法批准（pendingAction 回灌）；模型源
  删/移 key 后模型↔key 映射错位（错凭证服务）。
- 报错横幅可关闭（原先无出口、被抑制的落库红卡也回不来）；确认方案双击
  不再触发假 409；附件按 base64 膨胀计总预算、预检失败恢复草稿文本；
  StrictMode 双建会话；KeyModelsPanel 编辑不再被覆盖；0ms 展示为 —。

**安全与存储**
- 出站策略改动落盘失败回滚内存与运行时下发（防内存/磁盘分叉）；运行配置
  保存只回传改动过的块（防旧快照覆盖 agent/手改配置），热重载后刷新表单。
- 工具入参中的密钥字段（apiKey/token/secret）持久化前脱敏；消息渲染的
  平台解析记忆化（长会话 N+1 查询）；预置协议改名迁移不再因旧协议行被删
  而留下悬空 platform 引用。

### 安全与 AI 助手：出站策略重构、调用复用与日志补全

- **SSRF 防护改为可配置禁止 IP 段列表**：原「固定私网段 + Fake-IP 放行开关」
  重构为配置驱动的 CIDR 黑名单（`outbound.deniedIpRanges`，预置默认 =
  原固定语义）。运行时配置页提供一行一段的编辑器（逐条校验、恢复默认、
  清空警示）；上游是本机/内网服务（如 127.0.0.1）被拦截时，删掉对应段
  即可放行。AI 助手新增 `update_outbound_policy` 工具（审批后生效），
  遇到 "refused to dial denied IP" 可代为调整。旧 `allowFakeIPOutbound`
  配置自动迁移。
- **AI 助手模型调用复用网关转发管线**：请求渲染、端点拼接、鉴权头、HTTP
  客户端全部改用 relay 适配器（与线上转发同一实现），修复自定义协议平台
  的模型源被错按 OpenAI 线制发送、以及 Anthropic/Gemini 流被错误解码两个
  隐患；OpenAI 系上游补 `stream_options.include_usage`，用量统计更完整。
- **模型调用写入调用日志**：每次调用（含失败）各记一条（key=AI 协议助手、
  relay_mode=agent-assist），带真实状态码/耗时/首字节；② 后端转发 = 实际
  请求体、③ 上游回传 = 非 2xx 错误响应体；① 下游请求/④ 返回下游按网关
  内部调用约定留空。此前的消息级观察（仅成功、零时长、断连停记）移除。
- **修复附件图片/文档触发上游 400 Invalid base64 data**：data URL 被
  解码成二进制后塞进了期望 base64 文本的字段，Anthropic/Gemini 出口
  原样透传即被拒。现在保留 base64 文本（解码仅作校验与限额）。
- **模型源协议下拉去重**：不再并列展示内置线路与等价预置协议，下拉仅列
  已注册协议（含四个预置）；列表为空时回退内置项，存量源的旧平台值以
  占位项回显。
- **修复报错双条展示**：同一失败既落库系统错误消息又经 SSE 出错横幅，
  两条同文案并排。现在 live 横幅（带重试）存在时抑制同文案的落库红卡，
  关闭横幅或刷新后红卡回归，历史不丢。

### AI 助手：侧栏拖拽调宽与输入区位置微调

- **侧栏宽度可拖拽调整**：展开的侧边栏左缘新增拖拽手柄（hover 显细线），
  实时调整宽度（260–560px 钳制，拖拽中禁用过渡保证跟手），松手写入
  localStorage 并在下次进入时恢复。
- **输入区宽度基线明确**：消息流与输入框同列，始终为工作区可用宽度的
  80% 并居中（窗口尺寸、侧栏开合与拖拽全程动态重算；侧栏挤占空间时
  聊天列平滑让位，永不超过 80%）。
- 输入框更贴近页面下边缘（底部留白 16px → 8px；页面自身底部空间
  72px → 16px，总间距 72px → 24px，不产生页面滚动）。

### AI 助手：会话总览页、对话轮数条与草稿还原点

- **总览 ⇄ 工作区两级结构**：AI 助手页先展示会话卡片网格（状态圆点、方案
  进度、计划模式标记、相对时间、悬停删除），点击卡片或「新建任务」以过渡
  动画进入工作区（即原交互页），返回不中断进行中的轮次；协议设计器的
  AI 生成/修改入口自动建会话并直接进入工作区。
- **对话轮数条**：工作区左侧新增竖向导航——每轮对话一个带序号的圆点，
  点击平滑滚动定位到该轮；当前视口所在轮 rose 高亮（视口中线判定），
  运行中在底部显示呼吸点。
- **草稿还原点（后端）**：每轮对话开始前快照当前配置草稿（`draft_restore`
  列，单槽覆盖）；新增 `POST /agent/sessions/:id/restore-draft` 把草稿回滚
  到上一轮修改前（进行中 409、无还原点 409）。配置页在还原点与当前草稿
  不同时显示「还原到上一轮修改前」（确认弹窗）。
- 输入区改为占当前可用宽度 80% 并居中（随侧栏开合平滑变化）；模型选择
  浮层搜索框去掉全局玫红 :focus-visible 描边，聚焦无高亮。

### AI 助手输入区细节修正

- 消息流与输入框在当前可用宽度内居中（上限 720px），侧栏折叠时不抵页面
  右缘、展开时随之收窄，宽度过渡全程保持居中；页头仍固定贴左。
- 模型选择浮层重设计：搜索框改为项目统一的凹槽胶囊形态（well 底、全圆角、
  无聚焦描边），移除触发器的玫红聚焦环；权限/思考菜单触发器同步改为中性
  展开态。
- 上下文占用指示器移至模型选择左侧；输入占位精简为「请描述您的任务」。

### AI 助手输入区与侧栏动效精修

- **输入卡片浮现动画**：composer 默认有线无底（框线常显、内部透明），hover /
  聚焦时底色填充与 rose 光环淡入；发送按钮默认只有 ↑ 箭头，hover 时圆形
  底色浮现（停止按钮同款）。
- **上下文占用指示器**：控制条新增环形进度图标（jade→amber→ember 按占用着
  色），悬浮展示会话累计 tokens、缓存命中率与上下文占用百分比（按所选模型
  MaxTokens 估算）；替代原统计文本。
- **控制条重排**：模型选择与思考强度移至右侧紧贴发送按钮；思考菜单等级去掉
  「思考 ·」前缀；计划模式改为与三档一致的勾选行（不再是开关）。
- **侧栏开合动画**：侧栏改为常驻的宽度过渡容器（w-80↔w-0，300ms），输入框
  长短随之平滑变化而非跳变；聊天列宽度上限移除，折叠时充分利用页面宽度，
  页头标题位置固定不随侧栏移动。
- 输入区与控制条之间、附件区与输入区之间的分割线移除。

### AI 助手：composer 重排、权限模型与通用标签页侧栏

- **输入框重排（参考 Claude 风格 composer）**：上方为无边框多行输入，底部
  控制条从左到右依次为 `+` 附件、权限控制、会话累计 token 与缓存命中率、
  模型选择、思考强度、圆形发送/停止按钮；清空消息移至页头图标。
- **权限控制合并为一**：删除「写入/出站」两个独立 Seg，换成一个胶囊菜单，
  三档审批频繁程度：变更前确认（写入/出站都询问）→ 自动编辑（写操作自动，
  出站仍确认）→ 完全控制（全部自动）；顶端为可并行的**计划模式**开关。
- **计划模式（闭环）**：开启后引擎拒绝一切写操作与真实出站（拒绝理由回传
  模型引导其产出方案），系统提示同步注入计划模式指令；方案页出现「确认执行
  方案」按钮，点击后关闭计划模式并自动发送确认消息开始执行；对话中的反馈
  即为方案修改意见。新增 `agent_sessions.plan_mode` 列（容错迁移）。
- **思考二合一**：思考开关与推理强度合并为单一菜单，「关闭思考」成为等级
  之一；触发器直接显示当前等级。
- **侧栏通用标签页窗口**：取消「动态/方案/草稿」固定三分，改为按需打开的
  可关闭标签页——方案更新自动开「方案」页、草稿变化自动开「配置」页、点击
  消息中的工具执行行打开「动态」页；配置页分节展示请求构造/响应解析/流式
  映射 + 完整 JSON；动态页支持在近期执行列表中点选查看参数与结果详情。
- **布局与文案**：侧栏关闭时聊天列收窄居中（不再顶到页面右缘）；删除空
  会话时的大段能力描述文案；页头新增「计划模式」状态胶囊。
- 修复：gated 工具被拒时拒绝理由未回传模型（仅 `error:denied`），模型无法
  据此调整行为；现 Data 携带 `message` 字段。

### AI 助手页面融入背景的扁平化重构

- **合并式模型选择器**：替代「模型源 + 模型」两个下拉——单一触发器打开浮层，
  按模型源分组展示、支持搜索（模型名/源名）、视觉模型带 jade「视觉」标注，
  键盘导航 + 点击外部关闭；选中一次保存源与模型。
- **设置集成输入区**：删除顶部设置栏，模型/思考/写入/出站权限全部收进输入
  容器底部控制条；输入容器成为页面唯一抬升元素（focus 时 rose 光环）。
- **页面去机械分割**：三栏移除面板底色与硬边框，内容浮于页面背景、栏间留白
  分隔；消息扁平化——助手消息完全去框（纯文本流 + 左缘图标锚点），用户消息
  保留大圆角 wash 胶囊，工具执行改发丝左缀行。
- **侧栏智能跟随**：文本页签（动态/方案/草稿）+ 自动跟随模型当前活动——方案
  更新跟方案、草稿变化跟草稿、其他工具活动跟动态；点击页签本 turn 固定。
  「动态」页展示最新工具执行的参数与结果详情 + 近期执行列表。

### 预置协议去厂商化重命名

四条默认协议 ID 与名称改为协议式命名：`openai-chat → chat-completions-api`、
`openai-responses → responses-api`、`anthropic-messages → anthropic-api`、
`gemini-generate → gemini-api`（显示名 Chat Completions API / Responses API /
Anthropic API / Gemini API）。`request.shape` 枚举与线制别名不受影响。老库
启动时一次性迁移：旧 ID 行自动改名，`custom:<旧ID>` 的模型源/模型平台引用
同步重写；用户新旧 ID 并存时自定义行优先、跳过不改。

### 预置协议播种改为逐条补齐缺失

旧逻辑只在协议表完全为空时写入四条默认协议——已有自定义协议的老库升级后
永远拿不到预置。现改为启动时逐条检查：预置 ID 不在库中才写入（幂等；库中
优先，不覆盖用户对已有预置的编辑，不触碰自定义协议）。

### AI 助手页面 UI 与交互深度调整

- **凭证随聊天传递**：测试 baseUrl / API key 不再有专门配置框——用户在对话中直接告知，
  助手作为工具参数传入（审批卡片展示参数、key 脱敏），提供过的凭证本会话自动加密
  记住；设置栏只保留模型 / 思考 / 权限。
- **多用途侧边栏**：草稿面板升级为「方案 / 草稿 / 进展」三标签上下文面板——新增
  `update_plan` 工具让助手维护多步任务方案（步骤清单实时展示、随进度更新状态），
  草稿页展示协议 JSON，「进展」页聚合工具执行时间线；面板有更新自动切换标签。
- **设计语言对齐**：替换全项目唯一的 window.confirm 为统一确认弹窗；状态点改用
  项目 Dot 体系（新增 amber 待审批态）；sky/emerald 等偏离色统一为 jade/ember token；
  任意像素字号收敛到 text-2xs/xs 流式阶；代码块统一 bg-code + 边框；用户气泡改
  bg-wash；复制按钮/删除按钮/权限切换复用项目组件（CopyButton / danger / Seg）；
  页面改为视口高度锚定的真三栏应用式布局；补齐 .agent-markdown 排版样式。

### AI 助手通用化、思考等级修复与侧边栏调整

- **通用智能体**：AI 助手不再局限于协议——新增 12 个运维工具（7 只读 + 5 门控写入），
  覆盖模型源 / 模型组管理（创建/修改，复用管理端点同套校验与 SSRF 防护）、
  用量统计与按日趋势（查询结果自带图表规格）、失败请求下钻（四段捕获体 + 重试链）、
  系统日志查询；系统提示词重写为四大能力域（协议接入 / 源与组管理 / 统计分析 /
  错误诊断）。聊天内新增 ```chart 图表渲染（recharts 柱/折线/饼）。
- **修复**：会话设置 PATCH 改为指针增量语义——此前只改思考等级会把思考开关与
  模型选择一并清零。顺带修复 `slugID` 纯非 ASCII 名称兜底 id 在时钟粒度内
  碰撞导致同名配置静默合并的缺陷（兜底 id 加随机后缀）。
- 权限标签语义化：「保存」→「写入」（协议保存 + 源/组写入），「测试」→「出站」
  （上游测试 + 模型拉取）；侧边栏 AI 助手移入「系统」分组。

### 协议助手 Agent 化：从单次生成到全流程对话式接入

一代「AI 助手」是一次性请求（阻塞 300 秒、无历史、无重试、思维链丢弃、
token 不入统计）——本次彻底重构为**内置工具调用 Agent**（Go 原生实现，
复用 Maheshvara 的工具调用 / 思考 / 四线流式解码能力）：

- **全流程闭环**：读文档 → `update_protocol_draft` 写草稿（声明式校验 +
  离线渲染/映射自检）→ 向你提问索要测试地址 → 经**审批卡片**批准后
  `test_upstream` / `test_model_list` 真实测试 → 按测试结果修正 → 经批准
  `save_protocol` 保存生效；编辑模式会话可直接改已有协议。
- **会话持久化与可追溯**：会话/消息全部落 SQLite（断连轮次继续，回来即
  见）；支持复制提示词、失败重试、编辑重发、按消息重新生成、清空历史、
  多会话管理——不再需要刷新页面。
- **流式体验**：SSE 实时推送正文与**思维链增量**（可折叠查看），工具调用
  卡片实时展示执行状态；单会话串行、可随时停止。
- **模型与思考可调**：每会话选择模型源/模型，思考开关 + 等级
  （低/中/高/最高/自适应），映射到各平台的思考参数；思维链全程可查看。
- **用量入账**：Agent 每次模型调用按 key_name「AI 协议助手」记入统计页
  与调用日志（relayMode=agent-assist），可按其筛选。
- **权限分级**：真实测试 / 保存协议两档门控动作默认逐次审批，可设为
  「总是允许」或「禁止」；测试凭证（baseUrl/API key）加密落库、仅本次
  会话使用。
- 模块化设计：引擎（`backend/agent`）领域无关，工具（6 个）可插拔注册；
  一代 assist 端点与面板已移除。


## v1.4.0 - 2026-09-19

这个版本的主题是「**协议引擎数据化**」：格式转换核心全面可配置，四大标准
协议自身也变成了数据定义（预置入库、可编辑可复制），协议设计器获得独立的
映射关系页与临时凭据真实测试；同时修复了一批安全与正确性缺陷，自定义协议
源支持模型自动拉取。

### 一句话导读

> 以前接入一个新的大模型接口要写 Go 代码；现在打开协议设计器，从四个内置
> 协议里复制一份改改就行——AI 助手甚至能照着文档替你填。转换核心里那些
> 「流什么时候算结束」「哪个字段是思考内容」之类的判定，如今全部可以自己
> 配置，不再被写死。

### 转换核心全面可配置（自定义协议引擎）

- **条件原语 Match**：一套统一的「取字段 → 按任意类型的值比较」语义（支持
  等于/包含/在列表内/为真/大于等 15 种操作）。以前流式结束的判定是写死的
  「结束原因字段非空即结束」，遇到每帧都发 `finish: false` 的供应商会在第
  一帧就掐断流——现在可以配「只有值为 true 才算结束」，任何类型的任何值
  都能当截断标签。
- **键名别名可配置**：供应商的用量字段不叫 `prompt_tokens`？工具调用参数
  不在 `arguments` 里？现在可以整体替换内置别名表，支持嵌套路径（如
  `prompt_tokens_details.cached_tokens`），缓存/推理 token 明细不再丢失。
- **异构帧与分帧工具拼装**：一类事件一个形状的协议（如 Responses 型类型化
  事件）可以按事件名或 JSON 谓词逐帧配映射；工具调用的「身份帧 + 参数片
  段帧」分开发出的协议（如 Anthropic 式）也能正确拼装，多工具不再互相串线。
- **条件字段与双路径**：请求字段可以配「仅当开启思考时才携带」这类条件；
  流式请求可用独立路径（覆盖 Gemini 这类按流换动词的端点）；消息/工具与
  标准线制同形时一键复用内置整形，不必手写字段级转换。
- 完整语义见新增的《协议定义参考》文档（`docs/protocol-definition-reference.md`）。

### 四大协议成为预置

- OpenAI Chat Completions / Anthropic Messages / Gemini / OpenAI Responses
  四份协议定义内置于程序中，**首次启动自动写入数据库**，此后就是普通协议
  行：可编辑、可删除、可一键「复制为新协议」当定制基底。四大标准平台照旧
  走内置快速路径，预置是等价性的验证台。
- AI 助手的提示词同步升级：注入全部新能力说明，并以 Anthropic 预置作为
  完整范例，照文档生成的配置质量更高。

### 协议设计器体验

- **映射关系独立页签**：请求/响应体页签回归纯结构编辑（映射位显示为只读
  徽标），新增「映射关系」页签集中分配每个结构位置对应的 Maheshvara 字段，
  行随结构自动增减，页面不再混杂。
- **真实测试支持临时凭据**：填个 base_url 和 key 就能直接试新协议，不必
  先建模型源、先填手动模型；模型发现试拉（不落库）也在测试页一键完成。
- 请求页签重排：HTTP 结构（方法/路径/请求头/查询参数）前置，请求体结构
  殿后，并补上了此前 UI 从未暴露的「流式 Path」字段。
- **自定义协议源支持模型自动拉取**：协议里声明发现端点后，引用它的模型源
  可以开启自动拉取（`fetchBaseUrl` 同步生效）。

### 流式可靠性与正确性修复

- **尾帧用量不再丢失**：OpenAI 兼容流把用量放在结束帧之后的独立帧里下发
  （`stream_options.include_usage`），以前这一帧永远读不到——计费退回估算值。
  现在收到结束信号后继续排水一小段窗口，真实 token 数不再丢。
- 复合帧（正文+结束+用量同帧）不再丢信息；空嵌套映射不再顶掉已验证的顶层
  映射（此前表现为「非流成功、流式 502 无可呈现输出」）；HTTP 200 包业务
  错误的响应不再被包装成空答案的成功。

### 安全与审计修复

- **删除唯一的授权组不再导致扩权**：以前删除某 token 唯一允许的组后，该
  token 会变成「不限制」（可访问全部组）；现在这类 token 会被一并禁用并在
  管理响应中列明。
- **query 鉴权的密钥不再随网络错误泄露**给调用方（错误信息中的 URL 查询
  串统一脱敏）。
- 依据外部深度审计报告修复全部 17 项确认缺陷（含交付阻断：干净检出构建
  失败的 gzip 字节损坏、Docker 构建缺文件；详见
  `docs/debug-report-2026-09-18.md`）。
- 编辑模型源时删除的手动模型现在会真正从列表消失（保存同步此前从不删除
  手动行）。

### 性能

- 自定义协议热路径去重复劳动：注册时一次校验/编译，流式每事件从最多三次
  JSON 解析降为一次，映射预编译——转发延迟与 CPU 占用同步下降。

### 字段级映射模型（本版本前半程交付）

- **请求体**：`request.body` 为"结构即配置"的构造树——容器是普通 JSON
  对象/数组，每个叶子声明对应 Maheshvara 的哪个字段
  （`{"field", "mode": "json|string", "default"?, "omitIfEmpty"?}`）或为
  常量（`{"value": ...}`）。注册时编译为内部渲染表示，运行时链路零改动。
- **返回体**：`response.body` 构造树与请求体对称——按上游示例搭建结构，
  叶子标注对应 Maheshvara 的哪个字段（`text` / `usage` /
  `usage.input_tokens` / `stop_reason` / `metadata.<key>` 等，可选
  transform）；等效行表 `response.fields` 继续支持（与 body 二选一）；
  `response.sample` 保存上游示例响应供点选与离线验证。
- **字段目录单一事实来源**：`GET /api/admin/custom-protocols/schema` 提供
  请求/响应字段目录、transform 与类型约定，UI 下拉、AI 提示词、后端校验
  三方同源。
- 旧模板（`bodyTemplate` / `*Path`）作为 legacy 形态继续兼容加载。

### 协议存入数据库

- 自定义协议持久化到 SQLite（`custom_protocols` 表），保存即校验并原子
  热更新注册表；`config.json` 的 `customProtocols` 键废弃，升级启动时
  一次性导入数据库（同 ID 以库为准）并从文件移除。

### AI 生成 harness

- `POST /api/admin/custom-protocols/assist` 服务端闭环：**生成 → 声明式
  校验 + 编译 + 注册校验 → 失败自动携带 issues 修复重造（默认 2 轮）→
  离线验证**（样例请求渲染 + 示例响应映射，不发起真实请求）。系统提示词
  由字段目录程序化生成；文档/截图/PDF 作为原生多模态输入交给所选模型源，
  凭证不出服务端。助手草稿一键应用到编辑器，验证结果（渲染请求体与映射
  出的 Maheshvara 字段）直接展示。

### 致谢

- 感谢 **@vioaki** 在 PR #26 中带来的 WebUI/macOS 体验改进——登录页重设计
  （Panel Access Token 下划线输入、品牌区精简、悬停粒子强调、主题切换刻印
  形态、立绘呼吸灯）让整个面板的气质上了一个台阶！

老数据无需任何手工操作：协议表为空时自动播种四份预置，已有用户协议原样
保留。完整技术变更见仓库
[CHANGELOG.md](https://github.com/PinkElysiaDev/Elysia-Api/blob/deploy/CHANGELOG.md)。

<!-- release-details -->

自 v1.3.1 以来合入 deploy 的完整技术变更（按主题归纳）：

- **字段级映射模型与协议入库**：`request.body`/`response.body` 构造树 +
  注册期编译；`custom_protocols` SQLite 表 + 原子热更新注册表 + config.json
  一次性迁移；schema 端点成为 UI/AI/校验三方同源的字段目录；AI 助手闭环
  （多模态输入、issues 修复重造、离线渲染验证）；协议设计器页（列表、弹窗
  编辑、渲染预览、真实测试）；协议 `type` 声明（llm/reranker/embedding/x-*）。
- **Match 条件原语与流式终止配置化**（`a7ad068`）：`CustomProtocolMatch`
  15 操作符类型化比较；`stream.finishWhen/statusWhen` 覆盖终止判定；
  `stream.done` 类型化终止值与 `doneValuesReplace`；`stream.eventKeys`
  事件名判别键；终止事件发射移入 Decode（可访问原始载荷求值）。
- **别名/条件包含/模式细分**（`ee02476`）：`aliases`（textKeys/usage/
  toolCall，条目支持点路径）整体替换默认表；请求叶子 `when`（条件成立才
  写入）与 `omitIf`（渲染值等即省略）；`stream.modes` 按字段族混用
  delta/cumulative；`textFilter/reasoningFilter` 元素过滤；模板过滤器
  `|bool |int |string`；叶子支持「目录字段.子路径」。
- **谓词帧与分帧工具拼装**（`c8a4746`）：`frame.match` JSON 谓词选帧
  （Gemini data-only 帧型判别）；`frame.tool` 身份帧/参数帧按 idPath 或
  indexPath 关联拼装，片段原样透传；`request.pathStream` 流式路径覆盖。
- **四协议预置 + request.shape**（`31ac1eb`）：shape 复用内置整形器切换
  模板上下文消息/工具线制形状；四份预置 JSON 内嵌（model_catalog 先例），
  空表播种、库中优先；设计器预置徽标 + 复制为新协议；AI 助手注入能力说明
  与 few-shot 范例；验收即四预置对假上游的流式端到端。
- **debug 报告 17 项缺陷修复**（`42e3aed`/`32e6af4`/`85212eb`/`8b4a4ab`/
  `a8b0662`）：删除唯一授权组的 token 一并禁用；query 鉴权密钥随 `*url.Error`
  泄露统一脱敏；gitattributes 二进制例外 + gzip 原字节重提交（干净构建
  修复）；Dockerfile 补 COPY scripts；流级稳定工具槽位（多工具 index 冲突）；
  Responses 预置身份关联（item_id/call_id 混用）；复合帧全量映射；非流式
  映射错误路由失败路径；单帧多工具（tool.path 数组遍历）；预设错误帧；
  shape=anthropic 的 tool_choice 转换；thinking 文本键；usage 嵌套明细
  别名；omitIf 渲染值比较；条件数组删除逆序执行；JSON 首存旧闭包状态；
  复制唯一 ID。
- **引擎热路径与第四轮质量收敛**（`8546aeb`/`a663fd1`/`69293ac`/
  `11de204`/`f54817e`/`7e8ad7c`/`07ed891`）：注册路径免重校验 + 请求体
  注册期编译；帧 JSON 单次解析 + 映射预编译 + UseNumber 统一；占位符骨架/
  targets 表/状态常量/键格式常量；`ForEachBatch` 排水迭代器统一转发与采样；
  probeTimeout/customProtocolRow/preset 缓存；ad-hoc 测试不再依赖 store；
  前端 `custom:` 前缀归一/`hasDiscovery`/`updateStream`/useApiAction 迁移/
  handleSave 拆分；测试脚手架收敛。
- **空嵌套映射自愈**（`c7e3f52`）：设计器旧版流式开关写入的
  `stream.response = {body:{}}` 不再顶掉顶层映射（运行时空判定 + 注册净化），
  嵌套构造树编辑器就地可用；dashscope 文档补故障排查。
- **映射关系页签与手动模型删除同步**（`78a68f5`/`77fd5f5`）：
  `SyncManualSourceModels` 以手动集为权威删除缺席行（含组引用清理，fetch
  语义不变）；映射关系派生页（collect*MappedLeaves + replaceLeafAtPath）、
  树编辑器 badge 模式、请求页签重排 + 流式 Path 暴露。
- **文档**：《协议定义参考》（Match/别名/条件包含/shape/frames/预置机制/
  边界）；dashscope 接入指南持续更新（流式行为、frames 示例、故障排查）。
- **WebUI/macOS 体验（PR #26，@vioaki）**：登录页重设计（下划线令牌输入、
  悬停粒子强调、刻印形态主题切换、立绘呼吸灯）、macOS 访问体验改进。

## v1.3.1 - 2026-09-06

这个版本的主题是「界面焕然一新」：整套控件换了设计语言，图片预览大升级，
还新增了 ARM 版本、修好了几个烦人的小 bug。

### 新增 Windows / Linux ARM 版本

- 现在提供 Windows ARM64 和 Linux ARM64 独立二进制：ARM 笔记本、树莓
  派、ARM 服务器都能直接运行；发布页共六个平台任选，校验和文件同步
  覆盖全部产物。

### 界面焕然一新（感谢 @vioaki）

- **图片预览大升级**：换成黑底全屏查看器，支持滚轮缩放、拖拽移动、
  底部缩略图条翻页；缩略图完整显示不再裁切，文件名和文件体积常驻展示，
  加载失败可以点重试。
- **控件全面统一**：按钮、搜索框、分段选择、图例等换成一整套胶囊设计
  语言，页面观感更一致；主题切换变成带日/月形变动画的圆形按钮，切换
  主题时有整页光效过渡。
- **一批颜色终于显示对了**：错误文字的橙红色、成功图标的绿色、列表
  悬浮底色等，以前因为透明度写法问题从未真正生效，现在都按设计显示。
- 切换页面改为轻微淡入，动效更安静；退出登录增加二次确认，防止误触。

### 统计与 bug 修复

- 用量统计不再出现幽灵「—」模型行：以前「模型组不存在」这类还没路由
  到模型的失败会被记成一条空模型，现在从模型统计里剔除（调用日志里
  仍可查到）。
- 修复弹窗里的下拉菜单点不开的问题（模型组编辑、模型源表单等处的
  筛选下拉）。
- 修复通过局域网地址（`http://IP:端口`）访问面板时复制按钮无效的问题。

### 致谢

感谢 @vioaki 贡献了本版本绝大部分界面改进——从控件体系、图片查看器
到统计修复，两个 PR 让整个面板焕然一新！

老数据无需任何手工操作。完整技术变更见仓库
[CHANGELOG.md](https://github.com/PinkElysiaDev/Elysia-Api/blob/deploy/CHANGELOG.md)。

<!-- release-details -->

自 v1.3.0 以来合入 deploy 的完整技术变更：

- **新增 ARM64 独立二进制**（`17c1263`）：构建目标扩至六平台（+
  windows/arm64、linux/arm64，CGO_ENABLED=0 纯静态交叉编译）；发布流
  程的产物校验、artifact 上传、SHA256SUMS 与 Release 资产清单同步扩
  充；双语 README 平台表更新。
- **WebUI 控件体系与透明度修复（PR #24，@vioaki）**：统一弹窗/浮层
  圆角与阴影规范；约 17 处 `/N` 透明度从未生效的问题以 color-mix 与
  `<alpha-value>` 修复（品牌色裸 var 定义下修饰符不生成 CSS）；瓷钮
  Seg、胶囊筛选栏、系列色染色 LegendChip；View Transitions 主题切换
  （无支持浏览器降级为逐元素过渡）、切页 150ms 淡入（兼容
  motion-reduce）；登出二次确认；媒体卡片改为完整显示 + 常驻文件名/
  体积底栏 + 失败重试；灯箱重做为黑幕沉浸式（滚轮/双击缩放、拖拽平移、
  缩略图条、相邻图预加载，复用 blob LRU 缓存并新增只读 bytes 查询）；
  操作成功后自动清除模型选择。
- **按钮体系与工具栏（PR #25，@vioaki）**：pill 按钮家族（32px 高，
  主按钮梅釉瓷面，头部次动作降级 ghost）；SearchInput 收敛为凹槽胶囊；
  新增 ToolbarSummary 替代 sources/groups 重复统计块；主题钮改为 32px
  圆形日/月 clipPath 形变（不支持 CSS d 的浏览器降级平移）。
- **用量统计口径修复**（PR #25，`239e80b`）：模型维度四条查询路径
  （raw/rollup × 按日/按模型）统一排除 `model_name` 为空的未路由记录
  ——预路由失败（组不存在/组内无可用模型）属网关级错误，仅保留在调用
  日志与全局 totals；测试断言同步更新。
- **WebUI 修复**：portal 到 body 的 Select/Tooltip 弹层层级升至
  z-[76]，修复 Dialog 遮罩（z-[74]）压住弹窗内下拉的回归（`b26b19d`）；
  `copyText` 剪贴板助手带 execCommand 降级，修复非安全上下文（局域网
  HTTP 访问面板）复制按钮静默失败（`ee75501`）。
- **文案精简**：模型源表单去除「（仅一个 Key 时无差别）」等冗余提示
  （`c5d34f7`）；用量页 KPI 标签去除「（成功调用）」后缀（口径不变，
  `cc1eadb`）。
- **文档**：v1.3.0 发布后补 Docker 自建构建步骤说明（`5d42536`/
  `a678637`）。

## v1.3.0 - 2026-09-04

这次更新的主角是日志：日志可以自动清理、不再疯占磁盘，排查问题时还能
直接在日志详情里看图。同时新增了 Docker 支持。

### 日志管理（新功能）

- 可以给日志设置「保留天数」「最多占多少磁盘」「最多留多少条」，超了
  自动清理；默认全部关闭，想开哪个开哪个。
- 可以限制单条日志里请求体的大小，超长的只保留开头一部分，够排查就行。
- 新增「只保存出错请求」开关：正常请求不再把正文写进磁盘，占用直接
  大幅下降。
- 请求里的图片、音频等大文件不再塞在日志正文里，而是单独存成文件，
  正文只留一个占位符；在详情页点开就能查看原图。

### 修复：同一张图片不再被反复存储

- 以前多轮对话每轮都会重发历史消息，同一张图每次都被原样再存一份，
  一张 2MB 的截图聊 20 轮就要占 40MB。现在相同内容全盘只存一份；删除
  日志时，只有确认没有别的日志还在用它，文件才会真正删除。升级后首次
  启动自动整理旧数据，无需手工操作。

### 超限清理更聪明

- 磁盘只是轻微超出上限时，现在只删很少的日志：每批删多少在 1 到 1000
  条之间自动拿捏，不再固定一刀切掉 500 条，也不会因此清理得过于频繁。

### 日志里的媒体看得更舒服

- 图片默认显示小缩略图，点开是全屏大图预览，还能一键下载；已经看过的
  图会自动缓存，来回切换不再重复加载。

### Docker 支持

- 新增多架构 Dockerfile（amd64 / arm64）：克隆仓库后 `docker build`
  一行命令即可自行构建镜像运行，使用方法见 README 的 Docker 章节。

### 面板改进

- 调用日志新增「缓存命中」标记，列表和详情里都能看到哪些 token 走了
  缓存；
- 统计改为只按成功请求计算 token 和耗时，数字更真实；
- 模型每日调用分布图能展示更多模型、配色更丰富，图表悬停提示不再被
  裁掉；
- 修复小屏幕上按钮错位、macOS 客户端导出日志不完整等问题。

### 其他修复

- 修复流式回复偶发内容损坏、手动清理点了没反应、高并发下配置互相覆
  盖、健康检查改了配置要重启才生效、若干内存与连接泄漏等一批稳定性
  问题。

### 致谢

感谢为本版本提交代码的 @vioaki 与 @LingLambda！

- @vioaki：缓存命中展示、统计口径修正、模型分布图表与多处界面修复；
- @LingLambda：Docker 支持、双语 README 与项目状态徽章。

老数据全部自动迁移，无需任何手工操作。完整技术变更见仓库
[CHANGELOG.md](https://github.com/PinkElysiaDev/Elysia-Api/blob/deploy/CHANGELOG.md)。

<!-- release-details -->

自 v1.2.0 以来合入 deploy 的完整技术变更：

- **用量日志管理**：新增保留策略（按天数 / 磁盘占用上限 / 记录条数上
  限，可任意组合，默认关闭）、单条请求体大小上限（超限截断、全链路生
  效）、`BodyOnErrorOnly` 只落盘失败请求正文；base64 媒体外置为独立文
  件，正文中以 `__ELYSIA_ASSET__` 占位符代替。
- **媒体存储去重（严重磁盘泄漏修复）**：存储布局改为内容寻址扁平目录
  （`usage-assets/<sha256>.<ext>`），新增 `usage_asset_refs` 引用计数
  表；相同内容跨请求只存一份，删除记录仅在该文件零引用后执行；启动时
  一次性幂等迁移旧的按请求分目录布局（LIKE 预过滤 + 分批插入，规避单
  连接池死锁）；孤儿清扫改为引用表驱动（24h 宽限期，兼容遗留目录）。
- **超限清理自适应批量（v2）**：批量在 1..1000 全区间滑动，删除目标统
  一对准迟滞带边缘（101%），按实测均值逐批修正；轻微超限不再触发固定
  大批量删除，清理频率受节流控制。
- **保留清理可靠性**：手动清理互斥自锁修复（acquireRunSlot 拆分）、
  beyond-count 分批有界删除、rollup 审计只按少计方向重建、重置快速失
  败、VACUUM 限频（6h 成功冷却）。
- **流式链路修复**：SSE 行拆分器按 LastIndex 重建（修复 CRLF 行重复拼
  接导致的 JSON 损坏）、流事件环形缓冲尾部保留与 recordUsage 时物化、
  OpenAI/Anthropic 流式工具参数增量 delta 重构（修复参数重复）、流捕
  获单一来源化。
- **并发与配置安全**：config Reload 全程持锁修复并发保存丢失更新（附
  收敛测试）、运行时配置两阶段「校验-应用」、host/port 与
  modelCatalog 变更正确持久化、主密钥丢失后可恢复。
- **健康检查**：探测间隔与开关热重载、共享 transport（关停时释放空闲
  连接）、失败键过期清理、禁用模型不再探测、零间隔不再重试。
- **资源与输入加固**：AssetChip blob URL 释放、密钥轮询游标、数字输入
  框空值保持、表单重置与管理端校验。
- **媒体预览与 WebUI**：缩略图 + 灯箱大图 + 下载按钮；Dialog 层级
  （z-[74]/[75]）修复被详情 Sheet 遮挡；模块级 LRU blob 缓存（128MB /
  96 条，in-flight 去重、驱逐时 revoke）；陈旧 URL 竞态与灯箱越界守
  卫；重试徽章基线对齐。
- **重构（不改行为）**：prepareRelayPlan / relayFailer 统一中转入口
  前奏、自定义协议 normal handler 合并、LogDetailSheet 子树抽取、共
  享 UI 原语、死代码清扫、魔法值常量化与嵌套收敛。
- **Docker 支持**（PR #13 / #14，@LingLambda）：多架构 Dockerfile
  （linux/amd64 + linux/arm64）、Docker Hub 自动发布 workflow（需在
  仓库 secrets 配置 `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN` 后生效）、
  docker README、双语 README 入口与项目状态徽章。
- **面板修复与增强**（PR #16 / #23，@vioaki）：macOS App WKWebView
  内日志导出修复、小屏按钮与分段标签布局、禁用组成员保留但选择器隐
  藏、modelId 移至 query 参数（支持含斜杠的模型 id）、缓存命中率展示
  （列表 + 详情）、统计仅按成功调用计 token 与时延、模型日分布上限提
  升与扩展调色板、顶部条带跟随调用量、图表 tooltip 裁剪与缓存徽章基
  线修复。
- **发布物**：Windows exe / Linux 二进制 / macOS DMG + SHA256SUMS 照旧
  由 CI 产出；Docker 镜像随 tag 发布（`v1.3.0` 与 `latest`）。

## v1.2.0 - 2026-08-31

这是 v1.1.5 以来最大的一次更新，主题是「看得更清楚、跑得更快、更稳」。

### 全新的用量分析

- 总览页新增「实时脉搏」：每分钟请求量、平均与最慢响应时间，一张图看清
  网关当下的状态；还有「模型调用日分布」，最近 7/30 天每个模型每天被调了
  多少次一目了然。
- 统计与调用日志页支持按「模型源」筛选，不同源里的同名模型也能准确区分。

### 数据再多也不卡

- 过去把时间窗切到「30 天 / 全部时间」要等几十秒，现在几乎秒开——新版会
  提前把汇总数据算好。
- **升级后第一次启动会自动升级数据库**：历史数据多的话需要几分钟，期间可
  能暂时没有新日志，属正常现象、不是卡死；完成后以后的启动都会很快。

### 不同 AI 接口之间的翻译更可靠

- 思考过程、引用来源、工具调用等内容在 OpenAI / Claude / Gemini 三种协议
  之间来回转换时不再丢失或变形；流式回复的结束方式更规范，用量统计的口径
  更准。

### 修了一批稳定性问题

- 健康检查误报导致的「好模型被误禁」、Claude 源探测路径错误、重复记录导
  致统计翻倍、高并发下的写入竞态等。

### 界面更细致

- 页面按需加载，登录后首屏明显变快；模型源健康列表放不下时才滚动；热门
  模型支持 24 小时 / 7 天 / 30 天 / 全部快速切换。
- 大屏幕上的字号与间距做了收敛调优：只在很宽的屏幕上轻微放大，整体观感与
  旧版接近。

老数据全部自动迁移，无需任何手工操作。完整技术变更见仓库
[CHANGELOG.md](https://github.com/PinkElysiaDev/Elysia-Api/blob/deploy/CHANGELOG.md)。

<!-- release-details -->

自 v1.1.5 以来合入 deploy 的完整技术变更：

- **用量分析全量重建**（PR #12）：usage 记录持久化模型源 ID（`source_id`
  列 + 增量迁移），模型源筛选按源精确匹配，同名模型跨源不再串数据；
  总览页新增实时用量脉搏（RPM / 平均时延 / P95 / 瞬时吞吐）与模型日调用
  堆叠图；新增 `GET /api/admin/usage/pulse`、`/usage/by-model-daily`、
  `/usage/seq` 接口；路由懒加载 + chunk 拆分优化首屏。
- **用量写入生命周期加固**：reset 与异步落库的 generation 竞态、writer
  关停/入队并发、优雅关停幂等（usage writer 全链路上锁 + 防死锁测试）；
  同 request_id 重复落库不再使 rollup 双计数；坏时间戳（`started_ms<=0`）
  行不再落成 1970 日桶；rollup 回填随 Store 关闭可取消。
- **一次性迁移可见性与版本门控**：大库上覆盖索引构建与标签改写两步一次
  性迁移现在会打印进度/耗时日志（升级后首启不再「看起来卡住」）；标签迁移
  挂 `schema_migrations` 版本标记，已迁移的库每次启动零成本跳过全表扫描。
- **健康检查路径统一**：探测端点按 `NormalizeAPIFormat` 归一化构造，
  claude 源探测从 `/messages` 修正为 `/v1/messages`（与 ClaudeAdapter
  一致），Responses 平台支持原生 `/responses` 探测。
- **协议转换强化（六批次）**：推理链闭环（maheshvara-reasoning-v2 私信
  封）、严格流式终态语义、三协议 citations 与 grounding 保真、往返保真、
  用量计量保真、遗留 function calling 与 `system_fingerprint`。
- **全项目 review 修复（四批次）**：稳定性与安全（2×P1）、协议正确性
  （3×P1）、rollup 卫生与重试取消、路由元数据与可访问性。
- **大库用量窗口性能优化（两阶段）**：切窗查询索引友好化 + 小时级
  rollup 预聚合，全部时间窗口毫秒级返回；修复筛选命中单列索引退化陷阱；
  筛选工具栏无卡片重设计，空交集显式空结果。
- **WebUI 卡片化重设计（PR #10）**：总览与用量页重建，Gemini 风格设计
  语言与十项 UX 修复；模型源健康按实测溢出滚动、热门模型时间窗快捷切换。
- **异步后台模型刷新任务**与 usage 聚合覆盖索引。
- **代码质量 pass（六批次）**：死代码清理（约 -2300 行）、兜底审计、重复
  helper 提取、`migrate()` 拆分、命名与魔法值常量化、遗留 usage 面板下线。
- **核心转换协议命名统一**：Canonical → Maheshvara（大自在天），数据库
  历史标签自动迁移。
- **排版收敛**：大屏流式字号改为 1536px 起步、18px 封顶（原 1280px 起步、
  20px 封顶），侧栏/表头/品牌/抽屉标签的过松字距收紧，KPI 大数字上限调低；
  图表刻度字重阶梯相应简化。
- **发布说明机制改造**：散落的 `RELEASE_NOTES_v*.md` 合并为本 Changelog，
  Release 正文改由本文件提取。

## v1.1.5 - 2026-08-23

`v1.1.5` 是一次正确性与稳定性集中修复版本，同时是**首个提供 macOS App
（DMG）发布物的版本**：macOS 发布物由通用二进制 `elysia-api-macos.dmg`
取代此前的两个 darwin 裸二进制，Release 资产自此固定为 Windows exe /
Linux / macOS DMG 三件套（附 SHA256SUMS）。其余修复覆盖跨协议工具调用、
健康检查误禁、用量统计口径、数据写入安全与 WebUI 体验。

### macOS 发布物改为原生 App（DMG）

- 新增 `ElysiaApi.app` 原生壳应用：菜单栏常驻状态、面板自动登录、关窗
  后台运行、应用内一键自更新（下载 DMG 并校验 sha256 后原子替换）；
- 数据（config / SQLite / master-key / 日志）存放于
  `~/Library/Application Support/ElysiaApi/`，更新不影响数据；
- 本地 `npm run build` 仍与主机无关地交叉编译全部四个平台二进制；DMG
  只能在 macOS 上组装，由 CI 在发布时产出。

### 跨协议工具调用链路修复

- OpenAI `role:"tool"` 消息内容正确包装为 ToolOutput：转 Claude/Gemini
  时能生成 `tool_result`/`functionResponse`，不再因缺少工具结果被上游
  400；
- Claude → OpenAI 方向 tool 消息紧跟 assistant 的 tool_calls、先于补充
  文本，符合 OpenAI 消息顺序硬性要求；
- Gemini `functionResponse` 的调用 ID 自动回填为同名 `functionCall` 的
  实际 ID（name 关联 → id 关结对齐）；
- thinking 块修复为始终位于 assistant 消息首位（Anthropic 要求），
  `[thinking, text]` 往返后顺序不乱；
- `max_tokens` 显式小值原样透传，不再被强制抬高到 65536。

### 健康检查与模型列表防误伤

- Gemini 原生上游首次支持正确探测（
  `/v1beta/models/{model}:generateContent` + `x-goog-api-key`），不再必
  404/401 被误禁；
- 每次探测独立限时，单个慢上游不再耗尽整轮预算连带误禁其他模型；
- 404/405 视为"端点不支持探测"而非上游故障；
- 模型源刷新遇到异常响应（解析出 0 个模型）时保留现有列表，不再清空
  该源。

### 用量统计准确性与存储迁移

- usage_records 新增整型毫秒列 `started_ms`（含索引），修复 RFC3339
  字符串字典序在整秒边界漏记录、同秒排序错乱的问题；旧库启动时自动
  迁移并分批回填（幂等、可断点续传）；
- 请求 ID 追加随机后缀，修复 Windows 时钟粒度下并发请求 ID 相同互相
  覆盖；
- `allTimeSummary` 真正查询全量；时间窗口按本地时钟对齐；全部候选失败
  时补记 usage；
- usage 持久化压缩重写改为内存拼接 + 原子替换，中途崩溃不再丢全部
  历史。

### 可靠性与运行时配置

- 自定义协议非流式请求支持故障转移：连接失败 / 5xx / 429 时自动切换
  下一候选模型，而不是直接报错给客户端；
- 客户端断开连接后上游调用随 context 取消中止，不再空耗带宽与上游
  配额；
- `httpTimeout` 修改即时生效无需重启；"需要重启"状态真实上报
  （host/port/数据库路径变更后置位）；
- 拒绝设置空面板访问令牌，避免把管理面板锁死；
- config.json 改为原子写入 + 全程写锁，进程崩溃不留半截文件，并发保存
  不互相覆盖。

### 协议兼容细节

- 终止原因在 OpenAI / Claude / Gemini 三协议间完整枚举归一化，未知值
  不再原样透传导致严格 SDK 解析失败；
- 音频输入转为裸 base64 + `mp3`/`wav` 短格式（剥离 data: URI、归一化
  MIME）；
- SSE 流中未知字段行按规范忽略，不再破坏 JSON 解析或中止整条流；
- Anthropic 缓存写入 token 明细不再与总数双重计入；
- 自定义协议 `omitIfEmpty` 路径删除真正移除数组元素，不再留下 `null`
  空洞。

### WebUI 体验

- 修复暗色模式首帧闪白（主题脚本前置注入）；
- 用量页时间窗口每分钟自动推进，页面常开也能看到新记录；
- 记录删除/重置后分页自动收敛，不再停留在超界空页；
- 静态资源缓存策略优化（哈希资源 immutable、index.html no-cache），
  升级后不再白屏；
- 面板访问令牌留空提交 = 不修改；开发代理增加 `/debug`。

### 验证

- 后端 `go test ./...`（relay / server / storage / config）全部通过，
  含新增全项目 bug 审查回归测试（批次 A，A1–A9）与自定义协议故障转移
  端到端测试；
- 本地交叉编译四平台二进制（windows-amd64 / linux-amd64 /
  darwin-amd64 / darwin-arm64）通过；
- DMG 由 CI（macos-latest + Xcode 工具链）组装并随本 Release 发布。

## v1.1.4 - 2026-08-18

`v1.1.4` 修复 Anthropic Messages 转到 OpenAI Chat Completions 时
`tool_choice` 形态不合法的问题。Claude Code 默认发送的 `{"type":"auto"}`
不再原样进入 Chat 上游。

### 修复 Anthropic → Chat 的 `tool_choice` 转换

- Anthropic 对象形态现在映射为 Chat Completions 合法值：
  - `{"type":"auto"}` → `"auto"`
  - `{"type":"any"}` → `"required"`
  - `{"type":"none"}` → `"none"`
  - `{"type":"tool","name":"X"}` → `{"type":"function","function":{"name":"X"}}`
- Responses 扁平的 `{"type":"function","name":"X"}` 也会补成 Chat 的
  `function` 嵌套对象；
- 不再把 `{"type":"auto"}` 原样发给 Chat 上游，消除
  `Expected field function in tool_choice`。

### 对齐并行工具调用开关

- Claude `disable_parallel_tool_use` 写入 `parallel_tool_calls`，再由
  Chat 请求发出；
- Chat → Claude 时，若 `parallel_tool_calls=false`，会在 Claude
  `tool_choice` 对象上补 `disable_parallel_tool_use: true`。

### 修复 Chat → Claude 时 `tool_choice` 被原始值覆盖

- 去掉 Claude 写出路径里用原始 `req.ToolChoice` 覆盖转换结果的逻辑；
- Chat 的 `"auto"` / `{"type":"function",...}` 现在稳定渲染为 Claude 的
  `{"type":"auto"}` / `{"type":"tool","name":...}`。

### 验证

- 后端：`go test ./relay` 通过；
- 新增 `tool_choice` 表驱动测试与 Claude ↔ Chat 往返回归。

## v1.1.3 - 2026-08-13

`v1.1.3` 是修复版本，聚焦三类稳定性缺陷：模型组删除导致的后台整页
空白、工具调用 `id` 缺失导致的上游拒绝、以及 Responses 同协议透传丢失
`reasoning_text`。

### 修复删除被使用的模型组导致管理面板整页空白

- 零可见模型的模型组现在返回 `"models":[]` 而非 `null`，消除前端
  `group.models.slice` 崩溃的根源；
- `DeleteGroup` 改为事务化操作，级联移除所有 API Key `allowedGroups`
  中的组名引用，同时完整保留 usage 历史；
- 删除后同步清理该组的限流、轮询游标与粘滞路由运行时状态，避免残留
  内存；
- WebUI 增加数据归一化与根部 ErrorBoundary，异常时展示可重试界面而非
  白屏。

### 修复工具调用 id 缺失导致的 `messages[N]: missing field id`

- Claude、OpenAI Chat、Responses 输入中缺失的工具调用 `id` 会生成确定性
  的 `call_<消息序号>_<调用序号>`；
- assistant 的 `tool_calls[].id` 与后续 `role:"tool"` 的 `tool_call_id`
  保持一致；
- OpenAI 直通请求仅在确实缺少 id 时做最小修补，其余字节原样透传；流式
  渲染同步兜底。

### 修复 Responses 同协议透传丢失 `reasoning_text`

- Responses→Responses 与 OpenAI 系同协议流改为原始 SSE 逐行转发，不再
  经 Maheshvara 重渲染；
- `response.reasoning_text.*` 等 provider 私有事件完整到达下游，多轮
  思考模式续传不再报 `reasoning_text must be passed back`；
- usage 统计、首字节耗时与错误处理逻辑保持不变。

### 验证

- 后端：`go test ./...` 与 `go vet ./...` 全部通过；
- 前端：TypeScript 编译与 Vite 构建通过；
- 新增存储、协议转换、流式透传回归测试覆盖上述场景。

## v1.1.2 - 2026-08-13

`v1.1.2` 是一个修复与优化版本，聚焦于四类同协议透传的稳定化、工具调用
格式转换修复、模型管理一致性，以及发布产物体积回归。

### 四种协议同协议透传稳定化

- 将 Chat Completions、Responses、Claude Messages、Gemini
  GenerateContent 四类协议的同源透传改为**无条件默认启用**（不再依赖
  `relay.passthrough` 开关，该字段标记为弃用）；
- 彻底修复 codex 多轮 Responses 请求中 `reasoning_text` 等富字段被有损
  重建导致上游报错（`reasoning_text ... must be passed back`）的隐患。

### Anthropic → Chat 工具调用格式转换修复

- 修复 Claude `tool_result` 块被错误渲染为 `user` 文本消息的问题；
- 现在 `tool_result` 会正确转换为 OpenAI `role:"tool"` 消息并携带匹配的
  `tool_call_id`，消除上游 `insufficient tool messages` 报错。

### 模型源手动模型同步

- 手动添加的模型改为保存时**同步写入**模型缓存，消除前端 revalidate
  竞态导致的「手动模型不更新模型组」问题。

### 模型源删除/停用后的模型组清理

- 删除模型源时事务内同步清理 `model_group_models` 引用；
- 模型组列表查询按源 `enabled` 状态过滤，停用/删除源后不再残留旧模型
  选中。

### 发布产物体积回归

- 交叉编译脚本补回 `-ldflags "-s -w"`，剥离符号表与 DWARF 调试信息；
- 四平台二进制由约 22MB 回落至约 16MB。

### 独立交叉编译产物

本次发布的独立运行包包含静态 WebUI 嵌入，无需任何外部 Node.js / 前端
环境依赖：

| 平台架构 | 可执行文件名 |
| :--- | :--- |
| **Windows AMD64** | `dist/standalone/elysia-api-windows-amd64.exe` |
| **Linux AMD64** | `dist/standalone/elysia-api-linux-amd64` |
| **macOS Intel (AMD64)** | `dist/standalone/elysia-api-darwin-amd64` |
| **macOS Apple Silicon (ARM64)** | `dist/standalone/elysia-api-darwin-arm64` |

## v1.1.1 - 2026-08-09

`v1.1.1` 是一个重要修复与功能增强版本，主要聚焦于 **WebUI 页面交互与
缓存性能修复**，以及 **同类 API 协议默认透传模式 (Passthrough Mode)**
的功能升级。

### WebUI 交互体验与性能优化

- **Token 累计分布图 Hover 精准触发**：将 `UsageStatsPage` 中甜甜圈
  图表（输入 Token 拆分为缓存命中/未命中环）的 hover 触发区域限制在
  圆环真实半径范围内；避免鼠标仅划过图表卡片空白边缘或图例时产生误
  触发，交互体验更加自然流畅。
- **Usage 统计与日志页面秒开 (0ms 延迟)**：引入 `normalizedNow` 工具
  函数将查询终止时间戳向下舍入至 **1 分钟粒度**，解决毫秒级时间戳导致
  SWR Cache Key 频繁变动而反复触发骨架屏 (Skeleton) 的问题；开启
  `keepPreviousData: true` 及 5 秒请求去重，实现多页面间来回切换时上一
  次统计数据的**瞬间呈现与无感后台静默更新**。
- **模型源停用后的路由隔离与配置拦截**：修复停用某个模型源后，模型组
  编辑弹窗 (`group-form.tsx`) 依然可以选中该源下模型的 bug；后端路由
  缓存 (`route_cache.go`) 与数据库模型列表 (`queries.go`) 增加了对模型
  源 `enabled` 状态的同步校验，确保已停用源的模型无法在运行时被请求
  路由匹配或调用。

### 同类 API 默认启用透传模式

- **转发引擎透传策略升级**：将 `config.go` 中
  `RelayConfig.Passthrough` 的默认值由 `false` 修改为 `true`；当下游
  客户端请求协议与上游目标模型源协议同属一类（如 OpenAI Chat ->
  OpenAI, Claude Messages -> Anthropic, Responses -> OpenAI Responses）
  时，代理层默认自动采用 **原生 Payload 透传模式**；在跳过冗余格式
  转换开销的同时，完整保真客户端发送的第三方扩展字段与高级特性（如
  `cache_control`、`thinking` 等）。

### 独立交叉编译产物

本次发布的独立运行包包含静态 WebUI 嵌入，无需任何外部 Node.js / 前端
环境依赖：

| 平台架构 | 可执行文件名 |
| :--- | :--- |
| **Windows AMD64** | `dist/standalone/elysia-api-windows-amd64.exe` |
| **Linux AMD64** | `dist/standalone/elysia-api-linux-amd64` |
| **macOS Intel (AMD64)** | `dist/standalone/elysia-api-darwin-amd64` |
| **macOS Apple Silicon (ARM64)** | `dist/standalone/elysia-api-darwin-arm64` |
