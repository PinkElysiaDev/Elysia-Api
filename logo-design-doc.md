# Elysia API WebUI Logo 设计文档 (Design Document)

## 1. 视觉概述 (Visual Overview)
该 Logo 灵感来源于《崩坏3》中「爱莉希雅」(Elysia) 的标志性元素。整体呈现为一个带有强烈粉色发光效果（Glow Effect）的纯白水晶花朵/星芒徽章，背景衬托着带有尖刺的破碎星环。设计风格为扁平化、锋利的几何矢量图形，兼具科幻感与奇幻色彩。

## 2. 色彩规范 (Color Palette)
*   **主体填充色 (Main Fill):** 纯白 `#FFFFFF`
*   **发光/光晕色 (Glow/Aura):** 明亮的霓虹粉/洋红色，建议使用 `#FF40FF` 或 `#FF77FF`。
*   **背景 (Background):** 透明 (Transparent)。设计需确保在暗色主题下光晕效果最为显著，同时在亮色主题下白色主体依然清晰。

## 3. 结构拆解 (Structural Components)
为了便于 SVG 代码的实现，整个图形应自下而上分为以下几个逻辑层：

### 3.1 光晕滤镜层 (Glow Filter)
*   **机制:** 使用 SVG 的 `<filter>` 和 `<feGaussianBlur>` 标签。
*   **参数:** 需要较大的模糊半径（如 `stdDeviation="4"` 或更高），以还原原图中宽广且柔和的光晕。
*   **应用:** 将所有纯白色的矢量路径复制一份，填充为粉色，并置于底层应用该模糊滤镜；或者直接在白色图形上应用带有粉色阴影的复合滤镜。

### 3.2 背景层：荆棘星环 (Spiked Orbital Ring)
*   **形态:** 位于花朵后方的同心圆环结构，但并非完整的闭合圆。
*   **特征:**
    *   **破碎与重叠:** 圆环由多段粗细不一的弧线组成，形似交错的新月，部分区域有断点（如右上角和左下角）。
    *   **外侧尖刺:** 圆环的外边缘分布着若干向外突出的锐角三角形尖刺（类似荆棘或齿轮），分布不均匀，大约有 8-10 个。
    *   **内侧细节:** 包含一些较细的内环弧线，增加层次感。

### 3.3 前景层：水晶飞花 (Crystalline Flower)
*   **对称性:** 整体呈四轴（上下左右）放射状十字对称，但在细节上线条充满张力。
*   **核心 (Core):** 正中心是一个极小的、尖端向下的倒三角形。其周围由负空间（镂空）勾勒出一个类似水晶之心的几何轮廓。
*   **内层花瓣 (Inner Petals):** 四片较小的花瓣，分别指向北、南、东、西。线条锐利，带有折角，形似切割过的宝石面。
*   **外层星芒 (Outer Petals):** 四片巨大的、矛状的星芒/花瓣，同样指向四个正方向。它们的基部较宽，包裹着内层花瓣，并向外延伸至极尖锐的端点。
*   **对角元素 (Diagonal Elements):** 在四个主方向之间（东北、西北、东南、西南），点缀着较小的锐角碎片或次级花瓣。

## 4. 美学细节与比例 (Aesthetic Details)
*   **锐利度 (Sharpness):** 所有图形的转角和末端都必须极其锐利（在 SVG 中使用 `stroke-linejoin="miter"` 或直接绘制锐角的 Path），避免任何圆角（Round corners），以凸显“水晶”和“冰冷”的质感。
*   **负空间 (Negative Space):** 图形的美感很大程度上来源于花瓣之间、花瓣与星环之间的镂空缝隙。这些缝隙的宽度应保持视觉上的一致性，使得各部件既分离又构成一个整体。
*   **发光层次:** 原图的发光并非均匀的，靠近中心花朵的区域粉色光晕更浓郁，向外围逐渐消散。

## 5. 给后续代码实现模型的建议 (Implementation Guidelines)
1.  **ViewBox 设置:** 建议使用正方形画布（例如 `viewBox="0 0 100 100"` 或更大），并**必须预留充足的内边距（Padding）**（例如实际图形只占 `20` 到 `80` 的范围），否则外围的粉色光晕会被画布边缘裁剪（Clipping）。
2.  **滤镜代码参考:**
    ```xml
    <defs>
      <filter id="elysia-glow" x="-50%" y="-50%" width="200%" height="200%">
        <!-- 提取图形并染成粉色 -->
        <feFlood flood-color="#FF40FF" result="glowColor" />
        <feComposite in="glowColor" in2="SourceAlpha" operator="in" result="coloredGlow" />
        <!-- 模糊处理 -->
        <feGaussianBlur in="coloredGlow" stdDeviation="4" result="blur" />
        <!-- 增强光晕浓度 -->
        <feComponentTransfer in="blur" result="enhancedGlow">
          <feFuncA type="linear" slope="1.5"/>
        </feComponentTransfer>
        <!-- 将原图叠加在光晕之上 -->
        <feMerge>
          <feMergeNode in="enhancedGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>
    </defs>
    ```
3.  **路径构建 (Path Generation):** 鉴于图形极其复杂，建议后续模型将 SVG 拆分为多个 `<path>` 元素（如 `ring-spikes`, `inner-flower`, `outer-star`），全部设置 `fill="#FFFFFF"`，并将它们包裹在一个 `<g filter="url(#elysia-glow)">` 中。