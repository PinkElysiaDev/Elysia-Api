# DSL 完整迁移实施记录

实施分支为 `refactor/protocol-dsl`，本批基线为 `476688e7 / dev.81`。
目标是承接现有转换能力，再移除旧生产执行体系；不能用四份草案编译通过代替完成。

## 本批已确认的决定

- 每协议维护 `spec.json` 和 `mapping.json` 两份源文件；传输、存储和激活使用一个整体包。
  Spec 描述外部 API，Mapping 描述与大自在天的映射和语义处置，DSL 原语不认识供应商。
- 最终不兼容旧协议配置。保留旧数据、修订与绑定，明确标为不可执行；不自动翻译、重绑或重投任务。
  升级前排空旧任务，遗留任务和续传不能通过替换版本或删除来源冒充兼容。
- 保留成功／拒绝、正文和帧、工具关联、usage、终态、原生回放和隔离结果。
  允许改进内部表示、来源路径与错误包装，逐项说明变化；对外错误码、业务位置和归因仍保留。
- 保真差分只在开发验收执行；静态关系在编译期检查。运行期复用已有解析及必要动态检查，
  不添加新旧双执行、全文结果比较或失败后换执行器重跑。
- 优先完成功能迁移并消除重复序列化、重复扫描与无界保留。性能反向结果如实报告；
  异常最多追加一次配对确认，不因微小波动无限重测，也不把有回退的结果称为性能无损。
- 复用现有测试，只补实际缺口和新通用机制的必要反例。仅本地提交，不推送、不调用真实供应商。

这些决定取代旧迁移记录中要求新旧内部 Source/Native 表示及完整错误包装逐字相同的门槛，
但不放宽外部行为、字段保真或来源权限。尚未解释的差异仍属于未完成项。

## 修改前的可复核基线

标准构建在修改前完成 WebUI 和 Windows/Linux/macOS 的 amd64、arm64 六目标，
全部二进制构建信息为 `476688e79696bcf59d5f1e6033fc4093649a944f`、`vcs.modified=false`。
源码 ZIP、六目标二进制、SHA256 和 Go 构建信息保存在
`.tmp-dev/protocol-dsl-completion-2026-10-30/baseline/identity.json`。
构建日志为 `.tmp-dev/protocol-dsl-2026-10-29/checks/baseline-476688e7-six-targets.*`。
其他平台仅交叉编译，不是运行验收。

当前版与正式 v1.7.0 的直接 HTTP 对照位于
`.tmp-dev/release-1.7.0-http-2026-10-30-476688e7/REPORT.md`。
80 批、82,720 请求完成；真实修改大正文的分配明显下降，普通场景仍有 CPU、延迟与峰值波动。
四份草案未启用，不能把默认 HTTP 收益归因于草案已经达到替换门槛。

## 实施顺序

1. 两文件源编译与四份配置拆分：整体身份、单次编译和组合预算。
2. 四协议请求／响应：首先闭合 Chat usage 指令和 Gemini 同族嵌套扩展合并，再逐语义组迁移。
3. 真实流：源／输出契约、身份继承、分层快照、工具、metadata、usage 与多动作帧。
4. 模型发现、任务、会话、WebSocket、Agent 和续传，复用现有传输与调度。
5. 两文件编辑、验证、预览、原子激活、重启和新格式回滚；旧格式明确不可执行。
6. 删除旧模块，完成工程、功能和性能验收并登记未完成的真实客户端验证。

各阶段以实际完成的代码、测试和数据更新记录；当前生产预置尚未切换。

## 第一批：两文件编译与两个实际缺口

`0676a8d4` 以声明修复 Chat 主动收集 usage 的指令遗漏及 Gemini generationConfig 自有扩展
的浅合并误拒绝。旧候选失败证据保留；修复后 11 项成功输出完整字节相同、4 项真实碰撞仍拒绝，
builtin 全量通过。碰撞的对外错误诊断差异仍待后续迁移，不将其写为完整等价。
证据为 `checks/completion-request-gaps-{before,after,builtin}.*`（位于原 protocol-dsl 日期目录）。

四份草案已拆为各协议目录内的 `spec.json`／`mapping.json`，保留原始 JSON 数字和字符串 token。
新增 `CompileSources` 与整包入口共用一次编译，身份／样例证据一致；源文件及组合后字节、
节点和深度均受既有预算约束。预算扫描不拼接额外整包，亦不通过重新编码缩小输入后放行。
原迁移测试改为直接读两份源文件，不保留重复合包资产或测试编译框架。

`completion-sources` 定向以及 `completion-source-pairs` 的 protocol/builtin 全量通过。
本批不改变请求执行算法，未重新测量性能。Spec 的命名文档与纯外部操作结构、完整流及
管理激活仍未完成，当前生产加载器仍读取旧预置。

## 第二批：独立外部文档与局部冲突诊断（dev.82）

Spec 改为 `documents` 和独立的 `ProtocolOperationSpec`，不再引用核心 Direction 或旧 Mapping。
Mapping 每个方向显式引用 `document`，每个操作显式绑定请求／响应方向。既有四份配置和
合成样例逐文档迁移，保留 Anthropic／Gemini 输入与输出约束的差异，不机械合并。
同一文档编译一次，运行态仍按方向取不可变契约；未被本 Mapping 使用的文档仍验证合法性。
旧草案 `wireContracts`、根 `stream` 和嵌套旧程序明确拒绝，不保留兼容格式。

`merge_deep.onConflict` 可对本次合并的叶子冲突声明对外错误分类和业务位置。
Gemini 同族扩展采用此声明后，四个冲突的外层 ConversionIssue 与旧模块完全相等，
内部错误保留结构化冲突 Path 和 Unwrap，不复刻旧包装层。其他拒绝诊断尚未因此闭合。
成功路径不加正文扫描；元信息只在编译产物持有，失败时创建包装。

protocol/builtin 全量及相关 server 流与健康探测定向通过，日志为
`checks/completion-documents-protocol-fixed.*`、`completion-documents-server-fixed.*`。
初次检查的两个测试问题保留：空 items 比 null onConflict 更早被拒绝；健康探测夹具仍传入了
Spec 操作而非编译后的 Operation。修正测试输入／绑定后通过，没有放宽运行校验。
合并诊断首次还发现 suggestion 未保存，补为声明字段后外层 Issue 差分通过，日志为
`checks/completion-generation-diagnostic*`。本批尚未启用供应商流或管理加载。

随后固定修改集的 Go 全量通过（约 98 秒）、vet 通过，日志为
`checks/completion-documents-go-all.*` 和 `completion-documents-vet.*`。
此阶段未改前端，未重跑性能或六目标；修改前归档仍对应 dev.81。

## 第三批：模型发现、声明诊断与响应字段（dev.83）

模型发现已能由两份源文件编译。Spec 只含 GET 端点、目录文档和分页参数；Mapping 的
`operations.*.decodeModels` 描述目录转换，`modelSamples` 纳入原验证及样例哈希。
复用既有 ModelPage、分页／凭据约束和完整输入预算，不将模型目录伪装成核心 Response。
models-only 包不取得生成能力证据；未声明模型发现的包不分配模型运行状态。
管理加载及四预置的 models 操作仍待接通，不能将内部编译测试称为产品闭环完成。

两源编译移除每源预算扫描后再合包重扫的重复工作：各源只检查字节长度和 JSON 有效性，
组合预算扫描一次，再分别严格解码。身份及超限测试保持原期望。

Responses 普通根 metadata 的 string/bool/number/object/array 类型字段现已映射到独立核心元数据，
未知根字段落在自身 wire owner。原生往返、修改 model 和无根 Native 重建比较通过；
新增通用 entries/from_entries 用于动态成员构造，未增加供应商 Go 逻辑。
最终 root renderer 保留原 identity/metadata 冲突检查和后续包络字段覆盖顺序。
静态 emit 证明补充 last merge 的最后成员写入者，不给整个合并对象作完整复制承诺。

尚未完成：Responses output_text 一致性、tool_usage、access_programs 等特殊元数据，
usage 内未知扩展、foreign response owner 的完整拒绝和动态元数据错误位置。
三个非法普通元数据反例还复现旧 JSON decoder 将错误标成 decode_event/stream；新声明为
decode_response/wire，错误码、业务字段和原因相同。该对外归因差异明确登记，未据此宣称完整等价。

`completion-models-metadata-protocol` 的 protocol/builtin 全量通过。
定向与原失败证据包括 `completion-object-entries`、`completion-response-extensions-*`、
`completion-response-metadata*`。一条组合正则未选中 builtin 测试，随后已用实际测试路径重跑，
不将该空运行作为验收。Python 编辑脚本语法错误造成的未修改失败也保留。
此阶段未重测性能，不把新声明的功能完整性成本称为零开销。

## 第四批：声明式任务流程（dev.84）

Spec 的 task 只描述 status/result/cancel 操作及幂等 header；Mapping 的 task 含 decode、
encode、control 三个表达式。复用原 JobUpdate／TaskControl、身份、状态、usage 与凭据冲突
检查；provider reply 和 client receipt 分别使用其命名文档。动态 query 仍由原传输合并，
程序不能选择 URL、凭据或替换持久任务修订。

旧两套 job-alpha/job-beta 样例作为对照，新增两文件声明继承相同 wire 和静态任务样例。
所有原任务状态／回执／控制样例输出相同，新包和两包组合验证通过；缺证据不能激活。
这不等于旧数据库任务可由新引擎恢复，管理切换及存量任务处理仍待实施。

模型发现和任务共用一个窄的辅助操作计划入口，不套核心 Response/Native 逻辑。
有输入／输出 wire 契约时复用其完整预算检查，移除模型发现先计量后由同一契约再计量的重复。
无 wire 契约的核心控制输入仍执行原完整预算。正常运行不增加请求配置读取或数据库查询。
`completion-task-mapping` 的任务／模型定向通过，初次全协议发现的旧“不支持任务”测试改为
检查“缺少任务程序仍拒绝”，保留原失败记录 `completion-task-compile`。

本批 protocol/builtin 全量、全后端 vet 和任务／模型／新原语相关 race 通过。
固定 staticcheck v0.8.2 仍以既有 45 条诊断退出 1，归一化行号后新增 0、移除 0；
证据为 `completion-task-{protocol,race,vet}.*`、`completion-stage-staticcheck*`。

## Chat cache miss 计量声明

迁移检查发现 Chat 草案未读取 `prompt_cache_miss_tokens`：合法响应会丢失由命中与未命中
计数推导的输入、总量和未缓存明细，也会接受旧模块拒绝的矛盾计数。修复仅进入 Chat
`mapping.json`，以命名表达式复用命中选择、输入推导及计数约束，不新增 Go 厂商分支。
显式观测计数不改成推导计数；缓存创建量从未命中量扣除；缺失计数不默认补零。

复用现有 Chat 迁移测试，5 项成功场景比较完整核心对象和移除根 Native 后的编码字节，
11 项拒绝核对旧版的错误码、阶段、路径、原因及方向。声明拒绝使用通用 diagnostic
分类保留 `upstream_contract_violation/runtime`，仅增加规则证据及内部错误包装。
只有 miss 而缺少总输入的同步响应仍由现有核心算术检查拒绝；流式晚到 usage 合并
没有在本批改写。证据位于 `checks/completion-chat-cache-{before-isolated-fixed,diagnostics}.*`；
首次修改前记录为 `completion-chat-cache-before.*`。过程中一次测试助手调用不存在的
Response.Clone 导致编译失败、一次修订脚本路径错误未写入文件，均保留原始失败记录。

另独立发现：空 `choices` 解码后移除根 Native，旧 Chat 编码器合成一个空 assistant
choice，草案输出空数组。本批不处理这一消息布局缺口；usage 编码比较显式使用两边
相同的普通 assistant 节点，核心解码比较仍保留原始空 choices。后续消息布局迁移必须
单独承接，不能把本批 usage 通过解释为响应转换全部等价。

最终 builtin 全量通过，记录为 `checks/completion-chat-cache-builtin-final.*`。
本批只补声明中的已有计量语义，尚未重测 DSL 性能。
