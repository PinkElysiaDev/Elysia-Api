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
