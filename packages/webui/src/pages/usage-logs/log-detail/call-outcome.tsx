import { SheetSectionTitle } from '@/components/ui/sheet'
import type { UsageLogDetail } from '@/lib/types'

const deliveryPhases: Record<string, string> = {
  response: '响应交付',
  response_write: '写入响应',
  response_flush: '刷新响应',
  response_finalize: '结束响应',
}

const deliveryCauses: Record<string, string> = {
  downstream_disconnected: '下游连接断开',
  server_shutdown: '服务关闭',
  deadline: '交付超时',
  downstream_transport: '下游传输故障',
  request_canceled: '请求取消（来源未明）',
  server_panic: '服务处理异常',
}

function describeDeliveryValue(value: string, labels: Record<string, string>): string {
  return Object.prototype.hasOwnProperty.call(labels, value) ? `${labels[value]}（${value}）` : value
}

/** 分开呈现调用结果与服务端交付观察，旧日志不推断缺失的 HTTP 状态。 */
export function CallOutcome({ detail }: { detail: UsageLogDetail }) {
  const delivery = detail.delivery
  return (
    <section className="mb-5" aria-label="调用与交付结果">
      <SheetSectionTitle>调用与交付结果</SheetSectionTitle>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-[7px] text-xs">
        <dt className="text-muted-foreground">上游 HTTP</dt>
        <dd className="tnum" data-testid="upstream-http-status">{detail.upstreamHttpStatus || '未记录'}</dd>
        <dt className="text-muted-foreground">逻辑结果</dt>
        <dd className="tnum" data-testid="logical-status">{detail.statusCode}</dd>
        <dt className="text-muted-foreground">下游 HTTP</dt>
        <dd className="tnum" data-testid="downstream-http-status">{delivery?.httpStatus || '未记录'}</dd>
        <dt className="text-muted-foreground">下游交付</dt>
        <dd data-testid="delivery-outcome" className={delivery?.outcome === 'failed' ? 'text-ember' : ''}>
          {delivery ? (delivery.outcome === 'completed' ? '服务端交付完成' : '交付失败') : '未记录交付结果'}
        </dd>
        {delivery?.phase && <>
          <dt className="text-muted-foreground">交付阶段</dt>
          <dd className="min-w-0 break-all">{describeDeliveryValue(delivery.phase, deliveryPhases)}</dd>
        </>}
        {delivery?.cause && <>
          <dt className="text-muted-foreground">交付原因</dt>
          <dd className="min-w-0 break-all">{describeDeliveryValue(delivery.cause, deliveryCauses)}</dd>
        </>}
        {delivery?.error && <>
          <dt className="text-muted-foreground">交付诊断</dt>
          <dd className="min-w-0 break-all text-ember">{delivery.error}</dd>
        </>}
        {detail.transportFailure && <>
          <dt className="text-muted-foreground">传输诊断</dt>
          <dd className="min-w-0 break-all">{detail.transportFailure.kind} / {detail.transportFailure.phase}</dd>
        </>}
      </dl>
      <p className="mt-2 text-2xs text-muted-foreground">下游 HTTP 是服务端选定的状态；交付完成仅表示服务端写入和结束操作成功。</p>
    </section>
  )
}
