/**
 * z-index 常量（tailwind 类名形式）。只登记被多处消费的层级；ui 组件内部
 * 的遮罩/浮层层级见各组件（dialog 74/75、sheet 60/70、toast 90）。
 */
export const Z_INDEX = {
  multiSelectPanel: "z-50",
  arrivalEcho: "z-[45]",
  skipLink: "z-[100]",
} as const;

/** Recharts tooltip 的 inline zIndex(数字形态,JSX style 用):高于内容、
 * 低于 Sheet/Dialog,不遮挡抽屉与弹窗。 */
export const CHART_TOOLTIP_Z = 50;
