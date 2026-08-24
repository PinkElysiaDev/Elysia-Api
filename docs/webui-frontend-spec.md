# WebUI Frontend Spec

WebUI 负责模型网关的管理与观测，只通过 `/api/admin/*` 与后端通信。

## Source of truth

- 主题变量与全局样式位于 `packages/webui/src/index.css`；Tailwind 映射位于
  `packages/webui/tailwind.config.js`。
- 界面使用 shadcn/Radix 语义组件、Tailwind 工具类和 class-based dark mode。
- Fraunces 500/600 通过 `@fontsource/fraunces` 自托管；其余字体使用系统栈，产物不得依赖外链资源。
- WebUI 图片放在 `packages/webui/public`。已有 `favicon.ico`、`favicon.png`、`logo.png`
  保持原路径；其中 `logo.png` 同时被 macOS 打包流程使用。界面品牌图使用
  `logo-color.png`，登录页与总览水印使用 `role-mask.png`。
- 构建链为 `npm run build:webui`，完整发行构建由 `npm run build` 将前端产物复制到
  `backend/webui/dist` 并嵌入 Go 二进制，在 `/ui/` 提供服务。

## Visual and layout

- 设计语言为 porcelain / plum-ink：瓷白或梅紫背景、rose 主色、jade 成功色、ember 危险色。
- `>= 761px` 使用 228px 常驻侧栏；`<= 760px` 使用带遮罩的移动抽屉，支持关闭按钮、遮罩、Esc
  和导航跳转关闭。主栏 padding 桌面 40px、窄屏 22px。背景光晕铺满视口。
- 字号 11 / 12 / 13 / 14 / 16 / 19 / 30px。KPI 大数字用 display 30px。
- 后台页主栏软顶 `max-width: 1600px`，超出居中。运行配置表单在柱内 `max-w-xl`。登录无侧栏，舞台同样 1600px 居中。
- 立绘钉在总览/登录内容容器右上，与 KPI 或登录卡重叠；桌面 520px、窄屏 240px。
- 交互元素使用语义 HTML 或 Radix primitive，保留键盘操作、可见焦点和必要的 ARIA label。

## Usage API contracts

Usage 查询共用 `from`、`to`、`keyName`、`groupName`、`modelName` 等筛选；多选值通过重复参数发送。
`from` / `to` 为半开区间 `[from, to)`（`started_ms >= from` 且 `started_ms < to`）。

- `GET /api/admin/usage/stats`：返回请求、成功/失败、token 与耗时汇总。`cacheHitRate` 为 `[0,1]`。
- `GET /api/admin/usage/trend?utcOffsetMinutes=<local-minus-UTC>`：按固定 UTC offset 的本地日返回
  `{date, requests, tokens}[]`。东八区传 `480`。
- `GET /api/admin/usage/by-model`：返回
  `{model, requests, failed, tokens}[]`，按请求数降序、模型名升序。
- `GET /api/admin/usage/logs`：分页明细；`status=success|failed` 提供语义状态筛选，
  `statusCode=<code>` 提供精确筛选，同时出现时精确状态码优先。
- 成功定义为 `200 <= statusCode < 400`；其余状态均为失败。

## Pages

1. `/login`：保存 panel access token，401 时清除 token 并返回登录页。
2. `/overview`：今日/昨日 KPI、上一完整 60 秒窗口的 rpm/tpm、7/30 日趋势、热门模型、模型源健康、最近失败；
   运行状态在页头健康胶囊（10–15s 轮询，区分 loading/error/data）。立绘水印叠在 KPI/趋势右侧。
3. `/sources`：模型源增删改、启停、刷新，以及源内模型的筛选和批量操作。
4. `/groups`：模型组增删改、启停、策略筛选和成员管理；组名即客户端可见模型 ID。
5. `/tokens`：访问令牌增删改、启停、允许组限制、明文 reveal 与复制。
6. `/usage`：汇总 KPI、成功/失败与 token 分布、按模型图表和明细。
7. `/usage-logs`：服务端分页与筛选、当前页摘要、JSON 导出、调用详情 Sheet 和 Usage 重置。
8. `/logs`：系统日志分页、级别筛选和结构化字段详情。
9. `/runtime`：运行配置编辑、热重载及需要重启的字段提示。
10. `/diagnostics`：健康与内存指标、pprof 开关和分析入口。

## UX requirements

- 删除 source/group/token 与重置 Usage 必须二次确认。
- Secret 保存后立即清空明文输入；列表只展示脱敏值。
- 数据区必须区分 loading、empty 和 error，局部接口失败不得伪装成空数据。
- 时间戳按浏览器本地时区展示；数字列使用等宽数字。
- 桌面、中间宽度和移动断点都必须保持可读、可滚动和可操作。
