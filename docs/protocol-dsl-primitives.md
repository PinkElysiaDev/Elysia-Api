# 编译式 DSL 的局部值、遍历与原生来源

本文记录 `refactor/protocol-dsl` 中已经实现的构件，不代表四个生产预置已经迁移。新包仍通过独立的 `CompilePackage` 入口使用；生产加载器、core1 预置及 dev.78 尚未切换。迁移状态见 [进度记录](protocol-dsl-migration.md)。

表达式在配置编译时解析成计划。运行时执行选中的计划，不重新解释 JSON 声明，不在错误后换另一种转换算法。以下构件不包含供应商字段或跨协议取舍政策。

## 递归对象并集

`merge_deep` 按顺序求值全部 `items`，再合并对象。只有双方都是 object 的重叠成员才递归；其它重叠值一律报错，即使值相等。数组作为完整值处理，不拼接。缺失、null 和非对象操作数不会自动替换为 `{}`。原有 `merge` 的浅合并行为保持不变。

可选 `onConflict` 只为本次合并的非对象叶子冲突声明 `code`、`stage`、`pathPrefix`、
静态 `reason` 和可选 `suggestion`。动态业务路径由 `ObjectMergeConflictError.Path` 追加，
原错误继续可通过 Unwrap 读取；操作数内部错误、取消和预算错误不在此边界重分类。
未声明时保留原诊断。此机制不新增成功路径扫描或预先验证，并非通用 try/catch。
诊断元信息在编译期固定，编译结构多一个可空指针，不能称为完全没有内存成本。

```json
{
  "op": "merge_deep",
  "items": [
    {"op": "read", "from": "input", "path": "/mapped"},
    {"op": "read", "from": "input", "path": "/extension"}
  ]
}
```

例如 `{"nested":{"known":1}}` 与 `{"nested":{"extra":2}}` 合为一个对象；两个 `{"nested":{"known":1}}` 在 `/nested/known` 冲突。错误链保留 `ObjectMergeConflictError.Path`，路径使用 RFC 6901 转义，不含成员值。当前通用映射入口仍将此错误归为 `invalid_input`，定位到表达式；这尚未等价复现旧模块的 `unsupported_capability` 与 wire 路径诊断。

实现共享不可变子值，只为合并的对象构造新容器，不逐层编码，也不授予原生来源。深度、累计访问成员数、执行步骤、逻辑字节和取消沿用引擎预算。已有原始对象读取仍执行容器规范化：重叠的多层 raw 对象可能重复扫描其中的大叶子；“没有逐层编码”不表示“没有重复扫描”。`BenchmarkDeepMergeNestedPayload` 提供 2 MiB 叶子在 1／4／16 层下的 raw／已构造容器对照，收益必须另行测量。

编码方向提供两种有限 `emit` 证明。无条件 effect 可依赖明确排除 object／any 的目标 wire schema：原有预检拒绝缺失输入，成功合并不能覆写非对象值，nullable string 的 null 同样不能覆写。这条证明不适用于整对象。

条件 effect 的承诺仅在 `when` 成立时生效。条件只允许 `all` 合取、对声明输入的精确 `exists` 以及固定输入路径上的 `not exists`，拒绝直接矛盾、`any`、`not present` 或任意真假表达式。一个操作数必须通过原有完整复制证明；其它操作数必须能证明不写该输出路径的整个首级成员，而不只是其某个子字段。缺失证明仅识别不含该成员的 object、明确排除它的 project、受精确缺失条件约束的可选 input read，以及两分支均缺失的 if；不依赖错误返回、不追踪别名或循环。根 if 的两分支分别证明，不推测其条件。这样可以保留对象值，并在其它 owner 会修改它时明确不作复制承诺。证明只在编译时执行，新增遍历及条件比对受 Nodes 预算限制；解码方向不开放这两条规则，也不通过收紧协议 schema 或删除 effect 迁就迁移。

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

`type` 区分 missing/null/string/array/object/boolean/number；查询本身不解码大正文，但source求值仍可能读取、规范化字段。`is_integer` 按现有 int64 解码契约判断。`integer` 支持 add/subtract/lt/lte/gt/gte，先按顺序计算所有操作数，再验证类型及溢出，不使用浮点数或默认零掩盖错误。

`keys` 返回排序、去重并独立拥有的对象键名。编译器可为直接读取typed Request中Object字段的
`keys/type(read input/root)`选择生成的对象访问器，包括参数、工具选项及节点属性；不硬编码协议字段。
该入口已通过完整输入计量，因此无需为取名字或判断类型再次规范化各成员值。它仍执行原read与查询两次
步骤／取消检查、required和缺失／null语义、UTF-8键归一化及输出转义预算。
生成规则复用核心字段标签，遇到任意Value即停止；raw输入、item／binding及不符合静态路径
资格的读取事先选择原算法，执行失败后不重跑。它不新增运行时字段、缓存或Native来源权限，
也不消除完整输入计量及实际输出编码。`type`的这条路径仍严格区分missing、null和object；
任意Value叶、raw输入及其他作用域保持原求值路径。

Spec 的 `WireAssertion.check` 在 schema 通过后检查选中值，`when` 检查所在对象，root 保持原始文档。`each` 只对明确声明的非 nullable 数组逐项应用子断言，诊断路径按原始索引追加。断言按声明顺序返回首错，错误原因使用静态说明，不把正文带进日志。

转换取舍属于 Mapping effects，不能放进这些原语。缓存及 historyRetention 的有限处置分别见 [缓存说明](protocol-cache-dispositions.md) 和 [历史保留说明](protocol-history-declarations.md)。

## 对象键值项

`entries` 把对象变成按解码键排序的 `[{"key":...,"value":...}]`；空对象得到空数组，
显式 null 保留，原对象重复键沿用最后一个值。它不把缺失或 null 对象替换成空对象。
`from_entries` 反向构造对象，每项必须恰有字符串 key 和 value；空字符串键是合法 JSON 键。
`policy:"reject"` 拒绝任何重复键，`policy:"identical"` 只允许编码字节完全相同的重复值，
数字 `1` 与 `1.0` 仍冲突。支持与 `merge_deep` 相同的局部 `onConflict` 诊断。

二者复用延迟容器和不可变叶子，不逐项编码正文，也不授予 Native 来源。
遍历逐项执行原有步数／取消检查，构造结果继续受逻辑字节和最终节点／深度预算限制。
这组工具用于动态字段映射，不内置 metadata 或供应商语义。

显式 `reject` effect 可用 `diagnostic` 指定 code、stage 和可选 suggestion，path、reason、id
继续取自 effect。仅实际拒绝时采用该分类，条件求值、emit 缺失和 aggregate 失败不会被捕获。
带投影 scope 的 effect 暂不接受此字段，避免配置被另一处理边界静默忽略。

## 相邻分组

`group_runs` 对数组的每项计算一次布尔 `when`，把相邻、判断结果相同的项放入同一子数组。
不排序、不丢弃成员、不合并隔开的同类组；空数组仍为空数组。缺失、null、非数组来源及
非布尔条件均拒绝。`item` 和 `index` 指向本次输入数组的元素和位置。

```json
{"op":"group_runs","source":{"op":"read","path":"/items"},"when":{"op":"read","from":"item","path":"/isStandalone"}}
```

输出只增加数组容器，成员保留不可变视图和已证明的核心类型，不重新编码大正文，也不
授予新的 Native 来源。条件求值沿用步骤／取消检查，分组容器计入逻辑字节与最终节点、
深度预算。协议配置可据此保留正文段与独立条目的交错顺序；原语不认识消息或工具类型。

## 分段文本比较

`equal_text` 的 `items` 必须恰有两项；每项可以是字符串或字符串数组。比较解码后的连续
文本，不把分段差异视为内容差异，也不构造拼接后的完整字符串。空数组与空字符串相等；
不把 null、缺失、数字或非法数组成员当作空文本。两侧表达式先按顺序执行，再校验两侧
全部成员，因此前面的不相等不能隐藏后续非法值。

```json
{"op":"equal_text","items":[{"op":"read","path":"/summary"},{"op":"map","source":{"op":"read","path":"/parts"},"body":{"op":"read","from":"item","path":"/text"}}]}
```

临时解码允许借用未转义字符串，仅存活于同步比较；输出只有布尔值，不进入流状态。
Unicode 转义和非法 Unicode 的处理与现有字符串解码一致。操作数和解码文本受字节预算
约束，成员遍历保留步骤／取消检查。是否需要比较何种协议字段，由 Spec 或 Mapping 决定。
