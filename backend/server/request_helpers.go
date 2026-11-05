package server

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// errorDetailTruncateBytes 是错误详情入库的展示截断上限。
const errorDetailTruncateBytes = 2048

// setUsageError 区分调用方取消与上游失败。已成功结束的调用不经过此入口。
func setUsageError(record *usageRecord, ctx context.Context, err error) {
	record.Error = truncateForDisplay(err.Error(), errorDetailTruncateBytes)
	record.ErrorKind = ErrorKindUpstream
	recordFailureEvidence(record, err)
	if record.StatusCode > 0 && record.StatusCode < 400 {
		record.StatusCode = http.StatusBadGateway
	}
	if localStatus := localResourceStatus(record, err); localStatus != 0 {
		record.StatusCode = localStatus
	}
	if failure := (*upstreamHTTPFailure)(nil); errors.As(err, &failure) {
		// A timeout while decoding an already rejected HTTP response is
		// secondary evidence; retain the observed upstream status.
		record.StatusCode = failure.status
	}
	classifyRequestInterruption(record, ctx, err)
}

// waitForRetryOrCancel 等待重试间隔；客户端在等待期断开时返回 false
// （调用方负责落库并终止，见 abortRetryOnClientCancel）。
func waitForRetryOrCancel(ctx context.Context, retryIntervalMs int) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Duration(retryIntervalMs) * time.Millisecond):
		return true
	}
}

// usageDayKey 把时刻归一为日配额的日期键(acquire/adjust 的跨日守卫共用)。
func usageDayKey(t time.Time) string {
	return t.Format("2006-01-02")
}
