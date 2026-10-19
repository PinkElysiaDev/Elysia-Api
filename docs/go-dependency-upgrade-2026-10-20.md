# Go 依赖升级与 API 审查（2026-10-20）

工具链：Go 1.27.2。范围：`backend/go.mod` 的直接与间接依赖。

## 版本

| 直接依赖 | 升级前 | 升级后 |
| --- | --- | --- |
| `github.com/coder/websocket` | v1.8.14 | v1.8.15 |
| `github.com/gin-gonic/gin` | v1.10.0 | v1.12.0 |
| `github.com/mattn/go-isatty` | v0.0.20 | v0.0.24 |
| `github.com/rs/zerolog` | v1.34.0 | v1.35.1 |
| `golang.org/x/net` | v0.25.0 | v0.61.0 |
| `golang.org/x/sync` | v0.20.0 | v0.24.0 |
| `modernc.org/sqlite` | v1.52.0 | v1.60.1 |

通过 `go get -u -t ./...` 更新依赖，再执行 `go mod tidy` 清理失去引用的模块与校验和。对整理后的 42 个显式模块要求（7 个直接、35 个间接）逐个查询更新，唯一例外是 `modernc.org/libc`：

- SQLite v1.60.1 明确要求使用其 `go.mod` 指定的 **libc v1.77.1**，因此未采用独立发布的 v1.79.0。
- 这个配套约束已注释在 `backend/go.mod`，升级 SQLite 时应一并复核。
- `go list -m all` 还会显示依赖自身声明的工具、测试及未被本项目导入的包所需模块；没有为这些未使用模块增加额外的顶层版本覆盖。

## 直接依赖 API 与行为审查

### Gin

核对 [v1.12.0 的变更记录](https://github.com/gin-gonic/gin/blob/v1.12.0/CHANGELOG.md)、Context getter、响应写入及 Hijack 实现。

- 使用 `GetStringSlice` 和 `slices.Contains` 简化模型组权限读取，替换手动存在性检查、类型断言和循环；缺失、错误类型及空列表仍保持原来的语义。该 getter 是已有 API，升级后的实现统一使用泛型类型读取。
- v1.12.0 的 `Data` 渲染器会设置非空响应体的 `Content-Length`。项目的调用点均写入完整的缓冲响应；流式路径通过 writer 写入，不依赖 `Data` 分片输出。
- Hijack 现在拒绝已经写出响应体的连接，但允许只写过响应头的连接。项目 WebSocket 握手在写响应体之前完成，握手 writer 继续转发 `WriteHeaderNow`。
- 新增的错误 getter、Context.Delete、BSON、表单绑定和 HTTP/3 能力没有对应的项目手写实现需要替换。现有路由、JSON 绑定及中间件调用未发现签名不兼容。

### zerolog

核对 [v1.34.0 至 v1.35.1 的差异](https://github.com/rs/zerolog/compare/v1.34.0...v1.35.1)及新增 `NewSlogHandler` 实现。

- `logging.Logger.Standard` 改用 `slog.NewLogLogger(zerolog.NewSlogHandler(...), slog.LevelError)`，删除 `standardWriter`。
- 保留组件字段、错误级别、统一时间戳和控制台/持久化双阈值处理。消息末尾换行由标准库桥接处理；不再额外裁剪消息两侧空白。
- 新版废弃的 `Arr`、`Dict`、`DurationFieldInteger` 在本项目没有调用点。项目也没有可被新 `Objects` 等接口替代的对象数组编码器。

### SQLite

核对 [v1.60.1 的变更记录](https://github.com/modernc-org/sqlite/blob/v1.60.1/CHANGELOG.md)、DSN 参数处理和 `stmt.exec` 实现。

- 使用 v1.55.0 新增的 `_busy_timeout`、`_foreign_keys`、`_synchronous`、`_query_only` 参数表达连接配置；这些参数由驱动校验。
- 抽取一个共享的文件 URI/DSN 构造函数，复用已有日志数据库的路径转义逻辑。业务数据库也在每次物理连接打开时配置超时、外键、同步模式和缓存，避免取消查询后连接被替换而丢失设置。
- WAL、auto-vacuum 等数据库级设置仍在 schema 检查之后初始化，避免重复配置。
- 当前驱动的 `ExecContext` 已完整消费 `SQLITE_ROW`，因此 `ReclaimLogPages` 直接执行 `incremental_vacuum`，删除手工 `QueryContext` / `Next` 排空逻辑。这项驱动修复早于本次升级，但原来的兼容实现仍残留在项目中。
- 新版对 VFS 生命周期、虚拟表注册、UDF 参数所有权和底层 `lib` 常量的变化没有本项目调用点；也没有需要用 `NewConnector` 替换的全局驱动注册封装。

### WebSocket、终端检测和 Go 扩展库

- [websocket v1.8.15](https://github.com/coder/websocket/compare/v1.8.14...v1.8.15)：主要变化是压缩消息帧及读写超时注册的优化，当前使用的 Accept/Dial/Read/Write/Ping/Close API 不变。核对后确认 `CloseNow` 仍可能等待已有的优雅关闭，因此保留项目中先关闭底层 transport 的逻辑及其回归测试。
- [go-isatty v0.0.24](https://github.com/mattn/go-isatty/compare/v0.0.20...v0.0.24)：`IsTerminal` 签名不变，包含 ioctl 和平台检测修复；调用方继续使用该 API。
- x/net：本项目直接使用的 `http/httpguts` 两个源码文件在 v0.25.0 和 v0.61.0 之间相同，`ValidHeaderFieldName` 无需迁移。
- [x/sync v0.24.0](https://github.com/golang/sync/compare/v0.20.0...v0.24.0)：`errgroup.WithContext`、`SetLimit`、`Go` 和 `Wait` 契约不变；删除工具并行循环中 Go 1.22 之后已不需要的变量重新捕获。新版 semaphore 对负权重/容量的检查不涉及本项目调用点。

## 实现与测试去重审查

- 日志桥接只有官方适配器这一条实现。
- SQLite 文件 URI 转义和公共连接参数只有一个构造入口；业务与日志库保留各自需要的同步级别、缓存大小和读写权限。
- 原有日志阈值测试增加标准日志桥接的级别、组件、消息及唯一时间戳断言。
- 原有业务数据库初始化测试覆盖带空格和 `#` 的路径，以及连接被替换后的配置；替换原先仅检查首次连接超时的断言。
- 原有日志读写隔离、回收页数、取消、权限和 WebSocket 强制关闭测试仍覆盖不同业务行为，审查未发现由本次修改产生的重复测试。不新增单独的适配器或 DSN 实现镜像测试。

## 检查状态

- `go build ./...`：通过。
- `go vet ./...`：通过，包含测试源码的静态分析。
- `git diff --check`：通过。
- 按要求暂未执行测试；运行时回归结果待后续验证。
