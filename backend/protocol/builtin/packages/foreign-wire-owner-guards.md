# 请求参数中的其他协议 owner 拒绝

四个未启用草案在 encode_request 的 effects 中声明 `foreign-wire-owner`。
策略只写在 JSON，使用现有 `keys`、`filter`、`trim_prefix` 和比较原语，没有新增 Go 协议分支。

| 草案 | 自己的参数 owner |
|---|---|
| Anthropic | `wire:claude` |
| Gemini | `wire:gemini` |
| Chat | `wire:openai_chat` |
| Responses | `wire:openai_responses` |

仅当 parameters 是对象时读取它的键。任何以精确 `wire:` 开头、又不等于本协议 owner 的键都
触发拒绝，不枚举来源协议。因此 `wire:custom` 等未预置家族同样受保护。
判定依据是键是否存在，值为 `{}`、`[]`、null、空字符串、0 或 false 都不能绕过。

自己的 owner 不被本守卫拒绝；其他既有校验仍可拒绝其非法形态。parameters 缺失时不枚举键，
普通非 wire 参数也没有被加入新白名单或顺带收紧。

## 验证与诊断边界

`TestPackageDraftForeignWireOwners` 使用同一个小矩阵直接编译四个真实草案：

- 48 个拒绝场景：任意自定义家族与另一个已知家族，分别使用六种 JSON 值形态。
- 12 个现有编码成功场景：没有 foreign owner、自己的空对象、普通 `temperature` 参数。
- 4 个 nil parameters 场景：此守卫不触发；目标仍可因缺少必需字段拒绝。

该矩阵、四份原迁移差分、Anthropic history owner 和真实来源的历史目标矩阵已通过定向验证。
没有更改共享静态差异账本，也没有修改组合验证使任意拒绝算作通过。

此处补齐的是拒绝行为，不是旧诊断逐字节迁移：

| 方面 | 旧内置 renderer | 新草案声明 |
|---|---|---|
| 错误码 | `unsupported_capability` | `conversion_rejected` |
| 阶段 | `wire` | `mapping.effects` |
| 路径 | 原 owner 及其首个嵌套字段的动态路径 | 静态 `/parameters` |
| 归因 | 内置字段保真检查 | `foreign-wire-owner`，`protocol-mapping` |

新守卫在 Mapping 前执行；旧检查位于 renderer 的扩展保存阶段。多处错误同时出现时，首错顺序
可能不同，本批未声称完整错误树或顺序等价。新诊断不读取或插入 owner 的正文值。

## 尚未覆盖

本批只保护 Request.Parameters 中的 owner。节点 Attributes、工具 Options、响应/事件扩展等
其他位置仍需各自声明，不应把根参数的通过外推到全部核心数据。

Responses 自己的非空 `wire:openai_responses` owner 的渲染缺口已在后续独立切片中处理，
详见 [Responses 草案说明](openai-responses-draft.md)。本守卫自身不负责渲染；自己的空对象
不被拒绝，不能证明该 owner 的任意扩展已经完整输出。其他同族深层冲突合并及
普通未映射参数的处理仍须独立验证。
