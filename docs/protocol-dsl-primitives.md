# 编译式 DSL 的局部值、遍历与原生来源

本文记录 `refactor/protocol-dsl` 中已经实现的构件，不代表四个生产预置已经迁移。两份源通过 `CompileSources` 编译，完整包通过 `CompilePackage` 编译，管理入口共同保存其修订；生产切换与验收状态见 [进度记录](protocol-dsl-migration.md)。

表达式在配置编译时解析成计划。运行时执行选中的计划，不重新解释 JSON 声明，不在错误后换另一种转换算法。以下构件不包含供应商字段或跨协议取舍政策。

`encode_event.semanticRanges` 使用与 deliveryRanges 相同的 name／measure／texts／ranges 记录，
检查目标编码前的核心事件。选择器只能读取 type、responseId、itemId、parentId、collection、delta、
metadata 及 item 的 kind／payload／metadata。供应商引用字段及字符单位在 Mapping 中；原始值由
宿主转换边界保留，不能通过配置 Source 或布尔标记提交证明。未变化路径复用选择结果，不复制正文。
改变文本的事件可先输出，后续引用必须位于已证明未变的区间且索引正确；snapshot 复用前缀摘要。
该状态属于编码事务，表示来源与目标暂存内容的关联，不替代最终 wire 检查和 Flush 后交付提交。
目前此声明仅接线到 frameBatch 的事件输出；完整响应的语义布局改写尚未使用它。

`encode_event.nativeWhen` 可只读 `runtime.clientOutput` 和 `/encoding/hasRendered`。后者沿用本方向
既有编码状态，表示此前执行过语义编码；原生 observer 不设置它。Mapping 用它决定包含历史内容
的聚合帧是否可以原样重放。它不证明 Write／Flush 成功、正文等价、引用有效或资源权限。
编译器限制读取权限并隔离命名程序缓存；无状态程序使用这一事实时只安装固定编码状态，不计动态实体。
Responses 在语义编码开始后重建 message 的 output_item.done 及完整响应终态，继续允许合法的独立增量原样重放。
该选择在执行前完成，不以最终校验失败触发重试，也不缓存整轮原帧或额外正文。

## 构造与最终交付

`encode_request`、`encode_response`、`encode_event` 可用 `finalDocument` 引用另一份 Spec 命名文档。原 `document` 仍约束
编码结果及 Native 协调后的结果；未声明 `finalDocument` 时，最终交付继续使用同一文档。
用户原有的严格 schema 不会因为保留 Native 而被绕过。

请求的 `upstream-request` 与 `generation-request` 分别记录最终 wire 和构造约束。
当前请求结构切片及未闭合的工具／深层校验见[请求最终约束记录](../backend/protocol/builtin/packages/json-final-request-contracts.md)。

例如方向中的两个绑定可以是：

```json
{"document":"response-candidate","finalDocument":"client-response"}
```

两份文档分别描述允许构造／恢复的响应和实际可交付的响应。例如 Chat：
旧 typed 编码允许原样恢复来源中的裸 `error` 字符串，但最终 wire 检查要求错误对象。
将两者绑定到同一 schema 会提前改变组件接受边界；把最终 schema 放宽又会漏掉发送检查。
现在这个差异由明确的配置表达，不依赖失败后的回退或供应商 Go 分支。

`ValidateWireOutput`、`ValidateWireOutputContext` 和相应对象读取入口选择最终文档；
`NewWireStreamValidation` 同样选取最终事件文档。两个实际文档内容都进入方向指纹，
改变最终合同会使相关验证证据失效。JSON 请求/响应必须绑定非流文档，事件必须绑定流文档；
尚未扩展到任务或上游会话编码。组件样例执行器只执行编解码；`Verify` 在匹配、往返之后
检查已声明的最终 JSON 请求/响应文档，通过后才计入能力证据。组合发送验证仍独立检查最终字节。

Anthropic 使用事件构造文档保留旧裸编码样例的边界，最终文档要求初始 message 的
完整身份、角色、content 和 usage，以及 message_delta 的 stop_reason 与输出计数。
Thinking 的初始空块不需要完成签名，不能直接套用完整 JSON 响应的 thinking 约束。

`closure-dev93-protocol` 已通过本边界定向、四协议错误映射、119 份迁入 JSON 样例及
原自定义严格 Native 保护。该记录不表示四预置完整迁移或性能验收完成；原 invalid
HTTP context 的底层解码文字与静态诊断仍有明确记录的差异。

## 输入子文档作用域

`with_input` 对 `source` 求值一次，在 `body` 中把它绑定为 input；root 始终保持调用时的
完整输入。它直接传递不可变 JSONOutput，不编码中间正文，适合复用 JSON 响应程序处理
真实 SSE 的 `/response` 包络。

```json
{"op":"with_input","source":{"op":"read","path":"/response"},"body":{"op":"ref","ref":"decode-response-document"}}
```

原文来源位置跟随实际子值，原始 root 的位置不随 input 切换。构造出来的对象不因此
获得原文身份；编译器分别保留 input/root 的核心类型，不能把子对象类型授予整个 root。

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

需要来源位置但不需要原生快照时，使用 `capture_source`，source 具有相同的原文读取限制。
它只产生可信 Provenance，不持有选择的正文；关闭 `native.preserve` 后仍工作。
例如 metadata 的来源和节点分类不应依赖用户是否保存 Native：

```json
{"op":"capture_source","source":{"op":"read","from":"item","path":""}}
```

`capture_source` 不授予 Native 重放权限。它与 `capture_native` 共用位置证明、运行时身份、
预算和最终 scope 绑定；选择不存在或经过重排构造的值仍拒绝。

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

需要保留数组成员的单个来源字段时，使用 `operation:"array_item_field"`，`path` 仍为父数组指针，`name` 是该成员的直接字段名。例如 `path:"/choices/0/delta/tool_calls"`、`name:"index"` 匹配 `/choices/0/delta/tool_calls/2/index`，而不匹配整个工具或更深的字段。字段名在编译时按 JSON Pointer 转义；只捕获索引整数即可保持混合原生／重建流的工具位置，不需保留工具正文。

两种原语用于 v4 编码 program 及原生 observer；Mapping effects、Spec 断言、旧模块和解码 program 不开放。
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

事件程序也可读取 `runtime /clientOutput` 及其核心字段。未提供交付意图时是 missing；
空对象、IncludeUsage 缺失、false、true 分别保留。字段由既有 typed 读取器直接访问，
不序列化请求历史。Chat 的 usage 尾帧是否输出由 Mapping 使用这些值决定。

`length` 对 `source` 数组计数。构造中的数组直接读取成员数；原始数组使用现有数组读取。
缺失、null、非数组都报错，不把它们当成空集合。它可用于把辅助字段附在一个事件批次的
末事件，不需要为此新增跨帧计数器。

`type` 区分 missing/null/string/array/object/boolean/number；查询本身不解码大正文，但source求值仍可能读取、规范化字段。`is_integer` 按现有 int64 解码契约判断。`integer` 支持 add/subtract/lt/lte/gt/gte，先按顺序计算所有操作数，再验证类型及溢出，不使用浮点数或默认零掩盖错误。

`keys` 返回排序、去重并独立拥有的对象键名。编译器可为直接读取typed Request中Object字段的
`keys/type(read input/root)`选择生成的对象访问器，包括参数、工具选项及节点属性；不硬编码协议字段。
该入口已通过完整输入计量，因此无需为取名字或判断类型再次规范化各成员值。它仍执行原read与查询两次
步骤／取消检查、required和缺失／null语义、UTF-8键归一化及输出转义预算。
生成规则复用核心字段标签，遇到任意Value即停止；raw输入、item／binding及不符合静态路径
资格的读取事先选择原算法，执行失败后不重跑。它不新增运行时字段、缓存或Native来源权限，
也不消除完整输入计量及实际输出编码。`type`的这条路径仍严格区分missing、null和object；
任意Value叶、raw输入及其他作用域保持原求值路径。

Spec 的 `WireAssertion.check` 在 schema 通过后检查选中值，`when` 检查所在对象，root 保持原始文档。`each` 只对明确声明的非 nullable 数组逐项应用子断言，诊断路径按原始索引追加。断言按声明顺序返回首错。

错误说明在静态 `reason` 和 `reasonExpression` 中选择一个。后者必须编译为字符串，
只读取当前 wire 容器或 root，沿用现有表达式与预算；仅在断言失败后求值一次。
它与 `when` 的 input 相同，不改为 `check` 的选中字段，便于从兄弟字段读取事件类型。
例如 `join` 静态前缀与 `quote_string(read input /type)` 可保留既有 `%q` 事件名诊断。
声明应只引用需要报告的标识，不能引用私有正文；分类、阶段和诊断位置仍由静态配置决定。
表达式失败时保留原校验原因和表达式错误，不退回另一条说明；取消仍保持取消错误。

转换取舍属于 Mapping effects，不能放进这些原语。缓存及 historyRetention 的有限处置分别见 [缓存说明](protocol-cache-dispositions.md) 和 [历史保留说明](protocol-history-declarations.md)。

## 对象键值项

`entries` 把对象变成按解码键排序的 `[{"key":...,"value":...}]`；空对象得到空数组，
显式 null 保留，原对象重复键沿用最后一个值。它不把缺失或 null 对象替换成空对象。
`from_entries` 反向构造对象，每项必须恰有字符串 key 和 value；空字符串键是合法 JSON 键。
`policy:"reject"` 拒绝任何重复键，`policy:"identical"` 只允许编码字节完全相同的重复值，
数字 `1` 与 `1.0` 仍冲突。支持与 `merge_deep` 相同的局部 `onConflict` 诊断。
`policy:"last"` 显式选择同名键最后一个值，用于累计 metadata 更新；不允许同时声明
不会触发的 `onConflict`。其它策略的接受边界不变。

二者复用延迟容器和不可变叶子，不逐项编码正文，也不授予 Native 来源。
遍历逐项执行原有步数／取消检查，构造结果继续受逻辑字节和最终节点／深度预算限制。
这组工具用于动态字段映射，不内置 metadata 或供应商语义。

显式 `reject` effect 可用 `diagnostic` 指定 code、stage 和可选 suggestion，path、reason、id
继续取自 effect。仅实际拒绝时采用该分类，条件求值、emit 缺失和 aggregate 失败不会被捕获。
带投影 scope 的 effect 暂不接受此字段，避免配置被另一处理边界静默忽略。

## 对象字段约束与有序拒绝

`check_fields` 校验当前选中的对象，成功后返回同一个不可变结果，不重新编码它。`keys` 是
允许的精确成员名，`prefixes` 是允许的非空前缀；其余成员按解码键排序后拒绝首项。
缺失、null 和数组不会被当成空对象。字段名、前缀和诊断在编译期固定，运行时只检查该对象。

```json
{
  "op": "check_fields",
  "source": {"op": "read", "path": "/attributes"},
  "keys": ["label"],
  "prefixes": ["ext:"],
  "policy": "member",
  "onUnknown": {
    "code": "unsupported_capability",
    "stage": "wire",
    "pathPrefix": "/attributes",
    "reason": "attribute has no declared mapping"
  }
}
```

`policy:"member"` 定位未知成员；`policy:"leaf"` 继续沿其对象内字典序首成员定位，遇到
标量、数组或空对象即停止。路径由静态 `pathPrefix` 和真实成员键组成，键使用 RFC 6901
转义，不从正文值生成路径。首叶定位与既有未知扩展保护共用实现。检查计入执行步骤，
不增加请求外缓存；对象读取产生的临时分配仍需通过性能测量评估。

`reject` 表达式用于在程序的确定位置终止选中分支。它与预先执行的 `reject` effect 不同，
可表达“先检查字段，再校验分类，再合并原生余项”的顺序，而无需构造一个故意非法的值。

```json
{
  "op": "reject",
  "path": "/error/category",
  "reason": "target has no equivalent failure category",
  "diagnostic": {"code": "unsupported_capability", "stage": "wire"}
}
```

`reason` 和诊断分类都是静态配置；诊断位置在静态 `path` 与返回指针字符串的 `source`
中二选一。后者用于复用核心 metadata 中已记录的原始路径，只在该拒绝分支执行时读取，
不改写请求间共享的编译诊断。此操作不捕获之前的错误、不重跑分支，也不把正文值插入
错误说明。取消和资源预算检查仍在执行前生效。编译器只对明确的 `reject`
分支承认“它不会成功输出”，不猜测其它条件永远为假；`emit` 证明仍检查所有能返回的分支。

WireAssertion 的可选 `diagnostic` 复用同一分类结构，并允许静态 `direction`、`path`
覆盖，支持 wire 计数对应到核心账本位置的情况。未声明时保持原 wire 归因；声明空 stage
或已有 stream 归因用于迁移既有边界，不意味着推荐新协议沿用历史错标。Mapping effect
继续从自身取得方向和位置，对这些覆盖字段明确拒绝，不能静默忽略。

`check` 中实际执行的 `reject` 保留该分支明确声明的诊断和原始错误链；返回 false 或其他
执行失败仍使用外层断言的诊断。这样同一次有序关联扫描可以指出真正失败的消息或字段，
无需先生成整张错误清单，也不把任意执行错误改写成协议关联错误。

## 词法索引的删除与计数

`forget` 删除 `with_index` 中的原始 JSON 键，返回该键是否存在；即使存入的值为 missing，
存在的键仍返回 true。删除同时归还当前作用域的逻辑条目数和字节预算，且仅操作词法索引。
`index_size` 可读取词法索引或已声明持久表的存活条目数，不枚举已保存的值；本事务的新键
计入数量，覆盖已有键不重复计数，失败事务不提交计数。新增计数保存在既有更新槽内，
不为每帧再分配一份计数数组。

```json
{"op":"forget","name":"pending","source":{"op":"read","from":"item","path":"/tool_call_id"}}
```

```json
{"op":"index_size","name":"pending"}
```

键继续按 `raw_json` 比较，因此 `"a"` 与 `"\u0061"` 不同；需要比较解码字符串的关联必须
由配置先明确规范化键。顺序、重复 ID、未匹配结果及末尾是否要求排空都由 Spec/Mapping
声明，原语不认识工具或消息角色。作用域退出时释放余下条目，嵌套同名索引仍互相隔离。

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

## 错误字段选择

转换控制步骤的 each.select:error 选择 Response.Error，或 Event.Error 与 Event.Response.Error。
局部 input 读取当前错误，root 读取完整核心对象；pathBinding 返回该错误的实际核心位置。
缺失或 null 没有条目。现存错误必须为对象，原有未知字段保留；此选择不解析整份请求或正文。

允许更新 message、code、param、details、providerType。不能替换整个错误、修改 category、
响应状态或事件类型，也不能改写来源、资源或扩展。message 必须仍是字符串。如何验证细节、
保留结构、转入消息或拒绝，由 Mapping 的普通 when／set／diagnostic 声明，执行器不判断
供应商或错误类别。严格模式与规则禁用沿用现有完整规则边界。

## 对象聚合证明

请求方向的 aggregate effect 以 layout 声明对象的单一所有者：remainder 是完整余项，fields
把外部成员分配给独立核心 Value 字段，labels 固定当前核心变体。路径和成员类型在编译时
绑定，来源、Native、资源和客户端意图不属于可选 owner。

默认 Anthropic 包用 /cache/0 把 cache_control.ttl 拆入 ttl，其余成员进入 value；
另一个合法声明可以把 /tools/0 的 inputSchema 作为余项、name 作为独立字段。原语不识别
协议或缓存字段。编译器要求可证明的 partition／reconstruction 程序，无法证明即拒绝配置，
不在运行时换引擎。

null 余项必须保持 null 且不带分拆字段。未知核心成员、变更标签、重复余项字段及没有表示
的尾部数组成员拒绝；未知外部对象成员保留在余项。命名程序／绑定、条件和独占合并使用
现有编译预算，运行期复用当前输入视图，不为证明解析配置或反射类型。

## 核心内容稳定分区

控制步骤 layout.operation:stable_partition 只作用于请求或完整响应的 /content。when 的
input 是当前根节点，root 是完整文档；选中节点稳定移到未选中节点前，两组内部顺序保持。
可声明 pathBinding 和 movedBinding，分别读取原核心位置与是否会移动。
局部 steps 复用诊断及条件，仅能更新 role，不能替换节点、改写
来源或删除资源。布局声明不能和同一步的 each、set、omit 或 auxiliary 混合。

combine_children 把选中的相邻同 kind、同 role 普通容器的 children 顺序合入第一个容器，显式
移除后续容器边界。身份、状态、payload、reasoning、metadata、缓存、资源或未知属性存在时
拒绝；来源和 Native 不被当作语义载荷，合并后只保留首容器来源并清除该容器 Native。边界
损失诊断由局部 steps 声明，该操作不能同时更新容器字段，也不能跨过未选中根；不能依赖编码器暗中合并。

group_runs 为每段相邻选中根构造 role 声明的 message 容器。选中的普通 message 展开 children，
其他叶子按原顺序移动；未选中根截断这一段。实际改变的 run 必须以无条件损失诊断开始，
局部步骤不能更新字段。单个既有 message 不变，不触发诊断或分配结果；需要展开的容器有身份、
状态、缓存、资源或其他附属语义时拒绝，不能把这些语义挂到合成容器或任意叶子上。

```json
{"layout":{"operation":"stable_partition","path":"/content","when":{"op":"equal","items":[{"op":"read","path":"/role"},{"op":"literal","value":"system"}]}}}
```

原节点载荷和子节点共享已有不可变所有权，不作 JSON 往返。发生重排时只创建节点切片；
无移动时不分配结果切片。结构变化清除根 Native，只有实际更新的容器清除自身 Native，
叶子的资源和来源保留。多个根的 children 合并仅复制节点切片。规则需在 Mapping 中声明角色、作用位置及边界变化的诊断；DSL
不识别 system、developer 或供应商。默认 Anthropic／Gemini 已使用此操作。

## 消息内容展开与文本合并

unfold_children 作用于 /content，when 选择消息，childWhen 选择需要成为根的子节点。
选中子节点按顺序展开；相邻未选中子节点保留在原角色的消息中，不跨其他根。
没有选中子节点时不修改输入；真正展开时要求普通容器，身份、metadata、缓存、资源或
未知属性须先由明确规则处理。steps 首项必须声明无条件边界损失诊断，不能更新字段。
Chat tool-result-history 使用它将混合用户历史中的工具结果拆为独立消息。

merge_text 只作用于请求或完整响应的 /content/*/children。text.messageWhen 可选择消息；
when 选择其子节点，选中的同类 text、refusal 或无附属状态的 reasoning 叶子合成一个载荷。
结果占据首个选中节点的位置；其他节点相对顺序不变。默认不允许跨越未选中节点，
text.across:true 明确允许交错布局变化；steps 声明损失，严格模式拒绝。

text.measure 是非负整数表达式，定义当前位置计量口径。utf16_length(source) 统计 JSON
字符串的 UTF-16 code unit 数，空字符串为零，补充平面字符占两个单位，不构造字符数组。
measure 不读取偏移绑定；offsetBinding、lengthBinding 只提供给诊断及 metadata 处置，
分别表示当前片段在合并文本中的起点和长度。长度不能超过原文 UTF-8 字节数的两倍。
pathBinding 保留原核心位置。messageWhen、when、measure 不读取偏移绑定，避免选择结果
依赖尚未进行的合并。

text.metadata 按已声明名称选择表达式，input 是原 metadata，root 仍为完整核心对象。
表达式仅改变 Value，不能伪造来源或资源；未声明条目及隐式 omit 拒绝。条目按原顺序保留，
目标编码按声明聚合它们。引用字段结构和偏移运算由 Mapping 给出，Go 不识别引用字段名。

无命中或单块不读取、拼接正文，不产生合并诊断；只有实际合并才计量、分配正文和节点切片。
一次预先计量后顺序写入，不逐项复制完整前缀。改变的文本和消息清除 Native，其余叶子保留。
Chat response-text 显式合并文本、refusal、可见 reasoning，修正 URL 引用的 UTF-16 偏移，
保留 logprobs 的原顺序；禁用后编码拒绝多块，不自行重新拼接。

## 流式位置区间

track_position 的 name 是固定输出通道，source 是稳定 item key，body 是本次追加的非负整数
长度，返回通道的新末尾位置。project_range 使用同一 name／source，body 为 start／end
对象，返回换算后的 start／end。Mapping 选择长度单位：Chat 将 track_text 的新后缀交给
utf16_length，避免重新读取或拼接完整前缀。原语只允许在流程序中使用。

每个 item 保留自己的局部长度和位置区间。连续来源和输出区间合并；一次查询用二分定位，
跨其他 item 插入内容或超出已观察范围时拒绝，不扩大范围。空 item 首次观察记录空区间，
后续空更新不移动其起点。固定通道不计为动态实体，动态身份与实际区间占用 StateItems。
字符串身份由现有 tracker 表独立持有一次，各通道引用相同 key；区间不保存正文。

新末尾和新增区间在现有事件事务中暂存，失败不会改写已提交区间；提交只追加新段或扩大
最后一个连续段，不复制历史区间数组。网络交付位置另由原有成功发送边界推进。
默认 Chat 流已接入：原生观察和重建使用同一后缀程序，引用去重先于位置换算。

## 流内损失授权

`conversionDefaults[].streamDisposition` 声明 `kind: degrade | reject`、`path` 和 `reason`，
仅用于 event 阶段，不能与 steps、recovery、control 或 use 混用。匹配条件限于路由；
规则 ID、order、enabled 和整条用户覆盖沿用转换策略。声明绑定目标身份和事件程序指纹。

目标事件 program、observe 或 finalize 通过 `require_disposition` 请求处置：`name` 是
编译绑定的规则 ID，`source` 返回实际 JSON Pointer 诊断位置，空位置使用声明的 path。
表达式返回 true 并登记事务请求；不能在来源解码、只读 effects 或请求程序中调用。
宿主用 `EvaluationContext.WithConversion` 绑定本次已解析策略及路由，配置值不能伪造绑定。

输出计划构造后、编码前，只查询被请求的规则并按有效顺序执行。兼容模式接受明确的 degrade；
strict、reject、禁用、未匹配、规则被替换成其他动作或缺少绑定均拒绝。未发生损失时不要求绑定。
检查、编码或 Native 协调失败时，不提交事件状态或成功诊断。原生 observer 执行相同授权。
该原语不授予 Native 重放、受保护资源、权限、usage 或终态修改权，也不替代成功交付确认。

Chat `event-layout / 310` 使用现有目标身份及通道索引识别 message 和同类 item 的边界损失，
仅在首次观察相应边界时请求授权，不为每个 delta 重复登记。正文按到达顺序输出，
reasoning、refusal、text 保持不同通道；容器附属语义及独立候选仍需明确投影。

## 最终响应与事件引用关联

`encode_response.deliveryRanges[]` 和 `encode_event.deliveryRanges[]` 使用已有表达式建立只读的最终交付观察，不增加供应商原语。
`measure` 的 input 是字符串，结果必须为非负整数；默认包直接绑定 `utf16_length`，不创建字符数组。
`texts` 返回 `{key, text, mode, path}` 数组，`ranges` 返回 `{key, id, start, end, path}` 数组。
同一观察内文本 key 唯一，引用身份按 key＋id 唯一；坐标必须为有序非负整数。

选择计划直接遍历输出记录，不先编码为 JSON 再解码。JSON 无引用路径跳过正文选择；SSE 为晚到引用保存摘要和位置。不变正文
使用一个隐式连续区间，实际分歧才创建前后区间。相邻来源及目标位置都连续时合并区间。
不匹配的最终索引、改写区域或未知关联返回结构化拒绝，不自动补正、删除引用或选择其他引擎。

事件方向在 `WireStreamValidation.ConsumeTransformed(before, after, options)` 中共享最终 Spec、引用与续传载体检查。
同一通道／文本 key 的 append 推进位置；snapshot 复用摘要验证此前前缀，只追加新增后缀。
不得用等长替换恢复证明。来源或目标 snapshot 改写旧前缀均拒绝。
区间事务只暂存可变尾段；在 `CommitDelivery` 前不改变已提交摘要与区间。当前批正文仅保留到 Flush，
成功提交后只持有摘要、位置和独立身份；`AbortDelivery` 清除待提交载荷并关闭流，`Release` 释放所有观察。
文本身份及显式区间按实际条目计入 StateItems；身份字节及当前批暂存载荷计入 BufferBytes。
同一未改写载荷共用字符串和摘要，普通 1,024 帧追加不会产生 1,024 份历史区间。

这里证明的是最终 wire 规则前后的文本关系；dev.133 的 semanticRanges 另行证明语义事件与宿主原始投影的关联。
未知 wire 修改范围最多读取当前帧，不完整重建语义流；静态影响路径优化尚未完成。

## 客户端目录操作

`Spec.operations.<name>.models.clientDocument` 与 `Mapping.operations.<name>.encodeModels` 配对声明。
输入使用通用 ModelPage，编码复用现有 Expression、命名程序和只读 operation plan，最终输出必须
满足所声明 JSON 文档。上游发现继续使用 response／decodeModels，不共享成功验证覆盖。

`modelSamples.direction: encode_response` 提供客户端输出样例；省略或 decode_response 表示上游
发现样例。验证及 models 预览执行同一编码边界。宿主只提供目录成员，固定字段和默认数值在
Mapping 中声明；默认 Chat／Gemini 编码器只接受完整本地目录，有分页信息时明确拒绝。
