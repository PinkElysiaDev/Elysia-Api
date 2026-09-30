# Elysia API WebUI

[文档首页](../../docs/README.md) · **简体中文** · [English](README.en.md)

React 18 + TypeScript 管理界面，使用 Vite、Tailwind、Radix、SWR、Recharts 和 HashRouter。生产路径为 `/ui/`，无需独立前端服务。主题支持浅色、深色和系统设置。

## 功能

管理模型源、源内模型、模型组、推理令牌、自定义协议及运行配置；查看用量、调用日志、系统事件和诊断；通过内置助手执行受权限控制的管理任务。

调用日志详情可展示入站、转发、上游返回、下游返回四段正文；是否保存取决于配置，默认不保存正文。按需查看令牌明文需要管理员权限。

## 前提

使用 Node.js 22、npm 和 Go 1.25。以下命令均在仓库根目录执行。首次安装运行 `npm install`；完整环境说明见[开发指南](../../docs/development.md)。

## 本地开发

1. 在一个终端启动后端：

   ```sh
   cd backend
   go run . --config ./config.json
   ```

2. 在仓库根目录的另一个终端启动前端：

   ```sh
   npm run dev --workspace @root/webui
   ```

3. 打开 `http://127.0.0.1:5273/`，用后端配置中的 `panelAccessToken` 登录。

Vite 默认监听回环地址。后端地址可通过 `ELYSIA_DEV_PROXY` 覆盖；`ELYSIA_DEV_HOST` 设置前端监听地址。非默认后端示例：

```sh
ELYSIA_DEV_PROXY=http://127.0.0.1:8799 npm run dev --workspace @root/webui
```

代理包括 `/api`、`/v1`（也匹配 `/v1beta`）、`/health`、`/mcp`、`/a2a`、`/.well-known/agent-card.json` 和 `/debug`。

## 构建

```sh
npm run build:webui
```

产物为 `packages/webui/dist/`。该命令先检查登录素材，再执行 TypeScript 与 Vite 构建。`npm run build` 还会复制到 `backend/webui/dist/` 并嵌入发行二进制。

独立使用前端产物时，将后端 `webuiDir` 指向该目录；相对路径按配置文件目录解析。后端在 `/ui/` 提供静态资源，没有 history fallback，因此保留 HashRouter。

## 实现与交互

| 内容 | 来源 |
| --- | --- |
| 主题与全局样式 | [index.css](src/index.css) |
| Tailwind 映射与断点 | [tailwind.config.js](tailwind.config.js) |
| HashRouter 路由 | [App.tsx](src/App.tsx) |
| 布局与导航 | [app-layout.tsx](src/components/app-layout.tsx) |
| 管理接口与类型 | [管理 API](../../docs/webui-api.md)、[数据模型](../../docs/webui-data-model.md) |

- 复用现有主题变量和组件。交互使用语义 HTML 或 Radix primitive，保留可见键盘焦点和必要的无障碍标签。
- 移动抽屉支持关闭按钮、遮罩、Esc 和导航跳转关闭；关闭后恢复焦点，切回桌面解除滚动锁定。筛选支持方向键、Enter、Esc、Tab，清空搜索后保留输入焦点。
- 数据区区分加载、空数据和错误；接口失败不能显示为正常空列表。日志详情区分未捕获、截断和媒体外置。时间按浏览器本地时区展示。
- 删除模型源、组、令牌及重置 Usage 需要二次确认。密钥输入保存后清空，普通列表只展示脱敏值。
- 保留减少动态效果支持，以及登录素材加载失败时的登录回退。静态素材位于 `public/`；`logo.png` 同时用于 macOS 打包，修改路径时同步检查打包脚本。

## 验证

```sh
npm run lint --workspace @root/webui
npm run build:webui
npm exec --workspace @root/webui playwright install chromium
npm run test:e2e --workspace @root/webui
```

浏览器测试在 `127.0.0.1:5274` 启动前端，使用模拟响应和测试令牌，不需要真实后端。覆盖分页、清理后页码、错误重试、导航焦点、筛选键盘操作、Agent 流和深浅主题窄屏。已有 Chrome 可使用 `PLAYWRIGHT_CHANNEL=chrome npm run test:e2e --workspace @root/webui`。

修改交互后，检查键盘操作、窄屏、深浅主题和减少动态效果；素材修改见[轨迹制作](../../scripts/login-trace/README.md)。

## 故障处理

| 现象 | 检查 |
| --- | --- |
| 登录请求连接失败 | 后端是否运行，代理地址是否匹配实际端口 |
| 401 | 使用面板令牌，确认配置路径；推理令牌不能管理面板 |
| 页面空白或资源 404 | 使用 `/ui/` 访问生产构建，确认 `webuiDir` 及产物完整 |
| 构建提示素材哈希不一致 | 同步导出的轨迹与版本文件；不要跳过素材校验 |
| 浏览器测试无法启动 | 安装 Playwright Chromium，或选择已安装的浏览器 channel；检查 5274 端口 |
