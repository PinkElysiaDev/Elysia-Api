import type { ConversionPhase } from '@/lib/conversion-policy'

export const field =
  'h-[34px] w-full min-w-0 rounded-md border border-input bg-card px-3 text-sm text-foreground transition-[border-color,box-shadow] duration-200 focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash disabled:cursor-not-allowed disabled:opacity-50'

export const phases: Record<ConversionPhase, string> = {
  ingress: '入口规范化',
  request: '候选请求',
  response: '响应',
  event: '流式事件',
  source_event: '上游流帧（解码前）',
  wire: '编码后',
}
