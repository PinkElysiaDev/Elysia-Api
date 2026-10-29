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

## 在编码声明中使用原生信息

`replay_native` 的 source 是核心 Native 对象；运行时将实际目标的协议、方向、scope
及有效预算交给现有 `ReplayNative`，声明不能用正文或 Values 替换目标身份。
缺少 Native、跨 wire、scope 不符及预算错误保持各自错误，不自动改走语义重建。

```json
{"op":"replay_native","source":{"op":"read","from":"item","path":"/native"}}
```

输出是当前 DSL 输入中可观察的 Native.Value。typed 核心读取仍遵守原来的 JSON 规范化，
不能据此宣称恢复了规范化前的空白或转义拼写；后续 project/merge 也会按既有容器规则编码。
整请求的原始字节保留、workspace 与数组协调继续由原生协调层负责。

`native_matches` 的 source 是 Provenance，不读取整份快照。它判断来源是否与实际目标
具有兼容的 family、wireVersion、方向，以及来源位置是否为指定数组下的直接成员：

```json
{
  "op":"native_matches",
  "source":{"op":"read","from":"item","path":"/native/source"},
  "operation":"array_item",
  "path":"/messages"
}
```

这是位置分类，不是重放授权：不检查 scope，不要求原位置等于当前索引；真正取回载荷的
`replay_native` 仍检查 scope。数字部分沿用现有 Atoi 的非负整数规则，包括 `+1`、`01`。
原快照 role 等协议条件仍由声明组合 read/equal 检查，核心原语不认识供应商字段。

两种原语只允许用于 v3 编码 program；Mapping effects、Spec 断言、v2 和解码 program 不开放。
未使用它们的计划不创建原生目标上下文。操作数使用闭合 Native/Provenance 核心形状；
这不能等同于旧 raw 接口对任意含未知核心字段 JSON 的宽松解码。

原语来源还必须在编译期证明为原核心的 Native／Provenance 读取。证明按实际核心 Go 类型
和 JSON 标签走路径，遇到 Value 或 map 业务 JSON 即停止，不靠名为 native 的键猜测。
let、保留成员的 filter/sort/map 等可以传递已证明的原值；重建 object、merge/project、
JSON 往返、recall 以及未证明同类型递归的 walk item 不获得资格。
因此不能重建 Source.scope 后冒充原生授权；原数据跨 scope 仍由 ReplayNative 拒绝。
该证明同时约束 raw 和 typed 程序，不在请求中增加全文扫描或内容哈希。

## 拆分媒体容器

`split_data_uri` 只拆分字符串容器，不解码或验证媒体内容：

```json
{"op":"split_data_uri","source":{"op":"read","from":"item","path":"/url"}}
```

source 求值、字符串解码各一次。非精确 `data:` 前缀返回 `{"kind":"other"}`；
有此前缀但缺首个逗号，或 metadata 不以精确 `;base64` 结尾，返回 `{"kind":"data"}`；
匹配时返回 `{"kind":"base64","mime":"…","data":"…"}`。MIME 是去掉最后标记的完整 metadata，
data 保留首逗号后的全部文本，两者允许为空。缺失、null 和非字符串不被默认成 URL。

kind=base64 只说明存在该标记，不保证合法 base64。原语不补 MIME、不转码、不解码百分号，
也不改变大小写。是否保留空 MIME 的原 URL、是否拒绝非 base64 data URI，由协议声明决定。
输出字符串独立拥有存储，不保留一份原 URL；形成 data 字符串仍有必要的编码分配。

## 类型、整数与 wire 断言

v3 program 与普通 effect 条件可读取两个运行时事实：
`/interleavedThinking` 是布尔值，`/scope/model` 是当前绑定模型的字符串。
读取写作 `{"op":"read","from":"runtime","path":"/scope/model"}`。
它们直接来自 EvaluationContext，不读取正文或 Values 中的同名字段；
真实 false 和空模型字符串保持原值，遗漏内部绑定会报错。context.model 是否优先、
null 是否覆盖缺省模型，由声明表达。Spec 断言、局部处置条件与 v2 不能使用此入口；
未列出的运行时路径在编译期拒绝，来源身份及其余 scope 字段不在这里开放。

`type` 区分 missing/null/string/array/object/boolean/number，不解码大正文。`is_integer` 按现有 int64 解码契约判断。`integer` 支持 add/subtract/lt/lte/gt/gte，先按顺序计算所有操作数，再验证类型及溢出，不使用浮点数或默认零掩盖错误。

Spec 的 `WireAssertion.check` 在 schema 通过后检查选中值，`when` 检查所在对象，root 保持原始文档。`each` 只对明确声明的非 nullable 数组逐项应用子断言，诊断路径按原始索引追加。断言按声明顺序返回首错，错误原因使用静态说明，不把正文带进日志。

转换取舍属于 Mapping effects，不能放进这些原语。缓存及 historyRetention 的有限处置分别见 [缓存说明](protocol-cache-dispositions.md) 和 [历史保留说明](protocol-history-declarations.md)。
