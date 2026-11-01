# 三部分架构剩余功能闭合记录

执行基线为 `refactor/protocol-dsl`、`a20f63ce / dev.111`。本轮只做本地提交；不调用真实供应商。新版本不承担旧格式、旧绑定及旧任务的升级兼容，使用隔离新数据库验收，不删除现有数据库或日志。包／DSL／核心／策略版本继续为 4／2／2／2。

## 批次 0：基线和配置复用

- 独立性能参考 A 仍为 `b2844846 / dev.101`。重新读取其六目标二进制并校验 SHA256，六项全部匹配已有清单；没有重新构建或替换 A。
- dev.111 的源码归档、四对配置、已有检查原文和当前 Windows amd64 可运行构建存于忽略目录 `.tmp-dev/protocol-three-part-closure-2026-11-02/dev111/`，清单记录文件哈希。它是恢复点，不是功能完整版本 B。
- Responses 提取五组确实重复两次的表达式：文本增量事件、失败事件、缓存计数读取、有效存储决定和工具结果扩展。复用已有命名程序，不改变样例、限额或编译器版本。
- `closure-baseline-responses`：Responses 正式激活样例及相关包测试通过，10.108 秒。检查实际使用 GOMAXPROCS=2、GOGC=20、GOMEMLIMIT=384MiB、PGO 关闭，仅作功能检查，不作性能数据。

## 剩余实施顺序

1. 外部校验／metadata 白名单退出共享核心；请求投影和模型规则显式声明。
2. 缓存、metadata 和错误目标处置。
3. JSON 历史布局、多根输出、字符位置。
4. 真实流布局、增量与 Native 衔接。
5. 所有入口及资源所有权闭合，归档功能完整版本 B。
6. 三版本配对性能、profile 和定向优化。
7. 四预置切换、旧生产链删除及最终验收。

当前生产预置仍加载旧定义。正式性能、最终前端／页面／审计／六目标验收未完成，不能由阶段测试通过推断总目标完成或性能无回退。

## 批次 1A：显式目标程序（dev.112）

四份 Mapping 的 conversionDefaults 通过 use 绑定 model_policy；有请求字段处置的三个包同时绑定 request_projection。规则 ID、顺序和启用状态归声明所有。删除 DefaultConversionPolicy 中对这两个程序的自动安装；来源模型规范化只接入声明的模型规则。包 schema 和两源编辑器帮助同步。

编译顺序先绑定请求处置，再编译默认规则。原有缓存／历史夹具显式声明原先依赖自动注入的规则，未放宽 Native、严格模式或禁用后的编码拒绝。新增一个回归验证省略声明不运行、声明身份和顺序、禁用、错误引用及混用拒绝。

| 检查 | 结果 |
|---|---|
| closure-declared-defaults-final | protocol 全量通过，7.097 秒 |
| closure-declared-defaults-builtin | builtin 全量及四包正式证据通过，35.861 秒 |
| closure-declared-defaults-live | 32 场景网关矩阵、工具结果、storage 及最终 wire 冲突拒绝通过，24.219 秒 |
| closure-declared-defaults-race | 显式默认、缓存／Native、历史和两源编译相关 race 通过，无竞争报告 |
| closure-declared-defaults-vet | protocol 全部 vet 通过 |
| closure-declared-defaults-staticcheck | 固定 v0.8.2 仍有 24 条存量诊断，无新增或消失；退出 1 |
| closure-declared-defaults-types / lint | 前端类型检查和修改文件 lint 通过 |

检查日志在原 checks 目录。测试初次暴露一个仍依赖自动规则的缓存夹具，补齐声明后核心全量通过；未改生产兜底。该切片不代表批次 1 全部完成，metadata 白名单、共享请求外部校验和聚合证明仍待迁移；没有正式性能结论。

## 批次 1B：有限值映射的核心位置（dev.113）

有限值映射不再把核心路径固定为 reasoning.historyRetention，或把规范值固定为 all。编译按显式核心字段、字段类型、独立余项所有者和完整分区／重建证明检查；来源、Native、资源和客户端意图不在此原语的写入范围。仍不支持任意对象替换和数组布局。类型反射仅在配置编译阶段执行，运行期沿用原映射及所有权检查。

新增定向场景以 parameters.history_mode 映射 full／complete 为规范值、未知 future 留在独立余项位置，验证双向转换确实不依赖旧路径；原 historyRetention 的配置、严格拒绝及 Native 测试保持通过。聚合对象布局和请求字段处置仍有后续通用化工作，不能将该切片称为整个聚合原语完成。

- closure-finite-values-all：protocol 全量 6.833 秒、builtin 全量 34.827 秒，四包正式证据通过。
- closure-finite-values-final：最后的标量 null／独立所有者检查后，有限值与历史定向复核通过。
- closure-finite-values-race：有限值与历史相关 race 通过，无竞争报告。
- closure-finite-values-vet：protocol 全部 vet 通过。

未采集正式性能，不把编译期结构判断外推成运行期性能收益。
