import useSWR from 'swr'
import { api } from '@/lib/api'
import type { CustomProtocolSchema, MaheshvaraFieldSpec } from '@/lib/types'

/** 字段目录 hook：后端 schema 端点是目录的单一事实来源（校验同源）。 */
export function useCustomProtocolSchema() {
  const { data } = useSWR('custom-protocol-schema', () => api.customProtocolSchema(), {
    revalidateOnFocus: false,
    dedupingInterval: 5 * 60 * 1000,
  })
  return data
}

export const AUTH_MODES = [
  { value: 'bearer', label: 'Bearer（默认）' },
  { value: 'header', label: '自定义 Header' },
  { value: 'query', label: 'Query 参数' },
  { value: 'none', label: '不注入' },
] as const

export function protocolTypeLabel(type: string | undefined): string {
  switch (type) {
    case '':
    case undefined:
    case 'llm':
      return 'LLM'
    case 'reranker':
      return 'Reranker'
    case 'embedding':
      return 'Embedding'
    default:
      return type.startsWith('x-') ? type.slice(2) : type
  }
}

/** 响应字段可选项：目录 + 常用 metadata 子键示例（后端允许 metadata.<key>）。 */
export function responseFieldOptions(schema: CustomProtocolSchema | undefined): MaheshvaraFieldSpec[] {
  return schema?.responseFields ?? []
}
