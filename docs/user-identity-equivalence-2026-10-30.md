# 用户标识JSON表示等价修复（dev.80）

用户明确批准将这项接受边界变化独立于DSL行为等价迁移实施。旧Chat／Responses直接编码
使用Value的JSON字节相等判断`parameters.user`和`anthropic_metadata.user_id`，导致同一字符串
的不同转义写法冲突；既有默认转换投影按字符串内容比较，已经接受这些输入。

本次只修复两者同时存在时的字符串比较。两个Value字节相同仍直接通过；字节不同且两者
均为字符串时比较解码后的字符串。不同标识、null与字符串、其它非字符串表示继续沿用原边界。
不把不同数字拼写等扩展为普遍的JSON语义相等，也不修正其它字段的校验或默认值。

## 接受边界

以下已在`8a6f84b6 / dev.79`实际采集，两目标行为一致，共30次入口观察。
其间至实施前`44d57240`，对应生产参数编码代码没有改动。

| user／user_id | 旧直接Encode与raw Execute | 既有默认投影后Encode | 修复后直接入口实测 |
|---|---|---|---|
| `"u"`／`"u"` | 接受 | 接受 | 保持接受 |
| `"u"`／`"\u0075"` | 拒绝 | 接受 | 改为接受 |
| `"<"`／`"\u003c"` | 拒绝 | 接受 | 改为接受 |
| `null`／`null` | 接受 | 接受 | 保持接受 |
| `null`／`"u"` | 拒绝 | 拒绝 | 保持拒绝 |

旧直接冲突诊断为`unsupported_capability / wire / /parameters/user`；默认投影冲突为
`invalid_input / /parameters/anthropic_metadata/user_id`。保留两入口既有错误路径及顺序，
没有为统一展示新增全局错误包装。原采集中的默认投影还会增加`store:false`，该既有行为不变。

原始输入均为合成标识。证据在
`.tmp-dev/protocol-dsl-2026-10-29/user-identity-representation-2026-10-30/`，包含`results.json`、
完整错误树、探针源码、命令、环境和匹配二进制；该二进制SHA256为
`26c2ce4b9654ffcf6ad91a68c47c24309d6b41befa30373b8f6f81926cf5054d`。

## 版本和成本边界

编译器升级为`2.0.0-dev.80`，使既有离线证据依原机制重验。没有改写用户定义、草稿、绑定、
协议配置、公开API或数据库结构。生产预置仍使用旧模块；本修复不表示v3草案已迁移完成。

新增工作只发生在两个用户标识均存在、JSON字节不同且均为字符串的分支；不遍历请求历史、
媒体或工具正文，不新增缓存、锁或goroutine。解码成本随这两个标识长度增长，不能称完全零成本。
正常单一标识和字节相同的双标识保留快速比较。尚无本切片的性能实测，不承诺耗时改善。

## 修复后验证

原30次探针重放后，恰好8个入口（两个目标×两种转义别名×public/raw）由拒绝改为成功；
其余22项输出、诊断sink和完整错误树逐项相同，无需排除编译身份字段。after二进制SHA256为
`bc2f918746847bda903762b5805073d9f5025929ca34303ae6fad6bb24946abd`。
前后命令、编译身份、源码文件哈希和比较清单保存在
`.tmp-dev/protocol-dsl-2026-10-29/user-identity-equivalence-dev80-2026-10-30/`。
临时探针已删除，生产构建没有双执行比较。

扩展既有tracking identity测试，覆盖两个目标的公开编码、raw Execute和默认投影。
除表中形态外，还确认不同字符串、null与空字符串继续冲突；原raw调用的数字`1`／`1`仍接受，
`1`／`1.0`仍拒绝，公开入口与投影的非字符串校验仍先于这一比较。

protocol/builtin全量、对应vet和身份／hint定向race通过，日志分别为
`checks/identity-dev80-protocol.*`、`identity-dev80-vet.*`、`identity-dev80-race.*`。
本切片未单独执行完整HTTP性能或前端页面。后续a4a8f275阶段已完成dev.80六目标构建，
匹配产物与源码归档见 [阶段验收记录](protocol-dsl-validation-2026-10-30.md)；原dev.79产物保留。
未调用真实供应商；原客户端会话仍需另行复测。

## 两个null的独立兼容修复（dev.81）

2026-10-30用户批准继续修正，基线为aef4351d/dev.80。共享身份比较器现在也接受两个JSON
null的不同外围空白表示。不同身份、null／字符串，以及raw入口的数字拼写等原拒绝规则保持。
原字节相等和字符串比较路径不变；仅在原先拒绝非字符串的分支判断两者是否均为null。
编译器升级至dev.81，沿用既有离线证据重验机制。

这是旧比较规则与DSL规范化的表示差异，没有并发读写证据，不称竞态。复现请求由迁移测试
直接构造；正常默认投影会映射user并删除anthropic_metadata，常规HTTP字段解析也会跳过
外围空白。未发现正常客户端请求因此失败，本次不增加运行时模拟、默认字段或双执行。

沿用现有回归验证公开typed编码、raw Execute、默认投影及两份草案。直接typed测试明确
保留原Value外围空白，避免被测试输入解析提前清掉。旧实现对两个目标的4个定向断言失败，
修复后通过；两份草案原有空白null差异转为成功输出逐字节比较，并核对调用者输入未修改。
失败及成功日志在`.tmp-dev/protocol-dsl-2026-10-29/checks/null-identity-before.*`和
`null-identity-after.*`。其他迁移gap快照未修改。

新增判断只查看这两个标识，不扫描正文或历史，不增加锁、goroutine或缓存。正常字符串路径
不增加null检查；这是代码层面的成本说明，未发布本切片的性能改善数值。

修复后Go全量（约110秒）、vet及身份／hint／两草案tracking-user相关race均通过。
固定staticcheck v0.8.2仍以原45条诊断退出1，去行号后新增0、移除0。原始日志和对照保存为
`checks/null-identity-go-all.*`、`null-identity-vet.*`、`null-identity-race.*`、
`null-identity-staticcheck.*`及`null-identity-staticcheck-comparison.json`。
本次不重跑未修改的前端，也未重新构建六目标；现有a4a8f275归档仍为dev.80，不能当作dev.81产物。
