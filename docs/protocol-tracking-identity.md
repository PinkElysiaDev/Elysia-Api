# 请求用户标识的声明迁移边界

Chat和Responses草案以JSON表达`parameters.user`与`parameters.anthropic_metadata.user_id`
到线格式`user`的对应关系，没有新增Go供应商分支或原语。生产仍使用旧模块，草案不激活。

- 已有`user`（包括null）优先；缺失时才取metadata中的user_id。
- metadata为对象时只允许user_id。两标识同时存在时必须一致，不能覆盖真实冲突。
- 双方字符串使用解码后的值相等；其它raw值直接读取叶子后stringify再比较，保留数字拼写、
  对象顺序和空白差异。不得先装入新容器导致这些表示被规范化。
- 旧raw入口忽略非对象metadata的行为继续保留；公开typed入口的既有hint校验不变。

字符串等价的独立正确性修复是`f3a5fafa / dev.80`，其接受边界记录见
[用户身份修复](user-identity-equivalence-2026-10-30.md)。这里的声明迁移不再扩大该授权。

## 差分结果及未闭合项

两草案共50项新增比较：34项成功输出字节完全相等；14项均拒绝但诊断仍不同；
2项明确保留typed空白null的接受差异。共享测试入口是`checkPackageTrackingUsers`，
既有迁移gap期望没有修改。两份完整迁移与四草案foreign-owner定向均通过。

旧直接编码的冲突为`unsupported_capability / wire`，声明effects为
`conversion_rejected / mapping.effects`。未知metadata的旧路径还包含实际成员名，
新effect目前固定到metadata对象。新两个guard按unknown→conflict排列，已有effects相对
顺序不变；尚未闭合旧renderer跨阶段、多故障的完整首错顺序。

另有一个已实测的合法typed输入：`user`的Value保存` null `，metadata的user_id保存`null`。
旧编码器对非字符串仍比较原JSON字节，因而拒绝；typed DSL读取已去掉外围空白，草案接受并
输出`user:null`。两个目标均复现。此差异没有通过更新旧期望或增加raw读取框架隐藏，也不能
因为其只涉及空白就当成行为相等。完全迁移前须独立决定这个旧接受边界。

证据位于`.tmp-dev/tracking-user-migration-2026-10-30/`，包括50个子场景的输出与错误记录、
完整迁移定向日志。全部标识为合成数据；未运行真实供应商或本切片的性能测量。
