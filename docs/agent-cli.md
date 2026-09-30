# elysia CLI 参考

[文档首页](README.md) · **简体中文** · [English](agent-cli.en.md)

本页由工具定义、命令表和运行时帮助生成。`elysia` 是 `elysia_cli` 的命令语法，不是独立终端程序。下文 `text` 块中的尖括号和省略号表示待替换内容；不是可直接粘贴到 shell 的脚本。

内置助手还提供 `ask_user` 和 `update_plan`，职责及权限见[工具目录](agent-tools-catalog.md)。REST/A2A 遵循会话审批；MCP 持 `agent` 作用域 Key 直接执行，不进入该审批链。MCP 每次调用无状态，草稿和测试目标须在同一次 `command` 批处理中复用，见[远程接入](remote-agent-api.md)。

帮助原文保持运行时内容。阅读时注意：`key create` 的名称提示仍写有“创建后不可改”，但 `key update --new-name` 支持改名；“加密存储”以主密钥成功加载为前提；调用日志正文取决于[捕获策略](deployment.md#请求日志)，默认不保存。`--thinking` 的旧帮助档位也不等同于当前模型能力值 `both` / `non-thinking-only` / `thinking-only`；它与 Agent 的 `thinkingEffort` 是不同字段。协议完整字段以[定义参考](protocol-definition-reference.md)为准。

## 调用契约

执行 Elysia API 网关运维命令。每条命令须以 elysia 开头；首次使用先查 elysia help，具体用法按需查 elysia help <组> [命令]。

```json
{"command":"elysia source ls"}
```

## 总览

```text
elysia —— 网关运维 CLI（全部操作经 elysia_cli 工具执行）

命令组：
  source     模型源管理（create, delete, ls, refresh, update）
  model      单模型管理（ls, rm, set）
  group      模型组管理与成员维护（create, delete, ls, member add, member rm, update）
  key        API Key（推理访问令牌）管理（create, delete, ls, update）
  protocol   自定义协议设计（草稿/离线预览/真实测试/保存）（draft, models, preview, read, save, test）
  usage      用量统计与调用日志（log, logs, stats, trend）
  syslog     系统日志
  outbound   出站禁止 IP 段（SSRF 防护）（get, reset, set）

分级帮助：elysia help <组>（flag 全表）/ elysia help <组> <命令>（完整语义与示例）。

语法：支持 '引号'（'' 表示空值）、--flag value 或 --flag=value、批处理（&& 失败即跳过所在链，; 或换行继续）、
尾管道（| grep <子串> 大小写不敏感过滤、| head <n> 截前 n 个文本行，不代表记录数量；head 也支持 -n N / -N 写法）。
批处理无整体事务或自动回滚；仅合并参数已知、无需观察中间结果的操作。需要根据返回结果决定参数或后续操作时，应分次调用。
查询优先使用命令自身的过滤参数和 --limit；输出可能被截断，应依据实际返回结果判断操作状态。
修改、真实出站和删除受服务端权限与业务策略控制。

常用组合示例：
  elysia source ls && elysia model ls --source 主源 --limit 20
  elysia usage logs --days 1 --status failed --limit 10
  elysia group create --name 主力 --models s1:gpt-4o
```

## source

````text
elysia source — 模型源管理

  elysia source create --name <名> --base-url <URL> [--platform openai|anthropic|gemini|responses|custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]
    创建模型源（受权限策略控制）
      --name                   源名称（显示用）
      --base-url               上游 baseUrl（http/https）
      --platform               openai（默认）/anthropic/gemini/responses/custom:<协议ID>
      --api-key                API key（加密存储）
      --auto-fetch             标记为自动源（创建后如需上游实时模型列表，执行 elysia source refresh）
      --manual-models          手动模型名列表（逗号分隔）
      --fetch-base-url         模型列表拉取地址（缺省同 base-url）

  elysia source delete --source <id|名>
    删除模型源（不可逆，受权限策略控制；级联删模型与组引用）
      --source                 源 id 或名称

  elysia source ls
    列出全部模型源（密钥脱敏）

  elysia source refresh --source <id|名>
    从上游拉取模型列表（真实出站，受权限策略控制）
      --source                 源 id 或名称

  elysia source update --source <id|名> [--enabled] [--name <名>] [--base-url <URL>] [--platform <平台>] [--api-key <新key>] [--auto-fetch[=false]] [--manual-models a,b]
    修改模型源（受权限策略控制；api-key 留空=保留）
      --source                 源 id 或名称
      --enabled                启停
      --name                   改名
      --base-url               换 baseUrl
      --platform               换平台
      --api-key                新 API key（留空=保留原值）
      --auto-fetch             自动源标记开关
      --manual-models          整体替换手动模型列表

完整语义与示例：elysia help source <命令>。
````

### source create

````text
elysia source create --name <名> --base-url <URL> [--platform openai|anthropic|gemini|responses|custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]
创建模型源（受权限策略控制）

参数：
  --name                   源名称（显示用）
  --base-url               上游 baseUrl（http/https）
  --platform               openai（默认）/anthropic/gemini/responses/custom:<协议ID>
  --api-key                API key（加密存储）
  --auto-fetch             标记为自动源（创建后如需上游实时模型列表，执行 elysia source refresh）
  --manual-models          手动模型名列表（逗号分隔）
  --fetch-base-url         模型列表拉取地址（缺省同 base-url）

示例：elysia source create --name 主源 --base-url https://api.example.com --api-key sk-xxx

详细说明：创建模型源（受服务端权限与业务策略控制）。platform 支持 openai/anthropic/gemini/responses 或 custom:<协议ID>；autoFetchModels=true 标记为自动源（模型列表仍需创建后运行 elysia source refresh 拉取，或用户在页面手动拉取）；手动模型用手动列表或 manualModels。创建前先向用户确认 baseUrl、平台与密钥来源。
````

### source delete

````text
elysia source delete --source <id|名>
删除模型源（不可逆，受权限策略控制；级联删模型与组引用）

参数：
  --source                 源 id 或名称

示例：elysia source delete --source 主源

详细说明：删除模型源（受服务端权限与业务策略控制，不可逆）：源下全部模型与组内成员引用一并级联删除。source 用源 id 或名称指定。删除前先向用户核对对象与影响面（模型数、受影响的组）。
````

### source ls

````text
elysia source ls
列出全部模型源（密钥脱敏）


示例：elysia source ls

详细说明：列出全部模型源：平台、baseUrl、启停、自动拉取、密钥策略（脱敏）、模型数与最近拉取状态。不显示任何密钥明文。
````

### source refresh

````text
elysia source refresh --source <id|名>
从上游拉取模型列表（真实出站，受权限策略控制）

参数：
  --source                 源 id 或名称

示例：elysia source refresh --source 主源

详细说明：从上游拉取某模型源的模型列表（受服务端权限与业务策略控制，真实出站请求）。手动源则会同步手动模型列表。新建自动拉取源后用它（elysia source refresh）取回模型。上游返回空列表时刷新会报错并保留现有缓存；这不证明从未刷新，也不能单独确定上游为空的原因。
````

### source update

````text
elysia source update --source <id|名> [--enabled] [--name <名>] [--base-url <URL>] [--platform <平台>] [--api-key <新key>] [--auto-fetch[=false]] [--manual-models a,b]
修改模型源（受权限策略控制；api-key 留空=保留）

参数：
  --source                 源 id 或名称
  --enabled                启停
  --name                   改名
  --base-url               换 baseUrl
  --platform               换平台
  --api-key                新 API key（留空=保留原值）
  --auto-fetch             自动源标记开关
  --manual-models          整体替换手动模型列表

示例：elysia source update --source 主源 --enabled=false

详细说明：修改已有模型源（受服务端权限与业务策略控制）：启停、改名、换 baseUrl/平台、更换 API key（留空=保留原 key）、调整自动拉取或手动模型列表。source 用源 id 或名称指定。
````

## model

````text
elysia model — 单模型管理

  elysia model ls [--source <id|名>] [--search <子串>] [--limit <n>]
    查询本地缓存的模型清单（可按源过滤；上游实时列表走 source refresh）
      --source                 源 id 或名称
      --search                 名称模糊匹配
      --limit                  返回条数（默认 50，上限 200）

  elysia model rm --source <id|名> --model <模型id>
    删除单个模型（不可逆，受权限策略控制）
      --source                 源 id 或名称
      --model                  模型 id

  elysia model set --source <id|名> --model <模型id> [--name <名>] [--type <类型>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <模式>] [--enabled[=false]]
    修改单个模型（受权限策略控制）
      --source                 源 id 或名称
      --model                  模型 id
      --name                   改名
      --type                   类型（llm 默认 / reranker / embedding 预留）
      --max-tokens             maxTokens
      --vision                 视觉能力标记
      --tools                  工具能力标记
      --structured             结构化输出标记
      --thinking               思考模式（disabled 默认 / enabled / adaptive）
      --enabled                启停

完整语义与示例：elysia help model <命令>。
````

### model ls

````text
elysia model ls [--source <id|名>] [--search <子串>] [--limit <n>]
查询本地缓存的模型清单（可按源过滤；上游实时列表走 source refresh）

参数：
  --source                 源 id 或名称
  --search                 名称模糊匹配
  --limit                  返回条数（默认 50，上限 200）

示例：elysia model ls --source 主源 --search gpt --limit 20

详细说明：查询网关本地缓存的模型清单，不代表上游实时状态（默认全部源）。source 传源 id 或名称可按源过滤；search 按名称模糊匹配。建模型组前用它确认可用的模型 id。只给前 limit 条（默认 50、上限 200），总量在 summary 里。空列表不能单独证明未刷新：先检查 source/search 查询条件与 elysia source ls 返回的源状态；需要实时信息时再 elysia source refresh --source <源>，随后重新查询。
````

### model rm

````text
elysia model rm --source <id|名> --model <模型id>
删除单个模型（不可逆，受权限策略控制）

参数：
  --source                 源 id 或名称
  --model                  模型 id

示例：elysia model rm --source 主源 --model gpt-4o

详细说明：删除单个模型（受服务端权限与业务策略控制，不可逆）：组内引用一并清理。自动拉取的模型下次刷新可能重新出现；想临时下线优先用 elysia model set --source <源> --model <模型id> --enabled=false。source 用源 id 或名称，model 是模型 id。
````

### model set

````text
elysia model set --source <id|名> --model <模型id> [--name <名>] [--type <类型>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <模式>] [--enabled[=false]]
修改单个模型（受权限策略控制）

参数：
  --source                 源 id 或名称
  --model                  模型 id
  --name                   改名
  --type                   类型（llm 默认 / reranker / embedding 预留）
  --max-tokens             maxTokens
  --vision                 视觉能力标记
  --tools                  工具能力标记
  --structured             结构化输出标记
  --thinking               思考模式（disabled 默认 / enabled / adaptive）
  --enabled                启停

示例：elysia model set --source 主源 --model gpt-4o --vision --enabled=false

详细说明：修改单个模型（受服务端权限与业务策略控制）：启停、改名、类型、maxTokens、能力标记（视觉/工具/结构化）、思考模式。能力字段被修改后刷新不再覆盖（capability_source=manual）。source 用源 id 或名称，model 是模型 id（先 elysia model ls 查）。
````

## group

````text
elysia group — 模型组管理与成员维护

  elysia group create --name <组名> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
    创建模型组（受权限策略控制）
      --name                   组名（客户端调用时用的模型名）
      --models                 成员模型引用（sourceId:modelId 或模型名，逗号分隔）
      --strategy               调度策略：sequential=失败回退（按序调用，前败后补）/ random=随机起点环绕 / round-robin=游标轮询（默认）
      --max-retries            失败重试次数（默认 3）
      --enabled                默认 true
      --max-concurrency        并发上限（0=不限）
      --daily-limit-requests   每日请求上限（0=不限）
      --daily-limit-tokens     每日 token 上限（0=不限）

  elysia group delete --group <组名|id>
    删除模型组（不可逆，受权限策略控制；可能级联禁用 Key）
      --group                  组名或 id

  elysia group ls
    列出全部模型组及成员

  elysia group member add --group <组名|id> --models <列表>
    向模型组追加成员（受权限策略控制）
      --group                  组名或 id
      --models                 成员引用（sourceId:modelId 或模型名）

  elysia group member rm --group <组名|id> --models <列表>
    从模型组移除成员（受权限策略控制）
      --group                  组名或 id
      --models                 成员引用

  elysia group update --group <组名|id> [--name <新组名>] [--add-models <列表>] [--remove-models <列表>] [--enabled[=false]] [--strategy <策略>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
    修改模型组（受权限策略控制；改名/成员增删/策略/限额）
      --group                  组名或 id
      --name                   改名（组名即客户端调用的模型名；引用旧名的 Key 授权不会自动迁移）
      --add-models             追加成员
      --remove-models          移除成员
      --enabled                启停
      --strategy               调度策略：sequential=失败回退 / random=随机起点环绕 / round-robin=游标轮询（默认）
      --max-retries            重试次数
      --max-concurrency        并发上限
      --daily-limit-requests   每日请求上限
      --daily-limit-tokens     每日 token 上限

完整语义与示例：elysia help group <命令>。
````

### group create

````text
elysia group create --name <组名> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
创建模型组（受权限策略控制）

参数：
  --name                   组名（客户端调用时用的模型名）
  --models                 成员模型引用（sourceId:modelId 或模型名，逗号分隔）
  --strategy               调度策略：sequential=失败回退（按序调用，前败后补）/ random=随机起点环绕 / round-robin=游标轮询（默认）
  --max-retries            失败重试次数（默认 3）
  --enabled                默认 true
  --max-concurrency        并发上限（0=不限）
  --daily-limit-requests   每日请求上限（0=不限）
  --daily-limit-tokens     每日 token 上限（0=不限）

示例：elysia group create --name 主力 --models s1:gpt-4o,s1:gpt-4o-mini

详细说明：创建模型组（受服务端权限与业务策略控制）：模型引用列表（sourceId:modelId 或模型名）、调度策略、重试。名称需唯一；创建前先用 elysia model ls 确认成员引用，再用 elysia group ls 核对重名。
````

### group delete

````text
elysia group delete --group <组名|id>
删除模型组（不可逆，受权限策略控制；可能级联禁用 Key）

参数：
  --group                  组名或 id

示例：elysia group delete --group 退役组

详细说明：删除模型组（受服务端权限与业务策略控制，不可逆）：客户端将无法再按该组名调用。若某些 API Key 只授权了这一个组，会随删除级联禁用（名单在结果里返回）。group 用组名或 id 指定。
````

### group ls

````text
elysia group ls
列出全部模型组及成员


示例：elysia group ls

详细说明：列出全部模型组：名称、启停、策略、重试、并发/限额与成员（sourceId:modelId 引用，也可能只显示模型 id）。
````

### group member add

````text
elysia group member add --group <组名|id> --models <列表>
向模型组追加成员（受权限策略控制）

参数：
  --group                  组名或 id
  --models                 成员引用（sourceId:modelId 或模型名）

示例：elysia group member add --group 主力 --models s1:o1,s1:o2

详细说明：修改已有模型组（受服务端权限与业务策略控制）：改名、启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。改名注意：组名是客户端调用的模型名，改名后客户端调用名随之变化；引用旧组名的 API Key 授权（allowedGroups）不会自动迁移，改名前先向用户确认影响。
````

### group member rm

````text
elysia group member rm --group <组名|id> --models <列表>
从模型组移除成员（受权限策略控制）

参数：
  --group                  组名或 id
  --models                 成员引用

示例：elysia group member rm --group 主力 --models s1:o2

详细说明：修改已有模型组（受服务端权限与业务策略控制）：改名、启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。改名注意：组名是客户端调用的模型名，改名后客户端调用名随之变化；引用旧组名的 API Key 授权（allowedGroups）不会自动迁移，改名前先向用户确认影响。
````

### group update

````text
elysia group update --group <组名|id> [--name <新组名>] [--add-models <列表>] [--remove-models <列表>] [--enabled[=false]] [--strategy <策略>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
修改模型组（受权限策略控制；改名/成员增删/策略/限额）

参数：
  --group                  组名或 id
  --name                   改名（组名即客户端调用的模型名；引用旧名的 Key 授权不会自动迁移）
  --add-models             追加成员
  --remove-models          移除成员
  --enabled                启停
  --strategy               调度策略：sequential=失败回退 / random=随机起点环绕 / round-robin=游标轮询（默认）
  --max-retries            重试次数
  --max-concurrency        并发上限
  --daily-limit-requests   每日请求上限
  --daily-limit-tokens     每日 token 上限

示例：elysia group update --group 主力 --add-models s1:o1 --enabled=false

详细说明：修改已有模型组（受服务端权限与业务策略控制）：改名、启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。改名注意：组名是客户端调用的模型名，改名后客户端调用名随之变化；引用旧组名的 API Key 授权（allowedGroups）不会自动迁移，改名前先向用户确认影响。
````

## key

````text
elysia key — API Key（推理访问令牌）管理

  elysia key create --name <名> [--secret <明文>] [--allowed-groups <组,...>] [--enabled[=false]]
    创建推理 API Key（受权限策略控制；secret 留空自动生成，明文仅返回一次）
      --name                   Key 名称（主键，创建后不可改）
      --secret                 Key 明文——用户给定就用给定值（弱口令可提醒但不拒绝）；留空自动生成随机值
      --allowed-groups         允许访问的模型组（逗号分隔；空=不限制）
      --enabled                默认 true

  elysia key delete --name <名>
    删除 API Key（不可逆，受权限策略控制；远程访问 Key 拒绝）
      --name                   Key 名称

  elysia key ls
    查询 API Key 列表（脱敏）

  elysia key update --name <名> [--new-name <新名>] [--enabled[=false]] [--allowed-groups <组,...>] [--new-secret <新明文>]
    修改 API Key（受权限策略控制；改名/启停/授权/换明文；远程访问 Key 拒绝）
      --name                   Key 名称
      --new-name               改名（目标名被占用会报错）
      --enabled                启停
      --allowed-groups         整体替换允许访问的模型组
      --new-secret             新明文；留空保留原值

完整语义与示例：elysia help key <命令>。
````

### key create

````text
elysia key create --name <名> [--secret <明文>] [--allowed-groups <组,...>] [--enabled[=false]]
创建推理 API Key（受权限策略控制；secret 留空自动生成，明文仅返回一次）

参数：
  --name                   Key 名称（主键，创建后不可改）
  --secret                 Key 明文——用户给定就用给定值（弱口令可提醒但不拒绝）；留空自动生成随机值
  --allowed-groups         允许访问的模型组（逗号分隔；空=不限制）
  --enabled                默认 true

示例：elysia key create --name mobile-app --allowed-groups 主力

详细说明：创建 API Key，即客户端调用 /v1 接口用的推理访问令牌（受服务端权限与业务策略控制）。用户给定 secret 明文就按给定值原样创建（弱口令可提醒风险，但不代为拒绝）；留空则自动生成随机明文。完整明文只在本次结果里返回一次，请提醒用户立即保存。allowedGroups 为空表示可访问全部模型组（扩权面大，创建前先向用户确认授权范围）。远程访问 Key（驱动 AI 助手的那类）由用户在运行配置页管理，elysia key 命令不能创建。
````

### key delete

````text
elysia key delete --name <名>
删除 API Key（不可逆，受权限策略控制；远程访问 Key 拒绝）

参数：
  --name                   Key 名称

示例：elysia key delete --name mobile-app

详细说明：删除 API Key（受服务端权限与业务策略控制，不可逆）：使用该 Key 的客户端将立即无法调用。远程访问 Key（agent 作用域）请在运行配置页删除。删除前先向用户核对名称。
````

### key ls

````text
elysia key ls
查询 API Key 列表（脱敏）


示例：elysia key ls

详细说明：查询 API Key（访问令牌）列表。token 脱敏显示；allowedGroups 为空表示可访问全部模型组。带 agent 作用域的是远程访问 Key（驱动 AI 助手专用，不参与推理），由用户在运行配置页管理——不可对其做写操作。
````

### key update

````text
elysia key update --name <名> [--new-name <新名>] [--enabled[=false]] [--allowed-groups <组,...>] [--new-secret <新明文>]
修改 API Key（受权限策略控制；改名/启停/授权/换明文；远程访问 Key 拒绝）

参数：
  --name                   Key 名称
  --new-name               改名（目标名被占用会报错）
  --enabled                启停
  --allowed-groups         整体替换允许访问的模型组
  --new-secret             新明文；留空保留原值

示例：elysia key update --name mobile-app --allowed-groups 主力,备用

详细说明：修改已有 API Key（受服务端权限与业务策略控制）：改名（newName，目标名被占用会报错）、启停、调整可访问的模型组、更换明文（newSecret 留空=保留原值）。远程访问 Key（agent 作用域）由用户在运行配置页管理，elysia key 命令不可修改。allowedGroups 为空表示不限制（可访问全部模型组），调整前先向用户确认。
````

## protocol

````text
elysia protocol — 自定义协议设计（草稿/离线预览/真实测试/保存）

  elysia protocol draft '<完整配置 JSON>' [--example '<响应示例 JSON>']
    写入/更新协议配置草稿（立即校验并离线验证）
      --example                上游响应示例 JSON，用于离线检验 response 映射
      <config>                 完整 CustomProtocolConfig JSON（建议用单引号包裹）

  elysia protocol models [--base-url <URL>] [--api-key <key>]
    按草稿 models 配置试拉上游模型列表（受权限策略控制）
      --base-url               上游 baseUrl（缺省用当前 CLI 上下文已记录的）
      --api-key                API key（缺省用当前 CLI 上下文已记录的）

  elysia protocol preview [--sample '<样例 Maheshvara 请求 JSON>']
    离线渲染草稿请求（不发送）
      --sample                 自定义样例请求 JSON

  elysia protocol read --id <协议id>
    读取已保存协议的完整配置
      --id                     协议 id（含内置预置协议）

  elysia protocol save [--update <协议id>]
    把当前草稿保存为正式协议（受权限策略控制）
      --update                 显式更新已有协议；目标必须存在且与草稿 id 一致

  elysia protocol test [--base-url <URL>] [--api-key <key>] [--stream] [--sample '<样例请求 JSON>']
    向真实上游发送一次测试请求（受权限策略控制）
      --base-url               用户提供的上游 baseUrl（缺省用当前 CLI 上下文已记录的）
      --api-key                用户提供的 API key（缺省用当前 CLI 上下文已记录的）
      --stream                 按流式（SSE）测试
      --sample                 自定义样例请求 JSON

完整语义与示例：elysia help protocol <命令>。
````

### protocol draft

````text
elysia protocol draft '<完整配置 JSON>' [--example '<响应示例 JSON>']
写入/更新协议配置草稿（立即校验并离线验证）

参数：
  --example                上游响应示例 JSON，用于离线检验 response 映射
  <config>                 完整 CustomProtocolConfig JSON（建议用单引号包裹）

示例：elysia protocol draft '{"id":"my-api","request":{...}}' --example '{"text":"hi"}'

详细说明：接入工作流：
1. 阅读用户给的 API 文档/示例，识别：认证方式、端点路径、请求/响应结构、是否 SSE 流式、模型列表端点（若有）。
2. elysia protocol draft '<完整配置 JSON>' 提交草稿（有响应示例时带 --example '<示例 JSON>' 做离线映射校验）；校验失败会返回 issues，修复后重新提交。
3. elysia protocol preview 离线自查请求形状，再向用户询问测试目标（baseUrl / API key），elysia protocol test --stream 与 elysia protocol models 真实测试（受权限策略控制；凭证用 --base-url / --api-key 传入），按结果修正。
4. 通过后 elysia protocol save 保存；接入调用还需配套模型源（--platform custom:<协议id>）与模型组。

顶层字段：id（必填，短的小写英文标识）、name、version、type（llm 默认 / reranker / embedding 预留 / x- 前缀扩展）、request（必填）、response。

### request（网关 → 上游）
- method：GET/POST/PUT/PATCH/DELETE，默认 POST。
- path：相对模型源 baseUrl 的路径，支持 {{maheshvara.<字段>}} 插值，不能含 scheme。
- pathStream：流式请求的路径覆盖（如 Gemini 的 :streamGenerateContent?alt=sse）。
- headers / query：静态键值对（不得含认证头，认证走 auth）。
- contentType：默认 application/json。
- auth：{"mode": "bearer|none|header|query", "header": "...", "prefix": "...", "query": "..."}。Bearer/token 类用 bearer（默认）；X-Api-Key 类用 header；key 参数类用 query；无需认证用 none。
- body：请求体的字段级构造树。容器为普通 JSON 对象/数组；叶子二选一：
  - 字段引用 {"field": "<请求字段目录中的字段>", "mode": "json|string", "default": <可选 JSON 字面量>, "omitIfEmpty": <可选 true>}。mode json 以原生 JSON 值插入（对象/数组/数字/布尔必须用它）；mode string 以字符串插入。
  - 常量 {"value": <任意 JSON>}：上游必填但 Maheshvara 无对应的字段（版本号、固定格式参数等）用它。

### response（上游 → Maheshvara，只支持 JSON 响应体）
- body：返回体构造树（推荐）。容器为普通 JSON 对象/数组，结构按上游示例响应搭建；叶子为映射标注 {"field": "<响应字段目录中的字段>", "value": <示例值>, "transform": "<可选>"} 或纯占位 {"value": ...}。数组层级按示例保留（如 choices 数组只需在第 0 项标注映射）。
- fields：等效行列表 [{"path", "field", "transform"?}]，path 支持点路径与数组下标（choices[0].delta.content）。与 body 二选一。
- 尽量映射完整：text/reasoning/tool_calls/usage/stop_reason/id/model/error，文档里有就配。
- stream：仅当文档描述 SSE 流式时配置 {"payloadPath": "...", "mode": "delta|cumulative", "events": [...], "doneValues": ["[DONE]"], "response": {"body": {...} 或 "fields": [...]}}。

请求字段目录（request.body 叶子 field 可用值）：
- model — 模型名（路由后实际使用的上游模型）（string）
- instructions — 系统指令（string）
- messages — 消息数组（role + content）（native）
- input_items — Responses 风格输入项（native）
- stream — 是否流式（scalar）
- stream_options — 流式选项（native）
- tools — 工具定义数组（native）
- tool_choice — 工具选择策略（native）
- parallel_tool_calls — 允许并行工具调用（scalar）
- max_output_tokens — 最大输出 token 数（scalar）
- min_output_tokens — 最小输出 token 数（scalar）
- temperature — 温度（scalar）
- top_p — Top-P（scalar）
- top_k — Top-K（scalar）
- stop — 停止序列（native）
- n — 候选数量（scalar）
- seed — 随机种子（scalar）
- presence_penalty — Presence 惩罚（scalar）
- frequency_penalty — Frequency 惩罚（scalar）
- repetition_penalty — 重复惩罚（scalar）
- logprobs — 是否返回 logprobs（scalar）
- top_logprobs — logprobs 数量（scalar）
- typical_p — Typical-P（scalar）
- min_p — Min-P（scalar）
- top_a — Top-A（scalar）
- response_format — 响应格式（JSON schema 等）（native）
- reasoning — 推理配置（effort 等）（native）
- thinking — 思考配置（budget_tokens 等）（native）
- reasoning_effort — 推理力度（chat shape 整形产出）（string）
- output_config — 输出配置（anthropic shape 自适应思考档位）（native）
- thinking_config — 思考配置（gemini shape 整形产出）（native）
- tool_config — 工具选择配置（gemini shape 整形产出）（native）
- modalities — 输出模态（native）
- audio — 音频配置（native）
- safety_settings — 安全设置（native）
- service_tier — 服务层级（string）
- verbosity — 输出详细度（string）
- user — 终端用户标识（string）
- include — Responses include 列表（shape 会联动追加加密思考）（native）
- prompt_cache_key — 提示缓存键（string）
- metadata — 元数据（native）
- raw_extra — 客户端透传的未知字段（别名 extra）（native）

响应字段目录（response 映射的 field 可用值）：
- text — 正文文本
- reasoning — 思考文本
- tool_calls — 工具调用数组
- usage — 用量对象（多键名自动识别）
- usage.input_tokens — 输入 token 数
- usage.output_tokens — 输出 token 数
- usage.total_tokens — 总 token 数
- usage.cached_input_tokens — 缓存命中输入 token 数
- usage.reasoning_tokens — 思考 token 数
- stop_reason — 结束原因
- id — 响应 ID
- model — 模型名
- status — 状态
- error — 错误对象
- created_at — 创建时间戳（秒）
- service_tier — 服务层级
- system_fingerprint — 系统指纹
- metadata — 元数据（可用 metadata.<key> 子键）
- output — 输出项（结构化，配 output_items 等 transform）
- metadata 可带子键（如 metadata.vendor），用于携带上游特有元数据。

transform 可选值（一般无需指定；usage.* 默认已按 int 处理）：
、identity、raw、string、text、join、int、integer、number、float、bool、boolean、json、parse_json、json_string、timestamp_ms、first、usage、content_parts、tool_calls、output_items

设计要点：
- usage 整体对象直接用 field "usage"（键名自动识别）；只有结构特殊时才逐项映射。
- 从示例响应/截图推断结构时，数组层级不能丢。
- 上游要求的固定参数（版本号、格式等）用常量 value 提供。
- 消息/工具与某线制同形时优先用 request.shape（openai-chat/anthropic/gemini/responses）复用内置整形。
- 条件包含用叶子的 when 与 omitIf；流式终止判定用 stream.finishWhen/statusWhen，类型化终止值用 stream.done。
- 每类事件形状不同的流用 stream.frames（事件名或 match 谓词选帧）；工具调用分帧到达时用 frame.tool。
- 键名不符合内置别名表时用 aliases 声明；数组内按类型分块提取用 textFilter/reasoningFilter。
- 需要参考成熟写法时可先 `elysia protocol read --id <id>` 读取内置预置协议（chat-completions-api / responses-api / anthropic-api / gemini-api）。

完整范例（预置协议 anthropic-api）：
```json
{"id":"anthropic-api","name":"Anthropic API（预置）","version":"3","type":"llm","request":{"method":"POST","path":"/v1/messages","shape":"anthropic","headers":{"anthropic-version":"2023-06-01"},"auth":{"mode":"header","header":"x-api-key"},"body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"system":{"field":"instructions","mode":"string","omitIfEmpty":true},"max_tokens":{"field":"max_output_tokens","default":65536},"stream":{"field":"stream"},"temperature":{"field":"temperature","omitIfEmpty":true},"top_p":{"field":"top_p","omitIfEmpty":true},"top_k":{"field":"top_k","omitIfEmpty":true},"stop_sequences":{"field":"stop","omitIfEmpty":true},"thinking":{"field":"thinking","omitIfEmpty":true},"output_config":{"field":"output_config","omitIfEmpty":true},"tools":{"field":"tools","omitIfEmpty":true},"tool_choice":{"field":"tool_choice","omitIfEmpty":true}}},"response":{"textPath":"content","textFilter":[{"path":"type","op":"equals","value":"text"}],"reasoningPath":"content","reasoningFilter":[{"path":"type","op":"equals","value":"thinking"}],"toolCallsPath":"content","usagePath":"usage","finishReasonPath":"stop_reason","stream":{"frames":[{"event":"error","response":{"errorPath":"error"}},{"event":"message_start","response":{"usagePath":"message.usage"}},{"event":"content_block_start","match":{"path":"content_block.type","op":"equals","value":"tool_use"},"tool":{"idPath":"content_block.id","indexPath":"index","namePath":"content_block.name"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"text_delta"},"response":{"textPath":"delta.text"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"thinking_delta"},"response":{"reasoningPath":"delta.thinking"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"signature_delta"},"response":{"signaturePath":"delta.signature","signatureProvider":"anthropic"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"citations_delta"},"response":{"citationsPath":"delta.citation"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"input_json_delta"},"tool":{"indexPath":"index","argumentsPath":"delta.partial_json"}},{"event":"content_block_stop","tool":{"indexPath":"index"},"toolDone":true},{"event":"message_delta","response":{"usagePath":"usage","finishReasonPath":"delta.stop_reason"},"terminal":true}]}},"models":{"path":"/v1/models","listPath":"data"},"aliases":{"textKeys":["text","content","message","value","output","thinking"],"usage":{"cache_creation":["cache_creation_input_tokens"],"cache_read":["cache_read_input_tokens"]},"toolCall":{"arguments":["input"],"id":["id"],"name":["name"]}},"metadata":{"description":"Anthropic API 线制的预置定义；v3：命名去厂商化；v2：thinking/output_config 整形、signature 与 citations 帧、参数完成帧、缓存用量别名","preset":true,"presetVersion":3}}
```

````

### protocol models

````text
elysia protocol models [--base-url <URL>] [--api-key <key>]
按草稿 models 配置试拉上游模型列表（受权限策略控制）

参数：
  --base-url               上游 baseUrl（缺省用当前 CLI 上下文已记录的）
  --api-key                API key（缺省用当前 CLI 上下文已记录的）

示例：elysia protocol models --base-url https://api.example.com

详细说明：按当前草稿的 models 发现配置向真实上游（受服务端权限与业务策略控制）请求模型列表并解析，用于验证发现端点配置。草稿未声明 models 配置时会报错。用户在对话中给出 baseUrl / API key 时作为参数传入；仅在当前 CLI 上下文中复用本命令或 protocol test 已成功记录的测试目标；其他命令的密钥不在此复用。
````

### protocol preview

````text
elysia protocol preview [--sample '<样例 Maheshvara 请求 JSON>']
离线渲染草稿请求（不发送）

参数：
  --sample                 自定义样例请求 JSON

示例：elysia protocol preview

详细说明：用样例 Maheshvara 请求离线渲染当前草稿的出站请求（method/path/query/headers/body/凭证注入形态）。不发起真实上游请求。用于提交草稿后自查请求形状。
````

### protocol read

````text
elysia protocol read --id <协议id>
读取已保存协议的完整配置

参数：
  --id                     协议 id（含内置预置协议）

示例：elysia protocol read --id anthropic-api

详细说明：按 id 读取一条已保存协议（含内置预置协议）的完整配置 JSON，作为写法参考或编辑基准。
````

### protocol save

````text
elysia protocol save [--update <协议id>]
把当前草稿保存为正式协议（受权限策略控制）

参数：
  --update                 显式更新已有协议；目标必须存在且与草稿 id 一致

示例：elysia protocol save

详细说明：把当前草稿保存进协议注册表（受服务端权限与业务策略控制，写入即热生效）。编辑模式必须保持原协议 id；其余上下文默认新建，同名协议会被拒绝；更新已有协议须显式传 --update <协议id>，目标必须存在且与草稿 id 一致。建议在真实测试通过后再请求保存。
````

### protocol test

````text
elysia protocol test [--base-url <URL>] [--api-key <key>] [--stream] [--sample '<样例请求 JSON>']
向真实上游发送一次测试请求（受权限策略控制）

参数：
  --base-url               用户提供的上游 baseUrl（缺省用当前 CLI 上下文已记录的）
  --api-key                用户提供的 API key（缺省用当前 CLI 上下文已记录的）
  --stream                 按流式（SSE）测试
  --sample                 自定义样例请求 JSON

示例：elysia protocol test --base-url https://api.example.com --api-key sk-xxx

详细说明：把当前草稿渲染成请求并发送到真实上游（受服务端权限与业务策略控制），返回 HTTP 状态、原文、映射结果；stream=true 时采样 SSE 事件与解码结果。用户在对话中给出 baseUrl / API key 时作为参数传入；仅在当前 CLI 上下文中复用本命令或 protocol models 传入并成功记录的 baseUrl/API key；其他命令的密钥不在此复用。
````

## usage

````text
elysia usage — 用量统计与调用日志

  elysia usage log <requestId>
    读取单条调用日志详情（含四段捕获体）
      <requestId>              调用日志的 requestId

  elysia usage logs [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>] [--status success|failed] [--code <状态码>] [--limit <n>]
    查询调用日志列表（可过滤错误）
      --days                   最近 N 天（默认 7，最大 366）
      --from                   起始时间（RFC3339）
      --to                     结束时间（RFC3339）
      --model                  按模型名过滤
      --key                    按 API Key 名过滤
      --group                  按模型组过滤
      --status                 success | failed
      --code                   精确状态码
      --limit                  返回条数（默认 20，最大 100）

  elysia usage stats [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]
    查询用量汇总与模型分布
      --days                   最近 N 天（默认 7，最大 366）
      --from                   起始时间（RFC3339）
      --to                     结束时间（RFC3339）
      --model                  按模型名过滤
      --key                    按 API Key 名过滤
      --group                  按模型组过滤

  elysia usage trend [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]
    查询用量按日趋势
      --days                   最近 N 天（默认 7，最大 366）
      --from                   起始时间（RFC3339）
      --to                     结束时间（RFC3339）
      --model                  按模型名过滤
      --key                    按 API Key 名过滤
      --group                  按模型组过滤

完整语义与示例：elysia help usage <命令>。
````

### usage log

````text
elysia usage log <requestId>
读取单条调用日志详情（含四段捕获体）

  <requestId>              调用日志的 requestId

示例：elysia usage log req-123

详细说明：按 requestId 读取单条调用日志的完整记录：四段捕获体（入站/出站/上游响应/回给客户端的响应）、重试链、错误详情。用于深入分析失败请求。
````

### usage logs

````text
elysia usage logs [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>] [--status success|failed] [--code <状态码>] [--limit <n>]
查询调用日志列表（可过滤错误）

参数：
  --days                   最近 N 天（默认 7，最大 366）
  --from                   起始时间（RFC3339）
  --to                     结束时间（RFC3339）
  --model                  按模型名过滤
  --key                    按 API Key 名过滤
  --group                  按模型组过滤
  --status                 success | failed
  --code                   精确状态码
  --limit                  返回条数（默认 20，最大 100）

示例：elysia usage logs --days 1 --status failed --limit 20

详细说明：查询调用日志列表（新→旧）：状态码、错误与错误类别、模型、key、耗时、token。status=failed 只看失败请求；需要深入某个请求时用 elysia usage log <requestId> 取捕获的请求/响应体。
````

### usage stats

````text
elysia usage stats [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]
查询用量汇总与模型分布

参数：
  --days                   最近 N 天（默认 7，最大 366）
  --from                   起始时间（RFC3339）
  --to                     结束时间（RFC3339）
  --model                  按模型名过滤
  --key                    按 API Key 名过滤
  --group                  按模型组过滤

示例：elysia usage stats --days 7 --group 主力

详细说明：查询网关用量统计：请求量/成功失败/token 消耗/缓存命中/平均耗时汇总 + 按模型分布。时间窗用 days（默认 7）或 from/to（RFC3339）；可按模型名、key 名、模型组过滤。
````

### usage trend

````text
elysia usage trend [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <名>] [--key <名>] [--group <名>]
查询用量按日趋势

参数：
  --days                   最近 N 天（默认 7，最大 366）
  --from                   起始时间（RFC3339）
  --to                     结束时间（RFC3339）
  --model                  按模型名过滤
  --key                    按 API Key 名过滤
  --group                  按模型组过滤

示例：elysia usage trend --days 30

详细说明：查询用量按日趋势（请求数/成功失败/token），适合生成趋势图表。窗口与过滤参数同 elysia usage stats。
````

## syslog

````text
elysia syslog [--level info|warn|error] [--limit <n>]
查询系统日志

参数：
  --level                  info | warn | error（缺省全部）
  --limit                  返回条数（默认 30，最大 100）

示例：elysia syslog --level error --limit 50

详细说明：查询系统运行日志（info/warn/error 级别），排查网关自身问题时使用。
````

## outbound

````text
elysia outbound — 出站禁止 IP 段（SSRF 防护）

  elysia outbound get
    查看出站禁止 IP 段（SSRF 防护）

  elysia outbound reset
    恢复出站禁止段为预置默认（受权限策略控制）

  elysia outbound set --ranges <CIDR,...>   # 空列表 = 全放行
    整体替换出站禁止段（受权限策略控制；高影响）
      --ranges                 禁止段 CIDR 列表（逗号分隔；空=放行所有）

完整语义与示例：elysia help outbound <命令>。
````

### outbound get

````text
elysia outbound get
查看出站禁止 IP 段（SSRF 防护）


示例：elysia outbound get

详细说明：查看出站禁止 IP 段列表与预置默认（只读，不修改）。
````

### outbound reset

````text
elysia outbound reset
恢复出站禁止段为预置默认（受权限策略控制）


示例：elysia outbound reset

详细说明：把出站禁止 IP 段恢复为内置预置默认（环回/私网/链路本地/组播等，SSRF 防护基线）。丢弃当前自定义列表，执行前先向用户确认。
````

### outbound set

````text
elysia outbound set --ranges <CIDR,...>   # 空列表 = 全放行
整体替换出站禁止段（受权限策略控制；高影响）

参数：
  --ranges                 禁止段 CIDR 列表（逗号分隔；空=放行所有）

示例：elysia outbound set --ranges 10.0.0.0/8,172.16.0.0/12

详细说明：整体替换出站禁止 IP 段列表（SSRF 防护）。--ranges 给出替换后的完整 CIDR 列表——是整体替换而非增量增删；空列表 = 放行所有地址。上游是本机/内网地址（如 127.0.0.1）被 "refused to dial denied IP" 拦截时，先核实目标地址、服务归属及用户授权，再说明拟变更的具体 CIDR、影响范围和风险；不要仅因请求被拦截就放宽策略。保留无关禁止段，按授权决定是否修改。
````

## 文档同步

中文内容由本测试生成；排版修改应落在生成模板，不直接编辑生成文件。英文完整译本人工维护，命令、参数、默认值、约束和示例必须与同次中文生成结果逐项同步。运行时帮助仍使用中文。

在 `backend` 目录重新生成中文，再检查一致性：

```sh
UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
```
