# elysia CLI 参考（Agent bash 工具）

> 模型可见工具面：`bash`（执行 elysia 命令）+ `ask_user` + `update_plan`。本文档由命令表与 help 渲染器同源生成。

## 语法

支持单/双引号（`''` 表示空值）、`--flag value` 与 `--flag=value`、布尔 flag 单独出现即 true、列表 flag 逗号分隔或重复出现；批处理 `&&`（失败即跳过所在链的剩余命令）与 `;`/换行（继续）；尾管道 `| grep <子串>`（大小写不敏感）与 `| head <n>`（支持 `-n N` / `-N`）。help 输出同样支持管道。输出总预算 32KB（超限保头尾截断）。

## 总览

```text
elysia —— 网关运维 CLI（全部操作经 bash 工具执行）

命令组：
  source     模型源管理（create, delete, ls, refresh, update）
  model      单模型管理（ls, rm, set）
  group      模型组管理与成员维护（create, delete, ls, member add, member rm, update）
  key        API Key（推理访问令牌）管理（create, delete, ls, update）
  protocol   自定义协议设计（草稿/离线预览/真实测试/保存）（draft, models, preview, read, save, test）
  usage      用量统计与调用日志（log, logs, stats, trend）
  syslog     系统日志
  outbound   出站禁止 IP 段（SSRF 防护）（get, reset, set）
  session    会话操作（title）

分级帮助：elysia help <组>（flag 全表）/ elysia help <组> <命令>（完整语义与示例）。

语法：支持 '引号'（'' 表示空值）、--flag value 或 --flag=value、批处理（&& 失败即跳过所在链，; 或换行继续）、
尾管道（| grep <子串> 大小写不敏感过滤、| head <n> 截前 n 行，head 也支持 -n N / -N 写法）。

常用组合示例：
  elysia source ls && elysia model ls --source 主源 --limit 20
  elysia usage logs --days 1 --status failed | head 10
  elysia group create --name 主力 --models s1:gpt-4o

```

## source

```text
elysia source — 模型源管理

  elysia source create --name <名> --base-url <URL> [--platform openai|anthropic|gemini|responses|custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]
    创建模型源（需审批）
      --name                   源名称（显示用）
      --base-url               上游 baseUrl（http/https）
      --platform               openai（默认）/anthropic/gemini/responses/custom:<协议ID>
      --api-key                API key（加密存储）
      --auto-fetch             自动拉取模型列表
      --manual-models          手动模型名列表（逗号分隔）
      --fetch-base-url         模型列表拉取地址（缺省同 base-url）

  elysia source delete --source <id|名>
    删除模型源（不可逆，需审批；级联删模型与组引用）
      --source                 源 id 或名称

  elysia source ls
    列出全部模型源（密钥脱敏）

  elysia source refresh --source <id|名>
    从上游拉取模型列表（真实出站，需审批）
      --source                 源 id 或名称

  elysia source update --source <id|名> [--enabled] [--name <名>] [--base-url <URL>] [--platform <平台>] [--api-key <新key>] [--auto-fetch[=false]] [--manual-models a,b]
    修改模型源（需审批；api-key 留空=保留）
      --source                 源 id 或名称
      --enabled                启停
      --name                   改名
      --base-url               换 baseUrl
      --platform               换平台
      --api-key                新 API key（留空=保留原值）
      --auto-fetch             自动拉取开关
      --manual-models          整体替换手动模型列表

完整语义与示例：elysia help source <命令>。

```

## model

```text
elysia model — 单模型管理

  elysia model ls [--source <id|名>] [--search <子串>] [--limit <n>]
    查询模型清单（可按源过滤）
      --source                 源 id 或名称
      --search                 名称模糊匹配
      --limit                  返回条数（默认 50，上限 200）

  elysia model rm --source <id|名> --model <模型id>
    删除单个模型（不可逆，需审批）
      --source                 源 id 或名称
      --model                  模型 id

  elysia model set --source <id|名> --model <模型id> [--name <名>] [--type <类型>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <模式>] [--enabled[=false]]
    修改单个模型（需审批）
      --source                 源 id 或名称
      --model                  模型 id
      --name                   改名
      --type                   类型
      --max-tokens             maxTokens
      --vision                 视觉能力标记
      --tools                  工具能力标记
      --structured             结构化输出标记
      --thinking               思考模式
      --enabled                启停

完整语义与示例：elysia help model <命令>。

```

## group

```text
elysia group — 模型组管理与成员维护

  elysia group create --name <组名> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
    创建模型组（需审批）
      --name                   组名（客户端调用时用的模型名）
      --models                 成员模型引用（sourceId:modelId 或模型名，逗号分隔）
      --strategy               round-robin（默认）/random/sequential
      --max-retries            失败重试次数（默认 3）
      --enabled                默认 true
      --max-concurrency        并发上限（0=不限）
      --daily-limit-requests   每日请求上限（0=不限）
      --daily-limit-tokens     每日 token 上限（0=不限）

  elysia group delete --group <组名|id>
    删除模型组（不可逆，需审批；可能级联禁用 Key）
      --group                  组名或 id

  elysia group ls
    列出全部模型组及成员

  elysia group member add --group <组名|id> --models <列表>
    向模型组追加成员（需审批）
      --group                  组名或 id
      --models                 成员引用（sourceId:modelId 或模型名）

  elysia group member rm --group <组名|id> --models <列表>
    从模型组移除成员（需审批）
      --group                  组名或 id
      --models                 成员引用

  elysia group update --group <组名|id> [--add-models <列表>] [--remove-models <列表>] [--enabled[=false]] [--strategy <策略>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
    修改模型组（需审批；成员增删/策略/限额）
      --group                  组名或 id
      --add-models             追加成员
      --remove-models          移除成员
      --enabled                启停
      --strategy               调度策略
      --max-retries            重试次数
      --max-concurrency        并发上限
      --daily-limit-requests   每日请求上限
      --daily-limit-tokens     每日 token 上限

完整语义与示例：elysia help group <命令>。

```

## key

```text
elysia key — API Key（推理访问令牌）管理

  elysia key create --name <名> [--secret <明文>] [--allowed-groups <组,...>] [--enabled[=false]]
    创建推理 API Key（需审批；secret 留空自动生成，明文仅返回一次）
      --name                   Key 名称（主键，创建后不可改）
      --secret                 Key 明文；留空自动生成随机值
      --allowed-groups         允许访问的模型组（逗号分隔；空=不限制）
      --enabled                默认 true

  elysia key delete --name <名>
    删除 API Key（不可逆，需审批；远程访问 Key 拒绝）
      --name                   Key 名称

  elysia key ls
    查询 API Key 列表（脱敏）

  elysia key update --name <名> [--enabled[=false]] [--allowed-groups <组,...>] [--new-secret <新明文>]
    修改 API Key（需审批；new-secret 留空=保留；远程访问 Key 拒绝）
      --name                   Key 名称
      --enabled                启停
      --allowed-groups         整体替换允许访问的模型组
      --new-secret             新明文；留空保留原值

完整语义与示例：elysia help key <命令>。

```

## protocol

```text
elysia protocol — 自定义协议设计（草稿/离线预览/真实测试/保存）

  elysia protocol draft '<完整配置 JSON>' [--example '<响应示例 JSON>']
    写入/更新协议配置草稿（立即校验并离线验证）
      --example                上游响应示例 JSON，用于离线检验 response 映射
      <config>                 完整 CustomProtocolConfig JSON（建议用单引号包裹）

  elysia protocol models [--base-url <URL>] [--api-key <key>]
    按草稿 models 配置试拉上游模型列表（需审批）
      --base-url               上游 baseUrl（缺省用会话已记住的）
      --api-key                API key（缺省用会话已记住的）

  elysia protocol preview [--sample '<样例 Maheshvara 请求 JSON>']
    离线渲染草稿请求（不发送）
      --sample                 自定义样例请求 JSON

  elysia protocol read --id <协议id>
    读取已保存协议的完整配置
      --id                     协议 id（含内置预置协议）

  elysia protocol save
    把当前草稿保存为正式协议（需审批）

  elysia protocol test [--base-url <URL>] [--api-key <key>] [--stream] [--sample '<样例请求 JSON>']
    向真实上游发送一次测试请求（需审批）
      --base-url               用户提供的上游 baseUrl（缺省用会话已记住的）
      --api-key                用户提供的 API key（缺省用会话已记住的）
      --stream                 按流式（SSE）测试
      --sample                 自定义样例请求 JSON

完整语义与示例：elysia help protocol <命令>。

```

## usage

```text
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

```

## syslog

```text
elysia syslog — 系统日志

  elysia syslog [--level info|warn|error] [--limit <n>]
    查询系统日志
      --level                  info | warn | error（缺省全部）
      --limit                  返回条数（默认 30，最大 100）

完整语义与示例：elysia help syslog <命令>。

```

## outbound

```text
elysia outbound — 出站禁止 IP 段（SSRF 防护）

  elysia outbound get
    查看出站禁止 IP 段（SSRF 防护）

  elysia outbound reset
    恢复出站禁止段为预置默认（需审批）

  elysia outbound set --ranges <CIDR,...>   # 空列表 = 全放行
    整体替换出站禁止段（需审批；高影响）
      --ranges                 禁止段 CIDR 列表（逗号分隔；空=放行所有）

完整语义与示例：elysia help outbound <命令>。

```

## session

```text
elysia session — 会话操作

  elysia session title <文本>
    把会话标题改成任务概括（≤16 字动宾短语）
      <title>                  新标题

完整语义与示例：elysia help session <命令>。

```
