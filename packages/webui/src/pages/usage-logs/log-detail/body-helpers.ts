// 详情抽屉的展示/导出辅助（正文美化、导出 payload 组装、错误分类标签）。
import type { UsageBody, UsageLogDetail } from '@/lib/types'
import { tryParseJSON } from '@/lib/utils'
import { extractAssetRefs } from './asset-refs'

export function prettyPrintBody(content: string): string {
  if (!content) return ''
  const parsed = tryParseJSON(content)
  if (typeof parsed !== 'string') {
    return JSON.stringify(parsed, null, 2)
  }
  if (content.includes('data:')) {
    const blocks: string[] = []
    for (const rawLine of content.split('\n')) {
      const line = rawLine.trim()
      if (!line.startsWith('data:')) continue
      const payload = line.slice('data:'.length).trim()
      if (!payload || payload === '[DONE]') {
        blocks.push(payload || '')
        continue
      }
      const ev = tryParseJSON(payload)
      blocks.push(typeof ev === 'string' ? ev : JSON.stringify(ev, null, 2))
    }
    if (blocks.length > 0) return blocks.join('\n\n')
  }
  return content
}

/** 把详情重组为带标签的导出结构：总览 + 四段链路（请求体尽量解析为对象）+ 原始记录。 */
export function buildExportPayload(detail: UsageLogDetail) {
  const seg = (b: UsageBody | undefined) => ({
    content: tryParseJSON(b?.content ?? ''),
    truncated: b?.truncated ?? false,
  })
  // 外置媒体引用清单：正文内是占位符，导出时列出可回查的文件路径。
  const assets = extractAssetRefs(
    [detail.incomingBody, detail.outgoingBody, detail.providerResponse, detail.downstreamResponse]
      .map((b) => b?.content ?? '')
      .join('\n'),
  )
  return {
    overview: {
      requestId: detail.requestId,
      api: detail.platform,
      groupName: detail.groupName,
      modelName: detail.modelName,
      conversion: {
        from: detail.sourceFormat || detail.inputFormat || '',
        to: detail.targetFormat || detail.platform || '',
        chain: detail.conversionChain ?? [],
      },
      stream: detail.stream,
      statusCode: detail.statusCode,
      error: detail.error ?? '',
      errorKind: detail.errorKind ?? '',
      retryCount: detail.retryCount,
      retryEvents: detail.retryEvents ?? [],
      firstByteMs: detail.firstByteMs,
      durationMs: detail.durationMs,
      usage: detail.usage,
      usageDetail: detail.usageDetail,
      startedAt: detail.startedAt,
      endedAt: detail.endedAt,
    },
    chain: {
      downstreamRequest: seg(detail.incomingBody),
      backendForward: seg(detail.outgoingBody),
      upstreamResponse: seg(detail.providerResponse),
      downstreamResponse: seg(detail.downstreamResponse),
    },
    // 媒体文件可经 GET /api/admin/usage/assets/<requestId>/<file> 回查。
    assets: assets.map((a: { requestId: string; file: string }) => `${a.requestId}/${a.file}`),
    raw: detail,
  }
}

export function errorKindLabel(kind: string): string {
  switch (kind) {
    case 'conversion':
      return '协议转换失败' // 历史值域,现归入 invalid_request
    case 'invalid_request':
      return '请求无效'
    case 'authentication':
      return '认证失败'
    case 'permission':
      return '无权访问'
    case 'model_not_found':
      return '模型不存在'
    case 'rate_limit':
      return '限流'
    case 'overloaded':
      return '上游过载'
    case 'upstream':
      return '上游失败'
    case 'server':
      return '服务内部错误'
    default:
      return kind
  }
}
