import type { ConversionPhase } from '@/lib/conversion-policy'

export const field = 'rounded-md border bg-card p-2 text-sm w-full'
export const pretty = (v: unknown) => JSON.stringify(v, null, 2)
export const phases: Record<ConversionPhase, string> = {
  ingress: '入口规范化',
  request: '候选请求',
  response: '响应',
  event: '流式事件',
  wire: '编码后',
}
