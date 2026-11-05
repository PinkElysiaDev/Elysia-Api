# Agent 待审批轮次替代修复

最终出站合同检查复现了真实历史缺口：新用户指令替代待审批轮次时，旧工具调用没有对应结果，Chat 上游会收到关联不完整的历史。修复仅处理会话快照中仍为 `waiting_approval` 的待执行尾批：先清除审批，再为每个未执行调用追加取消结果，最后加入新用户消息。不执行这些工具，也不重复已经执行的工具结果。

清除审批失败时停止新轮次；工具结果写入失败仍沿用现有脱敏内存历史，保留失败提示。正常轮次的历史读取和摘要时点不变；新分支复用原工具结果构造和用户消息渲染。

Windows amd64、Go 1.27.2 验证：

- `phase-seven-agent-supersede`：Agent/A2A 定向通过。
- `phase-seven-server-final`：Agent、server 全量通过，约 183 秒。
- `phase-seven-agent-supersede-race`：新轮次、恢复、替代审批及 A2A 相关 race 通过，约 26 秒。

这是请求历史完整性的修复。未改变工具权限、模型调用重试或协议转换规则，也未进行性能收益宣称。原始检查日志位于忽略目录 `.tmp-dev/protocol-three-part-2026-10-31/checks/`。
