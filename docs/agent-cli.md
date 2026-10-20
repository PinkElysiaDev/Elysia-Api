# elysia CLI 参考

[文档首页](README.md) · **简体中文** · [English](agent-cli.en.md)

本页由工具定义、命令表和运行时帮助生成。`elysia` 是 `elysia_cli` 的命令语法，不是独立终端程序。下文 `text` 块中的尖括号和省略号表示待替换内容；不是可直接粘贴到 shell 的脚本。

内置助手还提供 `ask_user` 和 `update_plan`，职责及权限见[工具目录](agent-tools-catalog.md)。REST/A2A 遵循会话审批；MCP 持 `agent` 作用域 Key 直接执行，不进入该审批链。MCP 每次调用无状态，草稿和测试目标须在同一次 `command` 批处理中复用，见[远程接入](remote-agent-api.md)。

帮助原文保持运行时内容。阅读时注意：`key create` 的名称提示仍写有“创建后不可改”，但 `key update --new-name` 支持改名；“加密存储”以主密钥成功加载为前提；调用日志正文取决于[捕获策略](deployment.md#请求日志)，默认不保存。`--thinking` 的旧帮助档位也不等同于当前模型能力值 `both` / `non-thinking-only` / `thinking-only`；它与 Agent 的 `thinkingEffort` 是不同字段。协议完整字段以[定义参考](protocol-definition-reference.md)为准。

## 语法与命令框架迁移

使用 mvdan.cc/sh/v3 v3.14.1（POSIX 语法）和 Cobra v1.10.2 / pflag v1.0.10。解析器产出 AST，受限适配层调用原有网关 handler，不启动系统 shell、子进程，也不读取环境变量或执行文件通配。

支持字面参数、单/双引号拼接、空引号、反斜杠转义与续行、注释、分号、换行、&& 和原有内存 grep/head 过滤器。单词内的花括号、通配符及 ~ 保持字面值。含 $、反引号或 shell 运算符的 JSON、密钥等字面值请使用单引号。

### 兼容性变化

- POSIX 词法替代自定义扫描器：未引用的反斜杠转义后一字符，词首 # 开始注释。末尾 && 或 |、连续分号、未闭合引号均为语法错误。语法错误及禁止的顶层执行修饰符使整个脚本在执行前失败；其他不支持的语句或无效命令在对应语句处失败，保留 && / 分号的批处理语义。
- 不支持变量、算术、命令及进程替换、赋值、重定向（含 here-document）、后台执行、||、否定、子 shell 和控制结构；作为参数的同类文本须引用，解析器不会求值。
- 命令查找、未知命令/flag 错误及帮助排版采用 Cobra/pflag，不再限制命令路径深度。组级 help 列出子命令，命令级 help 展示 flag、示例、位置参数与完整语义；支持 group member 等嵌套组。
- 支持命令后 --help / -h。帮助不调用业务 handler，也不触发写审批；--help=false 仍执行并按业务规则审批。位置参数以 - 开头时使用 -- 结束 flag 解析；按照 pflag 规则，必需的 flag 值本身也可从 - 开始。
- 保留参数缺失与显式空值的区别、裸布尔 flag、大小写不敏感的 true/false、1/0、yes/no、可重复逗号列表及标量后值覆盖。显式布尔值使用 =；列表不采用 CSV 引号规则。
- CLI 回显、进度和 Agent 事件共用基于 AST 单词范围的密钥脱敏，覆盖转义空格及拼接引号；展示语法无效或含展开的脚本时使用诊断占位符。

业务响应结构、输出预算、取消、handler 超时、&& 短路及 Agent/MCP 权限策略保持原有语义。本次迁移涉及的应用层 JSON 调用使用 Sonic。

## 调用契约

执行 Elysia API 网关运维命令。每条命令须以 elysia 开头；首次使用先查 elysia help，具体用法按需查 elysia help <组> [命令]。

```json
{"command":"elysia source ls"}
```

## 总览

```text
elysia —— 网关运维 CLI（全部操作经 elysia_cli 工具执行）

分级帮助：elysia help <组> / elysia help <组> <命令>；命令后也可使用 --help 或 -h。
语法由 mvdan/sh 解析：单引号、双引号、反斜杠转义、注释；空引号表示空值。
参数由 Cobra/pflag 解析：--flag value / --flag=value；布尔值可省略；列表支持重复与逗号分隔。
批处理：&& 失败跳过所在链；分号或换行继续。尾管道：| grep <子串>（大小写不敏感），
| head <n> / -n N / -N（文本行数，不是记录数）。不执行系统 shell、变量展开、命令替换、重定向或控制结构。
含 $、反引号或 shell 运算符的字面值请用单引号；通配符和 ~ 保持字面值。
批处理无整体事务或自动回滚；需要根据中间结果决定后续参数时应分次调用。
查询优先使用命令过滤参数和 --limit；输出可能截断。修改、真实出站和删除受服务端权限与业务策略控制。

常用组合示例：
  elysia source ls && elysia model ls --source 主源 --limit 20
  elysia usage logs --days 1 --status failed --limit 10
  elysia group create --name 主力 --models s1:gpt-4o

Usage:
  elysia [command]

Available Commands:
  code        code
  group       模型组管理与成员维护
  help        Help about any command
  key         API Key（推理访问令牌）管理
  model       单模型管理
  outbound    出站禁止 IP 段（SSRF 防护）
  protocol    协议设计与运维（schema/草稿/验证/预览/测试/修订/启用/回滚）
  source      模型源管理
  syslog      查询系统日志
  usage       用量统计与调用日志

Flags:
  -h, --help   help for elysia

Use "elysia [command] --help" for more information about a command.
```

## code

````text
code

Usage:
  elysia code [command]

Available Commands:
  ls          列出源码快照文件(引擎与预置协议的参考实现)
  read        读取源码快照中的一个文件

Flags:
  -h, --help   help for code

Use "elysia code [command] --help" for more information about a command.
````

### code ls

````text
列出源码快照文件(引擎与预置协议的参考实现)

elysia code ls [前缀]
<prefix>: 路径前缀过滤(可选,如 backend/relay/ 或 packages/webui/src/lib/)

详细说明：列出随二进制打包的源码快照文件(仓库相对路径,可加路径前缀过滤;配合 grep/head 管道使用)。

Usage:
  elysia code ls [prefix] [flags]

Examples:
  示例：elysia code ls backend/protocol/builtin/definitions

Flags:
  -h, --help   help for ls
````

### code read

````text
读取源码快照中的一个文件

elysia code read <路径>
<path>: 仓库相对路径(先 code ls 浏览)

详细说明：读取源码快照中的一个文件(仓库相对路径,如 backend/protocol/compiler.go 或 packages/webui/src/lib/protocol-v2.ts);输出较长时配合 grep/head 管道截取。预置协议原文在 backend/protocol/builtin/definitions/ 下；协议指南在 docs/ 下。

Usage:
  elysia code read [path] [flags]

Examples:
  示例：elysia code read backend/protocol/builtin/definitions/anthropic-messages.json

Flags:
  -h, --help   help for read
````

## source

````text
模型源管理

Usage:
  elysia source [command]

Available Commands:
  create      创建模型源（受权限策略控制）
  delete      删除模型源（不可逆，受权限策略控制；级联删模型与组引用）
  ls          列出全部模型源（密钥脱敏）
  refresh     从上游拉取模型列表（真实出站，受权限策略控制）
  update      修改模型源（受权限策略控制；api-key 留空=保留）

Flags:
  -h, --help   help for source

Use "elysia source [command] --help" for more information about a command.
````

### source create

````text
创建模型源（受权限策略控制）

elysia source create --name <名> --base-url <URL> [--platform custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]

详细说明：创建模型源（受服务端权限与业务策略控制）。platform 使用 custom:<协议ID>，默认 custom:openai-chat-completions；autoFetchModels=true 标记为自动源（模型列表仍需创建后运行 elysia source refresh 拉取，或用户在页面手动拉取）；手动模型用手动列表或 manualModels。创建前先向用户确认 baseUrl、平台与密钥来源。

Usage:
  elysia source create [flags]

Examples:
  示例：elysia source create --name 主源 --base-url https://api.example.com --api-key sk-xxx

Flags:
      --api-key string          API key（加密存储）
      --auto-fetch              标记为自动源（创建后如需上游实时模型列表，执行 elysia source refresh）
      --base-url string         上游 baseUrl（http/https）
      --fetch-base-url string   模型列表拉取地址（缺省同 base-url）
  -h, --help                    help for create
      --manual-models list      手动模型名列表（逗号分隔）
      --name string             源名称（显示用）
      --platform string         custom:<协议ID>（默认 custom:openai-chat-completions）
````

### source delete

````text
删除模型源（不可逆，受权限策略控制；级联删模型与组引用）

elysia source delete --source <id|名>

详细说明：删除模型源（受服务端权限与业务策略控制，不可逆）：源下全部模型与组内成员引用一并级联删除。source 用源 id 或名称指定。删除前先向用户核对对象与影响面（模型数、受影响的组）。

Usage:
  elysia source delete [flags]

Examples:
  示例：elysia source delete --source 主源

Flags:
  -h, --help            help for delete
      --source string   源 id 或名称
````

### source ls

````text
列出全部模型源（密钥脱敏）

elysia source ls

详细说明：列出全部模型源：平台、baseUrl、启停、自动拉取、密钥策略（脱敏）、模型数与最近拉取状态。不显示任何密钥明文。

Usage:
  elysia source ls [flags]

Examples:
  示例：elysia source ls

Flags:
  -h, --help   help for ls
````

### source refresh

````text
从上游拉取模型列表（真实出站，受权限策略控制）

elysia source refresh --source <id|名>

详细说明：从上游拉取某模型源的模型列表（受服务端权限与业务策略控制，真实出站请求）。手动源则会同步手动模型列表。新建自动拉取源后用它（elysia source refresh）取回模型。上游返回空列表时刷新会报错并保留现有缓存；这不证明从未刷新，也不能单独确定上游为空的原因。

Usage:
  elysia source refresh [flags]

Examples:
  示例：elysia source refresh --source 主源

Flags:
  -h, --help            help for refresh
      --source string   源 id 或名称
````

### source update

````text
修改模型源（受权限策略控制；api-key 留空=保留）

elysia source update --source <id|名> [--enabled] [--name <名>] [--base-url <URL>] [--platform <平台>] [--api-key <新key>] [--auto-fetch[=false]] [--manual-models a,b]

详细说明：修改已有模型源（受服务端权限与业务策略控制）：启停、改名、换 baseUrl/平台、更换 API key（留空=保留原 key）、调整自动拉取或手动模型列表。source 用源 id 或名称指定。

Usage:
  elysia source update [flags]

Examples:
  示例：elysia source update --source 主源 --enabled=false

Flags:
      --api-key string       新 API key（留空=保留原值）
      --auto-fetch           自动源标记开关
      --base-url string      换 baseUrl
      --enabled              启停
  -h, --help                 help for update
      --manual-models list   整体替换手动模型列表
      --name string          改名
      --platform string      换平台
      --source string        源 id 或名称
````

## model

````text
单模型管理

Usage:
  elysia model [command]

Available Commands:
  ls          查询本地缓存的模型清单（可按源过滤；上游实时列表走 source refresh）
  rm          删除单个模型（不可逆，受权限策略控制）
  set         修改单个模型（受权限策略控制）

Flags:
  -h, --help   help for model

Use "elysia model [command] --help" for more information about a command.
````

### model ls

````text
查询本地缓存的模型清单（可按源过滤；上游实时列表走 source refresh）

elysia model ls [--source <id|名>] [--search <子串>] [--limit <n>]

详细说明：查询网关本地缓存的模型清单，不代表上游实时状态（默认全部源）。source 传源 id 或名称可按源过滤；search 按名称模糊匹配。建模型组前用它确认可用的模型 id。只给前 limit 条（默认 50、上限 200），总量在 summary 里。空列表不能单独证明未刷新：先检查 source/search 查询条件与 elysia source ls 返回的源状态；需要实时信息时再 elysia source refresh --source <源>，随后重新查询。

Usage:
  elysia model ls [flags]

Examples:
  示例：elysia model ls --source 主源 --search gpt --limit 20

Flags:
  -h, --help            help for ls
      --limit string    返回条数（默认 50，上限 200）
      --search string   名称模糊匹配
      --source string   源 id 或名称
````

### model rm

````text
删除单个模型（不可逆，受权限策略控制）

elysia model rm --source <id|名> --model <模型id>

详细说明：删除单个模型（受服务端权限与业务策略控制，不可逆）：组内引用一并清理。自动拉取的模型下次刷新可能重新出现；想临时下线优先用 elysia model set --source <源> --model <模型id> --enabled=false。source 用源 id 或名称，model 是模型 id。

Usage:
  elysia model rm [flags]

Examples:
  示例：elysia model rm --source 主源 --model gpt-4o

Flags:
  -h, --help            help for rm
      --model string    模型 id
      --source string   源 id 或名称
````

### model set

````text
修改单个模型（受权限策略控制）

elysia model set --source <id|名> --model <模型id> [--name <名>] [--type <类型>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <模式>] [--enabled[=false]]

详细说明：修改单个模型（受服务端权限与业务策略控制）：启停、改名、类型、maxTokens、能力标记（视觉/工具/结构化）、思考模式。能力字段被修改后刷新不再覆盖（capability_source=manual）。source 用源 id 或名称，model 是模型 id（先 elysia model ls 查）。

Usage:
  elysia model set [flags]

Examples:
  示例：elysia model set --source 主源 --model gpt-4o --vision --enabled=false

Flags:
      --enabled             启停
  -h, --help                help for set
      --max-tokens string   maxTokens
      --model string        模型 id
      --name string         改名
      --source string       源 id 或名称
      --structured          结构化输出标记
      --thinking string     思考模式（disabled 默认 / enabled / adaptive）
      --tools               工具能力标记
      --type string         类型（llm 默认 / reranker / embedding 预留）
      --vision              视觉能力标记
````

## group

````text
模型组管理与成员维护

Usage:
  elysia group [command]

Available Commands:
  create      创建模型组（受权限策略控制）
  delete      删除模型组（不可逆，受权限策略控制；可能级联禁用 Key）
  ls          列出全部模型组及成员
  member      member
  update      修改模型组（受权限策略控制；改名/成员增删/策略/限额）

Flags:
  -h, --help   help for group

Use "elysia group [command] --help" for more information about a command.
````

### group create

````text
创建模型组（受权限策略控制）

elysia group create --name <组名> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]

详细说明：创建模型组（受服务端权限与业务策略控制）：模型引用列表（sourceId:modelId 或模型名）、调度策略、重试。名称需唯一；创建前先用 elysia model ls 确认成员引用，再用 elysia group ls 核对重名。

Usage:
  elysia group create [flags]

Examples:
  示例：elysia group create --name 主力 --models s1:gpt-4o,s1:gpt-4o-mini

Flags:
      --daily-limit-requests string   每日请求上限（0=不限）
      --daily-limit-tokens string     每日 token 上限（0=不限）
      --enabled                       默认 true
  -h, --help                          help for create
      --max-concurrency string        并发上限（0=不限）
      --max-retries string            失败重试次数（默认 3）
      --models list                   成员模型引用（sourceId:modelId 或模型名，逗号分隔）
      --name string                   组名（客户端调用时用的模型名）
      --strategy string               调度策略：sequential=失败回退（按序调用，前败后补）/ random=随机起点环绕 / round-robin=游标轮询（默认）
````

### group delete

````text
删除模型组（不可逆，受权限策略控制；可能级联禁用 Key）

elysia group delete --group <组名|id>

详细说明：删除模型组（受服务端权限与业务策略控制，不可逆）：客户端将无法再按该组名调用。若某些 API Key 只授权了这一个组，会随删除级联禁用（名单在结果里返回）。group 用组名或 id 指定。

Usage:
  elysia group delete [flags]

Examples:
  示例：elysia group delete --group 退役组

Flags:
      --group string   组名或 id
  -h, --help           help for delete
````

### group ls

````text
列出全部模型组及成员

elysia group ls

详细说明：列出全部模型组：名称、启停、策略、重试、并发/限额与成员（sourceId:modelId 引用，也可能只显示模型 id）。

Usage:
  elysia group ls [flags]

Examples:
  示例：elysia group ls

Flags:
  -h, --help   help for ls
````

### group member add

````text
向模型组追加成员（受权限策略控制）

elysia group member add --group <组名|id> --models <列表>

详细说明：修改已有模型组（受服务端权限与业务策略控制）：改名、启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。改名注意：组名是客户端调用的模型名，改名后客户端调用名随之变化；引用旧组名的 API Key 授权（allowedGroups）不会自动迁移，改名前先向用户确认影响。

Usage:
  elysia group member add [flags]

Examples:
  示例：elysia group member add --group 主力 --models s1:o1,s1:o2

Flags:
      --group string   组名或 id
  -h, --help           help for add
      --models list    成员引用（sourceId:modelId 或模型名）
````

### group member rm

````text
从模型组移除成员（受权限策略控制）

elysia group member rm --group <组名|id> --models <列表>

详细说明：修改已有模型组（受服务端权限与业务策略控制）：改名、启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。改名注意：组名是客户端调用的模型名，改名后客户端调用名随之变化；引用旧组名的 API Key 授权（allowedGroups）不会自动迁移，改名前先向用户确认影响。

Usage:
  elysia group member rm [flags]

Examples:
  示例：elysia group member rm --group 主力 --models s1:o2

Flags:
      --group string   组名或 id
  -h, --help           help for rm
      --models list    成员引用
````

### group update

````text
修改模型组（受权限策略控制；改名/成员增删/策略/限额）

elysia group update --group <组名|id> [--name <新组名>] [--add-models <列表>] [--remove-models <列表>] [--enabled[=false]] [--strategy <策略>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]

详细说明：修改已有模型组（受服务端权限与业务策略控制）：改名、启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。改名注意：组名是客户端调用的模型名，改名后客户端调用名随之变化；引用旧组名的 API Key 授权（allowedGroups）不会自动迁移，改名前先向用户确认影响。

Usage:
  elysia group update [flags]

Examples:
  示例：elysia group update --group 主力 --add-models s1:o1 --enabled=false

Flags:
      --add-models list               追加成员
      --daily-limit-requests string   每日请求上限
      --daily-limit-tokens string     每日 token 上限
      --enabled                       启停
      --group string                  组名或 id
  -h, --help                          help for update
      --max-concurrency string        并发上限
      --max-retries string            重试次数
      --name string                   改名（组名即客户端调用的模型名；引用旧名的 Key 授权不会自动迁移）
      --remove-models list            移除成员
      --strategy string               调度策略：sequential=失败回退 / random=随机起点环绕 / round-robin=游标轮询（默认）
````

## key

````text
API Key（推理访问令牌）管理

Usage:
  elysia key [command]

Available Commands:
  create      创建推理 API Key（受权限策略控制；secret 留空自动生成，明文仅返回一次）
  delete      删除 API Key（不可逆，受权限策略控制；远程访问 Key 拒绝）
  ls          查询 API Key 列表（脱敏）
  update      修改 API Key（受权限策略控制；改名/启停/授权/换明文；远程访问 Key 拒绝）

Flags:
  -h, --help   help for key

Use "elysia key [command] --help" for more information about a command.
````

### key create

````text
创建推理 API Key（受权限策略控制；secret 留空自动生成，明文仅返回一次）

elysia key create --name <名> [--secret <明文>] [--allowed-groups <组,...>] [--enabled[=false]]

详细说明：创建 API Key，即客户端调用 /v1 接口用的推理访问令牌（受服务端权限与业务策略控制）。用户给定 secret 明文就按给定值原样创建（弱口令可提醒风险，但不代为拒绝）；留空则自动生成随机明文。完整明文只在本次结果里返回一次，请提醒用户立即保存。allowedGroups 为空表示可访问全部模型组（扩权面大，创建前先向用户确认授权范围）。远程访问 Key（驱动 AI 助手的那类）由用户在运行配置页管理，elysia key 命令不能创建。

Usage:
  elysia key create [flags]

Examples:
  示例：elysia key create --name mobile-app --allowed-groups 主力

Flags:
      --allowed-groups list   允许访问的模型组（逗号分隔；空=不限制）
      --enabled               默认 true
  -h, --help                  help for create
      --name string           Key 名称（唯一）
      --secret string         Key 明文——用户给定就用给定值（弱口令可提醒但不拒绝）；留空自动生成随机值
````

### key delete

````text
删除 API Key（不可逆，受权限策略控制；远程访问 Key 拒绝）

elysia key delete --name <名>

详细说明：删除 API Key（受服务端权限与业务策略控制，不可逆）：使用该 Key 的客户端将立即无法调用。远程访问 Key（agent 作用域）请在运行配置页删除。删除前先向用户核对名称。

Usage:
  elysia key delete [flags]

Examples:
  示例：elysia key delete --name mobile-app

Flags:
  -h, --help          help for delete
      --name string   Key 名称
````

### key ls

````text
查询 API Key 列表（脱敏）

elysia key ls

详细说明：查询 API Key（访问令牌）列表。token 脱敏显示；allowedGroups 为空表示可访问全部模型组。带 agent 作用域的是远程访问 Key（驱动 AI 助手专用，不参与推理），由用户在运行配置页管理——不可对其做写操作。

Usage:
  elysia key ls [flags]

Examples:
  示例：elysia key ls

Flags:
  -h, --help   help for ls
````

### key update

````text
修改 API Key（受权限策略控制；改名/启停/授权/换明文；远程访问 Key 拒绝）

elysia key update --name <名> [--new-name <新名>] [--enabled[=false]] [--allowed-groups <组,...>] [--new-secret <新明文>]

详细说明：修改已有 API Key（受服务端权限与业务策略控制）：改名（newName，目标名被占用会报错）、启停、调整可访问的模型组、更换明文（newSecret 留空=保留原值）。远程访问 Key（agent 作用域）由用户在运行配置页管理，elysia key 命令不可修改。allowedGroups 为空表示不限制（可访问全部模型组），调整前先向用户确认。

Usage:
  elysia key update [flags]

Examples:
  示例：elysia key update --name mobile-app --allowed-groups 主力,备用

Flags:
      --allowed-groups list   整体替换允许访问的模型组
      --enabled               启停
  -h, --help                  help for update
      --name string           Key 名称
      --new-name string       改名（目标名被占用会报错）
      --new-secret string     新明文；留空保留原值
````

## protocol

````text
协议设计与运维（schema/草稿/验证/预览/测试/修订/启用/回滚）

Usage:
  elysia protocol [command]

Available Commands:
  activate    Protocol v2 activate
  diagnose    Protocol v2 diagnose
  diff        Protocol v2 diff
  draft       写入协议草稿并校验定义
  models      按声明操作验证真实上游模型目录（受权限策略控制）
  preview     预览请求、响应、事件或工作流
  read        读取协议草稿或不可变修订
  rollback    Protocol v2 rollback
  save        保存草稿与离线证据，不启用
  schema      Protocol v2 schema
  test        验证真实上游契约（受权限策略控制）
  validate    Protocol v2 validate
  verify      Protocol v2 verify

Flags:
  -h, --help   help for protocol

Use "elysia protocol [command] --help" for more information about a command.
````

### protocol activate

````text
Protocol v2 activate

elysia protocol activate [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol activate [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for activate
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

### protocol diagnose

````text
Protocol v2 diagnose

elysia protocol diagnose [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol diagnose [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for diagnose
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

### protocol diff

````text
Protocol v2 diff

elysia protocol diff [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol diff [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for diff
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

### protocol draft

````text
写入协议草稿并校验定义

elysia protocol draft '<schemaVersion=2 JSON>'
<config>: 完整 v2 定义 JSON

详细说明：接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。
编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。
模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。
真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。
运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。
save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。
read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。
离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。

Usage:
  elysia protocol draft [config] [flags]

Flags:
  -h, --help   help for draft
````

### protocol models

````text
按声明操作验证真实上游模型目录（受权限策略控制）

elysia protocol models [--base-url <URL>] [--api-key <key>]

详细说明：接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。
编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。
模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。
真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。
运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。
save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。
read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。
离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。

Usage:
  elysia protocol models [flags]

Flags:
      --api-key string    真实上游凭据；不写入验证报告
      --base-url string   真实上游地址；仍受出站地址策略限制
  -h, --help              help for models
````

### protocol preview

````text
预览请求、响应、事件或工作流

elysia protocol preview --sample '<JSON>' [--direction <direction>] [--sequence] [--mode mapping|session|task|models|agent]

详细说明：接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。
编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。
模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。
真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。
运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。
save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。
read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。
离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。

Usage:
  elysia protocol preview [flags]

Flags:
      --direction string   映射方向，取值以 protocol schema 为准
  -h, --help               help for preview
      --kind string        任务映射方向：decode、encode 或 control
      --mode string        mapping、session、task、models 或 agent
      --operation string   定义中已声明的操作名
      --purpose string     任务阶段：submit、status、result 或 cancel
      --sample string      完整输入 JSON；真实生成探测使用语义请求
      --sample-id string   定义中已声明的会话样例 ID
      --sequence           将输入作为有序事件序列验证
````

### protocol read

````text
读取协议草稿或不可变修订

elysia protocol read --id <id> [--hash <revision-hash>]

详细说明：接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。
编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。
模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。
真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。
运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。
save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。
read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。
离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。

Usage:
  elysia protocol read [flags]

Flags:
      --hash string   不可变修订的内容哈希；省略时读取草稿
  -h, --help          help for read
      --id string     协议 ID，区分大小写
````

### protocol rollback

````text
Protocol v2 rollback

elysia protocol rollback [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol rollback [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for rollback
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

### protocol save

````text
保存草稿与离线证据，不启用

elysia protocol save [--expected <draft-hash>]

详细说明：接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。
编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。
模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。
真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。
运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。
save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。
read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。
离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。

Usage:
  elysia protocol save [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
  -h, --help              help for save
````

### protocol schema

````text
Protocol v2 schema

elysia protocol schema [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol schema [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for schema
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

### protocol test

````text
验证真实上游契约（受权限策略控制）

elysia protocol test --operation <id> --sample '<semantic request JSON>' [--base-url <URL>] [--api-key <key>]

详细说明：接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。
编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。
模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。
真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。
运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。
save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。
read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。
离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。

Usage:
  elysia protocol test [flags]

Flags:
      --api-key string     真实上游凭据；不写入验证报告
      --base-url string    真实上游地址；仍受出站地址策略限制
  -h, --help               help for test
      --operation string   定义中已声明的操作名
      --sample string      完整输入 JSON；真实生成探测使用语义请求
````

### protocol validate

````text
Protocol v2 validate

elysia protocol validate [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol validate [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for validate
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

### protocol verify

````text
Protocol v2 verify

elysia protocol verify [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]

详细说明：受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it.

Usage:
  elysia protocol verify [flags]

Flags:
      --expected string   保存时为当前草稿哈希；启用/回滚时为当前活动哈希，用于检测并发修改
      --from string       比较的起始修订哈希
      --hash string       不可变修订的内容哈希；省略时读取草稿
  -h, --help              help for verify
      --id string         协议 ID，区分大小写
      --section string    schema 目录分区；省略时返回目录摘要
      --to string         比较的目标修订哈希
      --type string       schema 分区中的具体类型名
````

## usage

````text
用量统计与调用日志

Usage:
  elysia usage [command]

Available Commands:
  log         读取单条调用日志详情（含四段捕获体）
  logs        查询调用日志列表（可过滤错误）
  stats       查询用量汇总与模型分布
  trend       查询用量按日趋势

Flags:
  -h, --help   help for usage

Use "elysia usage [command] --help" for more information about a command.
````

### usage log

````text
读取单条调用日志详情（含四段捕获体）

elysia usage log <requestId>
<requestId>: 调用日志的 requestId

详细说明：按 requestId 读取单条调用日志的完整记录：四段捕获体（入站/出站/上游响应/回给客户端的响应）、重试链、错误详情。用于深入分析失败请求。

Usage:
  elysia usage log [requestId] [flags]

Examples:
  示例：elysia usage log req-123

Flags:
  -h, --help   help for log
````

### usage logs

````text
查询调用日志列表（可过滤错误）

elysia usage logs [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>] [--status success|failed] [--code <状态码>] [--limit <n>]

详细说明：查询调用日志列表（新→旧）：状态码、错误与错误类别、模型、key、耗时、token。status=failed 只看失败请求；需要深入某个请求时用 elysia usage log <requestId> 取捕获的请求/响应体。

Usage:
  elysia usage logs [flags]

Examples:
  示例：elysia usage logs --days 1 --status failed --limit 20

Flags:
      --code string     精确状态码
      --days string     最近 N 天（默认 7，最大 366）
      --from string     起始时间（RFC3339）
      --group string    按模型组过滤
  -h, --help            help for logs
      --key string      按 API Key 名过滤
      --limit string    返回条数（默认 20，最大 100）
      --model string    按模型名过滤
      --status string   success | failed
      --to string       结束时间（RFC3339）
````

### usage stats

````text
查询用量汇总与模型分布

elysia usage stats [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]

详细说明：查询网关用量统计：请求量/成功失败/token 消耗/缓存命中/平均耗时汇总 + 按模型分布。时间窗用 days（默认 7）或 from/to（RFC3339）；可按模型名、key 名、模型组过滤。

Usage:
  elysia usage stats [flags]

Examples:
  示例：elysia usage stats --days 7 --group 主力

Flags:
      --days string    最近 N 天（默认 7，最大 366）
      --from string    起始时间（RFC3339）
      --group string   按模型组过滤
  -h, --help           help for stats
      --key string     按 API Key 名过滤
      --model string   按模型名过滤
      --to string      结束时间（RFC3339）
````

### usage trend

````text
查询用量按日趋势

elysia usage trend [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]

详细说明：查询用量按日趋势（请求数/成功失败/token），适合生成趋势图表。窗口与过滤参数同 elysia usage stats。

Usage:
  elysia usage trend [flags]

Examples:
  示例：elysia usage trend --days 30

Flags:
      --days string    最近 N 天（默认 7，最大 366）
      --from string    起始时间（RFC3339）
      --group string   按模型组过滤
  -h, --help           help for trend
      --key string     按 API Key 名过滤
      --model string   按模型名过滤
      --to string      结束时间（RFC3339）
````

## syslog

````text
查询系统日志

elysia syslog [--level info|warn|error] [--limit <n>]

详细说明：查询系统运行日志（info/warn/error 级别），排查网关自身问题时使用。

Usage:
  elysia syslog [flags]

Examples:
  示例：elysia syslog --level error --limit 50

Flags:
  -h, --help           help for syslog
      --level string   info | warn | error（缺省全部）
      --limit string   返回条数（默认 30，最大 100）
````

## outbound

````text
出站禁止 IP 段（SSRF 防护）

Usage:
  elysia outbound [command]

Available Commands:
  get         查看出站禁止 IP 段（SSRF 防护）
  reset       恢复出站禁止段为预置默认（受权限策略控制）
  set         整体替换出站禁止段（受权限策略控制；高影响）

Flags:
  -h, --help   help for outbound

Use "elysia outbound [command] --help" for more information about a command.
````

### outbound get

````text
查看出站禁止 IP 段（SSRF 防护）

elysia outbound get

详细说明：查看出站禁止 IP 段列表与预置默认（只读，不修改）。

Usage:
  elysia outbound get [flags]

Examples:
  示例：elysia outbound get

Flags:
  -h, --help   help for get
````

### outbound reset

````text
恢复出站禁止段为预置默认（受权限策略控制）

elysia outbound reset

详细说明：把出站禁止 IP 段恢复为内置预置默认（环回/私网/链路本地/组播等，SSRF 防护基线）。丢弃当前自定义列表，执行前先向用户确认。

Usage:
  elysia outbound reset [flags]

Examples:
  示例：elysia outbound reset

Flags:
  -h, --help   help for reset
````

### outbound set

````text
整体替换出站禁止段（受权限策略控制；高影响）

elysia outbound set --ranges <CIDR,...>   # 空列表 = 全放行

详细说明：整体替换出站禁止 IP 段列表（SSRF 防护）。--ranges 给出替换后的完整 CIDR 列表——是整体替换而非增量增删；空列表 = 放行所有地址。上游是本机/内网地址（如 127.0.0.1）被 "refused to dial denied IP" 拦截时，先核实目标地址、服务归属及用户授权，再说明拟变更的具体 CIDR、影响范围和风险；不要仅因请求被拦截就放宽策略。保留无关禁止段，按授权决定是否修改。

Usage:
  elysia outbound set [flags]

Examples:
  示例：elysia outbound set --ranges 10.0.0.0/8,172.16.0.0/12

Flags:
  -h, --help          help for set
      --ranges list   禁止段 CIDR 列表（逗号分隔；空=放行所有）
````

## 文档同步

中文内容由本测试生成；排版修改应落在生成模板，不直接编辑生成文件。英文完整译本人工维护，命令、参数、默认值、约束和示例必须与同次中文生成结果逐项同步。运行时帮助仍使用中文。

在 `backend` 目录重新生成中文，再检查一致性：

```sh
UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
```
