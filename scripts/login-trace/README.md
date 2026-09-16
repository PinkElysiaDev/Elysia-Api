# 登录角色轨迹制作与审校

本目录只用于开发期素材制作。页面使用随项目交付的逐帧轨迹，不安装 Python、OpenCV 或视觉模型，也不调用外部模型 API。

## 复现

在仓库根目录执行：

```powershell
python -m pip install --target .tmp-dev/vision-python -r scripts/login-trace/requirements.txt
python scripts/login-trace/extract.py
node scripts/login-trace/verify.mjs
```

Python 3.12；依赖安装在已忽略的 `.tmp-dev/vision-python`，不修改全局 Python 环境。普通前端构建只执行 Node 校验，不重新运行提取。

快速检查首帧而不覆盖发布资产：

```powershell
python scripts/login-trace/extract.py --limit 1
```

完整执行读取 MP4 解码器提供的呈现时间戳，不通过定时器或解码顺序推算帧位置。源素材固定为 1920 × 1080、60fps、960 帧、16 秒。非单调时间戳、解码失败及尺寸变化会报错。

## 视觉标注与纠错

实际审查轮次、已发现问题及验收边界见 `review-log.md`；自动生成报告不会宣称已经完成手工语义验收。

- `annotations.json` 包含四类语义区域：头发／发饰、面部、手部、服饰／其余装饰。
- `sourceRegions`、`sourceExclusions` 是原始视频像素坐标；`targetRegions` 是 760 × 808 目标线稿像素坐标。
- 区域通过参考帧特征与光流跟踪定位。`trackWith` 让细小附属区域复用可靠的主体运动，不独立追踪背景特征。
- 区域只限定候选范围；实际笔画来自每一帧的暗线中心线和边界边缘。描边不会沿区域多边形生成，也不会沿画面裁切边生成。
- 线段使用跨帧匹配标识，但每帧都有独立点列。闭眼或遮挡造成的消失以该帧不含对应线段表达，不连接到不存在的笔画。
- 纠错可收紧源区域，或向 `corrections` 添加包含 `startFrame`、`endFrame`、`polygon`、`exclude`、`group` 的原图坐标修正。重新提取后必须复查受影响帧与邻帧。
- 审图时重点排除背景晶体、光环和光线，检查发饰尖端、两侧发梢、手指、眨眼和首尾循环。源人物与目标姿态不同，粒子按语义部位映射，不做整体轮廓硬变形。

## 审查输出

完整运行生成 `.tmp-dev/login-trace-review`：

- `overlay.mp4`：原分辨率、原帧率的完整描边叠加回放。
- `index.html`：可直接用本机浏览器打开的审图器，支持 960 帧精确定位、左右键单步、原图／叠加／纯线稿和 100%／200% 局部检查；轨迹内嵌，不需要启动外部服务。读取仓库原始视频，不依赖浏览器是否支持 OpenCV 输出 MP4 的编码。
- `contact-000.jpg` 等：每组 16 帧的连续帧联系表，覆盖全片 960 帧。
- `frame-XXXX.png`、`overlay-XXXX.png`、`lines-XXXX.png`：标记关键帧的原图、叠加图和纯线稿，可放大检查。
- `report.json`：每帧的 PTS、线段／顶点数量、分组覆盖、参考特征跟踪数量及导出折线相对提取骨架的偏差。

**精度口径：** `maxCenterlineDeviationPx` 是导出折线对该帧提取骨架的几何误差，不是相对人工逐像素真值的语义准确度。全片自动检查与关键帧视觉审查不能被描述为“960 帧全部经人工证明误差小于 2px”。2px 是主要清晰结构线的验收目标；遇到误抓、漏线或错位，需要回到标注纠错，不能通过加粗描边掩盖。

标识匹配使用等距笔画采样、局部光流预测与双向形状残差，并进行全局贪心的一对一分配。分裂、合并、遮挡及歧义匹配创建新标识，避免将相邻发丝或服饰线强行视为同一条。只调整追踪而不重提几何时执行 `python scripts/login-trace/retrack.py`；这会刷新发布哈希和审图器，并输出 `tracking-report.json`。其残差阈值不是 2px 语义准确度的替代指标。

只重建审图器可执行 `python scripts/login-trace/preview.py`。审图图像与录像保存在已忽略的 `.tmp-dev`，不会增加网页下载量；制作脚本、校正区域和完整轨迹是正式交付文件。

## 发布资产与格式

完整提取更新以下资产，必须一起提交：

- `packages/webui/public/assets/elysia-character-trace.json`：源／目标／标注校验值、PTS 索引、分组和目标笔画。
- `packages/webui/public/assets/elysia-character-trace.bin.gz`：gzip 压缩的逐帧线段。
- `packages/webui/src/lib/login-media-version.json`：浏览器缓存版本，绑定视频、轨迹及目标图案。

二进制采用小端：每帧 `uint32 pathCount`；每条线段为 `uint32 id`、`uint8 group`、`uint8 closed`、`uint16 pointCount`，之后为有符号 16 位 XY 像素增量。首点相对原点，其余点相对前一点；浏览器解码后归一化。不存在的线段表示不可见。帧偏移量为解压后字节偏移。

同时保存压缩文件和解压内容的 SHA-256。某些静态服务器会为 `.gz` 添加 `Content-Encoding: gzip`，浏览器在 Fetch 返回前已解压；加载器根据文件头区分原始 gzip 与已解码内容，分别校验，避免二次解压或误判版本。

节点校验检查全部素材哈希、960 帧覆盖、单调 PTS、边界、分组及路径标识。更换视频、目标图案或修改标注后，没有重新导出时构建必须失败。

## 前端验证

```powershell
npm.cmd run build:webui
npm.cmd run lint --workspace @root/webui
$env:PLAYWRIGHT_CHANNEL = 'msedge'
npm.cmd run test:e2e --workspace @root/webui -- --workers=1
```

无 Edge 的平台可使用已安装的 Playwright Chromium，不设置 `PLAYWRIGHT_CHANNEL`。端到端测试用模拟的管理端响应与测试 Token，不需要读取真实配置。

过场使用视频呈现时间选取轨迹，使用另一条 4 秒时间线控制粒子与文字。在同一 WebGL2 画布中合成视频、描边和粒子；暂停、后台、资源不可用或渲染失败均不阻塞已验证的登录。开发模式下画布上的 `data-media-time`、`data-elapsed`、`data-paths` 便于检查同步，正式构建不输出这些诊断字段。

帧回调内优先创建不可变 `VideoFrame`，用它自身的 PTS 选轨迹并将同一帧上传 GPU，随后立即释放。主线程长任务可能使回调的旧 `mediaTime` 落后于读取视频时的实际帧；已用 220ms 阻塞复现此情况，不能直接把旧元数据与更新后的视频图像配对。没有 `VideoFrame` 能力时只接受按时到达的视频回调；连续超时直接完成登录，不显示错帧描边。

## 浏览器实景与性能复现

在另一个终端运行 `npm.cmd run dev --workspace @root/webui -- --port 5274 --strictPort` 后：

```powershell
$env:PLAYWRIGHT_CHANNEL = 'msedge'
node scripts/login-trace/capture.mjs
node scripts/login-trace/capture.mjs --measure
```

输出位于 `.tmp-dev/login-browser-review`：明暗桌面与触屏移动端的登录页、四个过场阶段、交接截图及 JSON 报告。移动端用 15 秒开始，验证循环接缝；另覆盖 DPR 1／2。性能单独测量，不在过场中截图，记录实际画布回调频率和间隔。它不等同于物理移动设备的 GPU 帧率保证；真机 60／30fps 仍需在目标设备上复核。默认桌面 1600 粒子、移动端 650 粒子，画布 DPR 上限分别为 2／1.5；合成绘制分别限为 60／30Hz，避免高刷新屏空耗。视频帧回调独立运行，每次合成仍使用最新呈现帧的准确轨迹，不使用低频关键帧插值。
