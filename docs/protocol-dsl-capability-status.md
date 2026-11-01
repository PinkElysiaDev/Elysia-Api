# DSL 能力状态（dev.118）

本表替代 dev.100／101 的过期迁移状态；旧调查可从 Git 历史读取。最新实施记录见[剩余闭合记录](protocol-three-part-closure-2026-11-02.md)。本轮从 a20f63ce 继续；四包已可独立编译和执行，但默认生产加载器仍为旧定义。

| 能力 | 外部合同与核心表示 | Mapping 和真实入口证据 | 剩余缺口 |
|---|---|---|---|
| 四协议 JSON／SSE 文本 | 四 Spec 的请求、响应、事件及生命周期；Message／Text | 四份 directions；网关 16 方向共 32 场景、正式独立验证 | 复杂能力组合不由文本矩阵代表 |
| usage 与客户端展示 | Usage、ClientOutput，完整计量与展示独立 | dev.107 混合流；真实网关 usage 偏好 | 全状态资源计量及正式性能 |
| API 存储意图 | Spec store 类型；ClientOutput.ResponsesStorage | dev.108 responses-storage／190、Agent 及最终出站 finalCheck | 不代表供应商实际持久化行为 |
| 签名／加密续传 | 来源资源及 Scope；宿主认证证明 | dev.109 continuation、恢复规则、实际两轮与载体交付 | 多段 reasoning 与一般布局 |
| 工具选项 | Spec strict／streaming；Tool.Options | dev.110 tool-options／145；Agent 实际出站 | 不扩大不支持的工具 API |
| 工具结果 | Spec 内容块／对象；ToolResult payload／children／status | dev.111 tool-results／150；真实三目标 JSON／SSE、Agent 两轮 | Gemini 混合包络、一般混合布局及复杂媒体组合 |
| 历史与多根输出 | 已有消息父容器、关联及 reasoning 集合 | 配置已有局部分组与身份处置 | system/developer 上提、一般分组、多根／多段 reasoning |
| 缓存 | CacheIntent 与资源 Scope；cache_intents 通用执行 | dev.118 四份目标显式处置；Native／重建、禁用及真实 JSON／SSE 请求 | 聚合证明、一般布局与复杂缓存组合；不扩大未声明策略 |
| metadata／错误 | Spec 外部类型；核心通用载体与 logprobs 约束 | dev.114 处置前类型检查；dev.117 output_text 当前正文一致性、真实网关回归 | 一般错误处置、缓存／布局中的附属字段归属 |
| 请求外部字段与目标规则 | Spec safety／上下文／追踪合同；参数及 reasoning 叶子 | dev.112 显式默认规则；dev.113／115 通用有限值与字段投影；dev.116 来源、重建和最终出站 | 对象 aggregate 仍限定单个 cache owner；缓存／布局待闭合 |
| 管理／CLI／模型／任务／会话 | 整包编译、修订、快照与已有 task/session 合同 | 两源编辑器、CLI、Agent 与模型发现已有接线 | 所有入口统一、资源所有权及最终页面验收 |
| 生产和性能 | 包 4、DSL 2、核心 2、策略 2 | 独立包测试、Go 全量／相关 race／vet 已通过 | 默认加载切换、旧链删除、A/B/C 正式性能与最终构建 |

## Go 残留分类

- 新核心版本不再调用共享 Go 的 Gemini／Responses 外部类型校验或普通 metadata 白名单；默认 Spec／Mapping 接管它们。核心 logprobs、工具、reasoning 结构、关联和资源检查保留。
- 旧核心版本仍使用外部类型校验和 metadata 白名单；旧 codec 的 Responses output_text 专用检查暂留。新包的来源、目标投影、重建和最终输出均由声明检查 output_text，不调用旧拼接检查。
- 仅旧 codec 自动策略：DefaultConversionPolicy 中依赖 Codec 的供应商动作；新包 Codec 为空，不能误称它们全部被新包调用。待旧链退役时删除。
- 通用宿主职责：权限、Scope、Seal/Open、I/O、计量、取消和实际交付确认继续保留，不能因供应商配置化而删除。

无调用者的函数按实际引用逐项确认后删除，不以文件名或供应商字符串搜索作为唯一依据。staticcheck v0.8.2 当前有 24 条存量诊断，未宣称静态检查全净。
