# 编译式 DSL 的局部值、遍历与原生来源

本文记录 `refactor/protocol-dsl` 中已经实现的构件，不代表四个生产预置已经迁移。新包仍通过独立的 `CompilePackage` 入口使用；生产加载器、core1 预置及 dev.78 尚未切换。迁移状态见 [进度记录](protocol-dsl-migration.md)。

表达式在配置编译时解析成计划。运行时执行选中的计划，不重新解释 JSON 声明，不在错误后换另一种转换算法。以下构件不包含供应商字段或跨协议取舍政策。

## 复用局部值

`let` 的 `source` 在外层作用域执行一次，`body` 用 `read.from=binding` 读取结果。名称在编译时转换为整数槽位，槽位按最大词法深度分配，兄弟分支复用。离开绑定后恢复外层值；不可变大字符串不会因绑定而重新编码。

```json
{
  "op": "let",
  "name": "message",
  "source": {"op": "read", "from": "item", "path": ""},
  "body": {
    "op": "object",
    "fields": {
      "role": {"op": "read", "from": "binding", "name": "message", "path": "/role"},
      "content": {"op": "read", "from": "binding", "name": "message", "path": "/content"}
    }
  }
}
```

`index` 只在数组迭代内可用，表示当前直接来源数组的位置。过滤、排序或重新构造后的数组有自己的索引；这个数字不证明原始 wire 位置。

## 显式递归

`walk` 与 `map` 一样逐项执行 body，并允许 body 中的 `recur` 对显式选择的子数组再次执行最近的 walk body。input/root 不变，item/index 进入当前子数组；父值可用 let 保存。运行时复用已编译 body，没有循环展开的 AST。

```json
{
  "op": "walk",
  "source": {"op": "read", "from": "input", "path": "/content"},
  "body": {
    "op": "object",
    "fields": {
      "payload": {"op": "read", "from": "item", "path": "/text"},
      "children": {
        "op": "if",
        "when": {"op": "exists", "source": {"op": "read", "from": "item", "path": "/content"}},
        "then": {"op": "recur", "source": {"op": "read", "from": "item", "path": "/content"}}
      }
    }
  }
}
```

这里没有选择工具 arguments 或其他未知 JSON，因此不会进入它们。来源必须是数组；null 不自动变成空数组。递归深度、累计访问项、执行步骤、逻辑输出字节和取消都沿用引擎预算，省略结果也计访问量。walk 外的 recur 与循环 ref 在编译时拒绝。

## 捕获真实原生来源

`capture_native` 仅能出现在 v3 解码 program。source 必须是来自 input/root/item/binding 的 read，不能是作者提供的路径字符串、上下文对象或重建的对象。

```json
{"op":"capture_native","source":{"op":"read","from":"item","path":""}}
```

运行时记录原始数组的来源锚点，并让 let、walk/recur 沿实际读取保持锚点。协议身份、方向和初始 scope 由运行时提供；typed 请求的授权和最终 scope stamping 继续走原流程。产生的 Native 不允许声明自行填写这些身份。

新包的正式 typed 解码在写入 Native／Provenance 结构时检查私有捕获标记。普通 object、literal 或 wire read 即使构造出相同的身份、scope、路径和值，也不能成为可信来源。原样读取及 let 绑定保留标记；merge、project 等对象重建和 JSON 物化不保留标记。工具参数、schema 和普通 Value 载荷中的同名 `native`／`source` 键仍是用户数据，不受结构来源检查。

`Execute` 是未经过最终身份 stamping 的底层检查／编写入口，返回 JSON，不是正式请求的授权结果。其序列化输出不携带私有标记。正式 `DecodeRequest`／`DecodeResponse` 使用一次既有核心 writer 验证来源；拒绝不会通过再次解码恢复为成功。旧 v2 解码合同不因此改变。

过滤、排序及合成容器不获得原位置。需要重排时，在原始迭代内先捕获，再移动已经带 Native 的语义项；不能将过滤后的 index 解释为原位置。无法证明来源时明确报错，不扫描全文猜测，也不通过内容相同推断身份。关闭 native preservation 时不生成快照。

捕获只提供来源证据，不自动授权原生重放。作用域、数组身份、映射指纹、未知字段和最终 wire 校验仍需通过。节点级重建、丢弃语义后的 Native 删除证明及完整预置映射仍在迁移中。

## 类型、整数与 wire 断言

v3 program 与普通 effect 条件还可读取唯一运行时事实：
`{"op":"read","from":"runtime","path":"/interleavedThinking"}`。
它直接来自网关传入的 EvaluationContext，不读取正文或 Values 中的同名字段；
真实 false 保持布尔 false，遗漏内部绑定会报错。Spec 断言、局部处置条件与 v2
不能使用此入口。来源身份及 scope 仍由 capture_native 处理，不在 runtime 中开放。

`type` 区分 missing/null/string/array/object/boolean/number，不解码大正文。`is_integer` 按现有 int64 解码契约判断。`integer` 支持 add/subtract/lt/lte/gt/gte，先按顺序计算所有操作数，再验证类型及溢出，不使用浮点数或默认零掩盖错误。

Spec 的 `WireAssertion.check` 在 schema 通过后检查选中值，`when` 检查所在对象，root 保持原始文档。`each` 只对明确声明的非 nullable 数组逐项应用子断言，诊断路径按原始索引追加。断言按声明顺序返回首错，错误原因使用静态说明，不把正文带进日志。

转换取舍属于 Mapping effects，不能放进这些原语。缓存及 historyRetention 的有限处置分别见 [缓存说明](protocol-cache-dispositions.md) 和 [历史保留说明](protocol-history-declarations.md)。
