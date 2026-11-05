# DSL 能力状态（dev.148）

本表替代 dev.100／101 的过期迁移状态；历史实施见[剩余闭合记录](protocol-three-part-closure-2026-11-02.md)，最新验收见[后端优化与默认切换门槛](backend-optimization-2026-11-05.md)。本轮从 `00fba372` 开始，以该提交的默认链 D0 和三分链 P0 为基线。四包及 Spec/Mapping 独立编辑已可运行，但默认生产加载器仍为旧定义，整轮重构尚未完成。

| 能力 | 外部合同与核心表示 | Mapping 和真实入口证据 | 剩余缺口 |
|---|---|---|---|
| 四协议 JSON／SSE 文本 | 四 Spec 的请求、响应、事件及生命周期；Message／Text | 四份 directions；网关 16 方向共 32 场景、正式独立验证 | 复杂能力组合不由文本矩阵代表 |
| usage 与客户端展示 | Usage、ClientOutput，完整计量与展示独立 | dev.107 混合流；真实网关 usage 偏好 | 全状态资源计量及正式性能 |
| API 存储意图 | Spec store 类型；ClientOutput.ResponsesStorage | dev.108 responses-storage／190、Agent 及最终出站 finalCheck | 不代表供应商实际持久化行为 |
| 签名／加密续传 | 来源资源及 Scope；宿主认证证明 | dev.109 continuation、恢复规则、实际两轮与载体交付 | 多段 reasoning 与一般布局 |
| 工具选项 | Spec strict／streaming；Tool.Options | dev.110 tool-options／145；Agent 实际出站 | 不扩大不支持的工具 API |
| 工具结果 | Spec 内容块／对象；ToolResult payload／children／status，具名 result_envelope | dev.111 tool-results／150；dev.122 混合包络与扩展重建，四目标真实 JSON／SSE、Agent 两轮 | 一般混合布局及复杂媒体组合 |
| 历史与多根输出 | 消息父容器、关联、reasoning 集合和 UTF-16 文本位置 | dev.121—125 JSON 布局；dev.126 流式区间；dev.127 Chat event-layout／310；四目标代表性复杂 SSE 实际调用，含多 message、工具、引用、logprobs、双 thinking 签名及 Gemini 有序 parts；根 Native 删除重建 | 最终 wire 文本改写证明及完整原生／重建／交付失败组合；代表性组合不等于任意组合均支持 |
| 缓存 | CacheIntent 与资源 Scope；cache_intents 通用执行 | dev.118 四份目标显式处置；dev.120 默认缓存对象聚合证明、Native 编辑与重建 | 一般布局与复杂缓存组合；不扩大未声明策略 |
| metadata／错误 | Spec 外部类型；核心通用载体与 logprobs 约束 | dev.114 处置前类型检查；dev.117 output_text；dev.119 受限错误选择、已知细节目标处置和真实 HTTP 失败 | 未声明错误扩展继续拒绝；缓存／布局中的附属字段归属 |
| 早期错误与入口快照 | 端点注册选择协议；宿主通用类别和 HTTP 状态；Spec 最终错误合同 | dev.131 鉴权前固定 ingress；四包 public／gateway 鉴权、未知模型、资源拒绝；自定义字段切换与在途快照、HTTP 524 网关及 Agent | 模型目录成功输出、任务／会话等全部入口仍需逐项闭合；配置不可用或编码失败使用固定网关错误格式 |
| 请求外部字段与目标规则 | Spec safety／上下文／追踪合同；参数及 reasoning 叶子 | dev.112 显式默认规则；dev.113／115 通用有限值与字段投影；dev.116 外部合同；dev.120 按核心声明类型编译对象聚合 | 动态数组成员聚合及一般布局仍有能力边界 |
| 管理／CLI／模型／任务／会话 | 整包编译、修订、快照与已有 task/session 合同 | 两源编辑器、CLI、Agent 与模型发现已有接线 | 所有入口统一、资源所有权及最终页面验收 |
| 客户端模型目录 | Spec models.clientDocument；通用 ModelPage | dev.134 encodeModels、分方向 modelSamples 与 models 预览；Chat／Gemini public 和 gateway、自定义字段改名及在途快照真实调用 | 旧生产目录 handler 随默认切换删除；Responses／Anthropic 未增加客户端目录能力 |
| 上游输入计数 | 独立 count_tokens request/response；TokenCount | Anthropic/Gemini countTokens、四方向样例、四组合文本/工具/图片转换；真实 HTTP 权限、密钥、wire 检查、取消释放和重试快照回归；两包计数浏览器预览通过 | 这些用例不代表任意复杂能力组合；生产默认仍未切换 |
| 生产和性能 | 包 4、DSL 2、核心 2、策略 2 | 本轮 Go 全量、相关 race、vet、staticcheck、生成与源码清单检查；46 个浏览器用例、六目标编译及 Windows amd64 发行启动通过 | 最终三分链仍未通过 D0 门槛，不能切换默认或删除旧链；数组优化保留，其他三个候选拒绝；六目标编译不代表六平台运行验收 |
| 采集入口身份 | 源码提交、编译器、四包修订及策略哈希 | 冻结 D0/P0 和最终源码、二进制、客户端、模拟上游；当前默认与显式包入口分别测量，历史版本仅供参考 | production 默认检查仍会拒绝旧 codec；完整测量、内存不确定性和拒绝实验见本轮报告 |
| 最终 JSON 引用证明 | Spec 外部形状；未改写 Unicode 区间 | dev.128 Chat／Responses deliveryRanges；ValidateResponseOutput、真实 Chat 网关、组合验证及 wire 预览；自定义字段合同组件通过 | 语义事件修改事实、静态修改路径分析仍待接线；当前不能声明所有文本改写已安全闭合 |
| 最终 SSE 引用证明 | Spec 事件及生命周期；摘要、未改写 Unicode 区间和成功交付事实 | dev.130 deliveryRanges／ConsumeTransformed；dev.133 Chat／Responses semanticRanges、宿主原始事件投影；真实跨协议 SSE、emoji 与晚到引用，组件验证修改后缀索引 | 完整响应语义布局、Agent 核心收集、静态影响路径分析和完整 Native 接续组合；性能仍待正式测量 |
| 失败交付关闭 | 宿主 Write／Flush 事实；独立 wire 位置和认证续传 | dev.129 AbortDelivery；dev.130 同批多帧第二次写失败、Flush 失败、Flush 后取消；已提交位置和持久化事实保留，不重发部分批次 | 完整 Native→重建→Native 组合仍需验收 |
| Native 与语义编码接续 | 原帧摘要、编码历史和最终外部生命周期分别检查 | dev.132 四协议实际正文改写后继续原生增量；Responses 工具参数及快照改写后继续原帧，完成事件不重复；完整消费、usage 和单次结算；Mapping 使用 encoding.hasRendered 重建含子项正文的完成帧 | 其余协议工具改写与慢交付等组合；语义引用证明不能用编码历史布尔值代替；正式性能待测 |

## Go 残留分类

- 新核心版本不再调用共享 Go 的 Gemini／Responses 外部类型校验或普通 metadata 白名单；默认 Spec／Mapping 接管它们。核心 logprobs、工具、reasoning 结构、关联和资源检查保留。
- 旧核心版本仍使用外部类型校验和 metadata 白名单；旧 codec 的 Responses output_text 专用检查暂留。新包的来源、目标投影、重建和最终输出均由声明检查 output_text，不调用旧拼接检查。
- 仅旧 codec 自动策略：DefaultConversionPolicy 中依赖 Codec 的供应商动作；新包 Codec 为空，不能误称它们全部被新包调用。待旧链退役时删除。
- 新网关错误出口不再通过 URL 后缀猜供应商或调用旧 renderer；非法上游错误体不再嗅探 Cloudflare 专用字段。旧 renderer 及模块源码尚待生产退役批次按调用关系删除。
- 通用宿主职责：权限、Scope、Seal/Open、I/O、计量、取消和实际交付确认继续保留，不能因供应商配置化而删除。新包客户端目录经 encodeModels 和 clientDocument 输出；server/http.go 的旧格式目录构造只服务尚未切换的旧核心，不能作为新包失败兜底。上游发现和客户端输出各有独立证据。

无调用者的函数按实际引用逐项确认后删除，不以文件名或供应商字符串搜索作为唯一依据。本轮 staticcheck v0.8.2 为零诊断；仍在默认链使用的旧 codec、模块与 renderer 继续保留。

流式布局授权由目标 Mapping 调用通用原语，宿主绑定已解析策略；网关、组合验证和映射流预览接入相同事务边界。Agent 复用网关转换快照的候选因最终默认链内存门槛失败已撤回，相关测试和证据保留。其收集路径固定策略上下文并直接保留核心内容，不为了授权布局重新编码或伪装供应商来源。默认切换与旧链退役受 D0 性能门槛阻止，不能把局部优化或配置编辑入口完成视为整轮重构完成。
