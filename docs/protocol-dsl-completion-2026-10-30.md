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

## 第五批：Agent 参数、目录声明与工具历史（dev.85）

四份 Mapping 继承原预置的全部 Agent 偏好样例与模型发现样例，Spec 独立声明目录端点、
分页参数及目录文档。Agent 参数程序由同一计划编译器执行，不再要求旧 Mapping/module。
工具结果格式在编译时保存；每次 BuildAgentRequest 不再解码整份协议定义。调用方读取
编译后的核心版本和 Agent 能力，离线验证读取 Mapping 的证据并报告正确的配置路径。

迁移对照实际发现三类历史编码缺口：Anthropic/Gemini 草案漏掉顶层工具结果；Responses
把消息内工具调用放入 content 而非独立 input/output 项。映射现按旧编码规则承接这些布局。
Responses 用通用 group_runs 保留相邻正文段及独立项顺序；原语只增加临时数组容器，
不含协议字段或供应商判断。大整数及工具结果原始 JSON 字符串保持不变，调用者历史不修改。

本阶段边界必须区分：Agent 参数与本地工具结果准备、模型目录是新旧直接对照；请求编码
对照的输入先经已有模型策略投影，验证的是相同投影后核心对象的渲染。模型能力／reasoning
投影尚未声明式迁移，不能据此称新 Agent 完整链路已可替代生产路径。首轮直接渲染还暴露
旧 Anthropic encoder 可输出缺预算的 enabled，而新 Spec 会拒绝；生产旧链先经模型投影，
该直接 API 接受边界仍登记为待闭合，不删除新 Spec 检查来掩盖差异。

四份草案真实流操作仍未配置；保留原 stream=true 样例，当前参数入口明确拒绝未声明的流
传输。完整离线验证不能通过，也未接入生产激活。原失败记录包括 `completion-agent-migration`
（测试目录名和格式比较问题）、`completion-agent-migration-comparison`（实际布局缺口）、
`completion-agent-history`（未投影思考设置及一次静态来源证明失败）；修复后对照为
`completion-agent-history-projected`。这些记录不作为完整流或模型策略迁移的验收。

随后 protocol/builtin 全量、Agent server 定向和全后端 vet 通过；Responses 请求／响应的
“连续正文→工具→正文→空消息”也与旧编码完整字节一致。日志为
`completion-agent-{protocol,server-history,vet}`，新增原语为 `completion-group-runs`。
尚未重测本阶段性能，也未更新六目标二进制；归档仍是修改前 dev.81。

## 第六批：双向会话声明（dev.86）

Spec 的 WebSocket session 声明客户端帧／服务端帧文档与现有传输参数，握手使用命名文档；
Mapping 接通 decode_client_event、encode_upstream_event 及其余既有方向。不添加供应商模块，
不改 WebSocket 读写循环、关闭顺序、队列和凭据限制。静态绑定要求完整 ingress 或 upstream
方向及正确文档，编译器仍需显式安装 transport.websocket 支持。

会话复用原 SessionReplay 的双向状态机，按响应分别管理工具关联、终态和晚到 usage；
它与单响应 HTTP 流契约分开，不能给会话套单响应结束条件。帧的静态类型约束来自 Spec，
会话的动态关联检查来自已转换核心事件，不新增第二个会话解码器或逐帧双执行。
源 WireStreamValidation 的独立单响应契约仍只适用于已声明的 HTTP 流；本批不宣称会话
获得了独立于 Mapping 的完整 wire 状态机。

原 session-alpha／session-beta 的握手和两条 16 步轨迹迁入两文件测试夹具，外部帧保持
原格式，核心事件及嵌套 Request 使用版本 2。Verify、组合验证和样例哈希接通 SessionSamples，
缺证据不能激活。SessionAdapter 的二进制媒体及失败事件使用绑定核心版本，不再固定版本 1。

新旧原轨迹、跨两包组合、原样帧字节、工具后续轮次、usage、显式关闭通过；复用原媒体与
模型授权回归，二进制字节、模型重写及非法改模型拒绝也通过。记录为
`completion-session-{compile,mapping,protocol,media}`。这里验证的是声明夹具；没有接入真实
Realtime 供应商、也没有宣称四预置真实 SSE 已完成。管理／激活加载仍在迁移范围内。

固定修改集的 Go 全量通过（约 104 秒），vet 及 Agent／会话／分组相关 race 通过。
staticcheck v0.8.2 仍为既有 45 条；归一化行号后新增 0、移除 0，未把退出 1 写为通过。
记录为 `completion-session-{go-all,vet,staticcheck}`、`completion-agent-session-race` 及
`completion-session-staticcheck-comparison.json`。没有改前端，未重跑六目标或性能矩阵。

## 第七批：Responses 派生正文与比较分配（dev.87）

实际对照发现草案会因缺少 status 而拒绝旧版接受的响应，且把 output_text 和
content_filters:null 都归入未知扩展。现由 Mapping 保留缺失 status，识别派生正文与已知
null 标记；非 null 的 content_filters 继续进入原 wire owner。Spec 要求 output_text 为
字符串、有显式 output 数组，并与按顺序提取的可见正文一致。普通消息字符串、分段正文、
空正文、工具穿插、大整数及无根 Native 的重建对照通过。

未闭合的诊断差异继续显式保留：旧 JSON 错误可能标为 decode_event/stream，新声明在
decode_response/wire；非法可见文本的新静态原因归入正文不一致，旧实现使用单独的文本
类型说明。错误码和业务位置的对照通过，多个字段同时非法时的首错顺序尚未统一。
这批没有迁移非空 tool_usage、access_programs、完整错误包络及响应输出侧的全部规则。

第一版用 equal(join(...))，256 KiB 派生正文约分配 2.01 MB/次，明显存在整段临时副本。
新增供应商无关的 equal_text 原语，按分段直接比较并在同步调用内借用字符串，降至约
0.87 MB/次；没有通过删除正文一致性检查取得该结果。普通 JSON equal 的既有行为不变。
Unicode／转义、空片段、后续非法值、取消和预算回归通过；protocol/builtin 全量、vet、
相关 race 通过，见 `completion-{output-text-*,text-comparison*,derived-text-protocol,text-vet,text-race}`。

### 必须保留的性能反向结果

本次补做 DecodeResponse 组件测试：同进程编译两版配置，固定 Go 1.27.2、GOMAXPROCS=4、
GOGC=100、GOMEMLIMIT=2GiB、PGO 关闭；每场景五对交替，短响应每批 5,000 次，大响应每批
40 次，预热后计时，不启用 profiler、不强制 GC。它不是独立客户端／网关／上游 HTTP 验收，
也没有测峰值。对旧生产 Responses 模块的回退另做一次五对确认，结果仍存在：

| 场景 | 旧模块中位耗时 | 新声明中位耗时 | 旧／新 B/次 | 结论 |
|---|---:|---:|---:|---|
| 128 字节正文 | 10.43 μs | 53.08 μs | 8,773／53,389 | 新声明约慢 5.09 倍，分配约 6.09 倍 |
| 256 KiB 正文 | 326.32 μs | 339.63 μs | 1,645,641／328,595 | 分配约少 80%；耗时配对变化 -4.1%～13.0%，不称稳定无回退 |
| 256 KiB 正文及 output_text | 618.22 μs | 815.27 μs | 3,494,790／876,737 | 分配约少 75%，耗时约增加 31.9% |

与 e9f7dea0 的旧草案相比，普通路径分配约增加 0.4%～0.6%；带 output_text 的草案过去
没有验证正文一致性，不能把新增校验耗时与旧草案直接解释为等价实现回退。与完整旧模块
的表格则确认了尚须解决的开销，不能把较低的大正文分配写成“DSL 全面无损”。

原始数据／配置身份与首轮失败实现留在
`.tmp-dev/protocol-dsl-completion-2026-10-30/derived-text-{benchmark,legacy-benchmark}`。
独立的 12 秒短响应诊断得到 17.42 CPU 秒、199,818 次解码：分配中 Sonic newLazyObject
约占 54.2%，readSonicObjectMember 累计约占 60.5%（两者不可相加）；CPU 还包含明显 GC
和计划递归开销。它是定位样本，不是正式三次 60 秒采样，未保留 go run 的临时可执行文件。
原始 profile 与 top 已保存；下一步首先减少指针读取产生的临时对象，再复核同一组件。

四预置仍未切换生产；真实流、模型策略、管理激活、旧格式退役和最终性能验收继续未完成。

## 第八批：有界复用子对象读取

针对上一批 profile，指针读取现在复用从当前根／迭代项实际访问到的子对象解析结果。
每个活动 owner 最多保存 8 项，按不可变存储身份匹配，不对正文计算哈希；容量用完后
直接沿用已有解析路径，不扩容、不全局缓存。相同内容的独立存储不能命中另一输入，
返回字段仍沿用小片段独立所有权。迭代完成立即释放；错误退出不覆盖错误引用的 Sonic
节点。重复／转义键的最后值语义、原生权限和预算均未调整，编译器继续 dev.87。

修改前 `60a73fca` 及修改后二进制独立构建，五对交替启动进程；每次采用前批相同负载，
取每进程五批的中位数再进行配对。原始结果、两个可执行文件哈希、Go 构建信息及源补丁
归档在 `.tmp-dev/protocol-dsl-completion-2026-10-30/reader-paired`。这是新 DSL 对自身的
组件比较，没有将旧模块的数字混入收益：

| 场景 | 耗时前／后 | 配对耗时变化中位数（范围） | 分配前／后 | 配对分配变化 |
|---|---:|---:|---:|---:|
| 短响应 | 53.71／44.35 μs | -17.43%（-18.17%～-15.18%） | 53,442／31,334 B | -41.37% |
| 256 KiB 正文 | 350.21／334.49 μs | -5.38%（-7.10%～+2.81%） | 325,974／304,231 B | -6.67% |
| 256 KiB 正文及派生文本 | 768.69／769.11 μs | -0.10%（-1.48%～+0.47%） | 874,119／853,449 B | -2.36% |

短响应改善稳定，大正文耗时改善不足以单独宣称。相对于旧生产模块，短响应仍约慢 4 倍，
带派生文本的大响应仍有回退；这次有界复用没有消除完整迁移的性能差距。不以分配下降
代替峰值／HTTP 验收。指针、来源权限、所有权、缓存容量与错误释放回归、protocol/builtin
全量已通过，记录为 `completion-reader-{contract,protocol,benchmark}`。

最终 Go 全量再次通过（约 92 秒）、vet 和相关 race 通过；固定 staticcheck 仍为既有
45 条，新增 0／移除 0。记录为 `completion-reader-{go-all,vet,race,staticcheck}`。
