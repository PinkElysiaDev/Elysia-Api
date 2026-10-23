# Responses 辅助 metadata 帧

`response.metadata` 有两种独立形状。存在 `headers` 字段时继续执行原有 turn-state 规则，包括空值、重复大小写头和混合载荷的拒绝；不会在校验失败后改走其他分支。

无 `headers` 的辅助帧必须包含且只包含 `type`、`metadata`、`response_id`、`sequence_number`。metadata 为不透明对象，响应 ID 必须属于已经开始且尚未终止的响应，序号必须合法递增。它不能建立响应身份、完成生成或改变 usage。

源解码保留 `NativeEvent` 原帧。只有实际使用 Responses 解码模块、且通过包络校验的事件，才由 runtime 赋予受保护的 `Native.Source.Path=/metadata` 来源标记。手写映射、协议名称或 family 标签不能产生该自动省略资格。

- 同协议沿用原生帧重放，保持值、位置和原始字节。不同定义 ID 可以复用同一模块，但 family/wireVersion、映射 hash、scope 和事件摘要检查仍执行；不同 after 不能被绕过。
- 跨协议使用现有 `event-metadata` 规则和 `response_metadata` 动作。兼容模式只将该辅助事件转为不输出正文的 metadata no-op，并记录降级；严格模式拒绝，禁用规则仍由 native 保护拒绝。
- 额外生成字段、普通 metadata、其他 unmapped 数据以及身份、scope 或序号不一致均不能通过省略分支被清除。其他 NativeEvent 不受影响。
- Collector 验证事件后忽略这类辅助载荷，不将其写入生成正文、工具结果或续传历史，也不关闭已有的收集器。流中的原帧保真由转发层负责。

最终 after/wire 校验仍使用同一包络规则。只有辅助帧后 EOF、终止后到达、错误响应 ID、序号回退及资源超限继续失败。此项改变接受与转换语义，编译器 dev.77 通过既有机制重验证据，不修改用户定义或绑定。
