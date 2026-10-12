package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/gin-gonic/gin"
)

// errorDetailTruncateBytes 是错误详情入库的展示截断上限。
const errorDetailTruncateBytes = 2048

// inputFormatFromPath 按 URL 推导客户端线制。错误出口可能出现在协议解析
// 之前的阶段（鉴权中间件、读请求体），与入口处的格式推导共用本函数。
func inputFormatFromPath(path string) builtin.FormatType {
	switch {
	case strings.HasSuffix(path, "/messages"), strings.HasSuffix(path, "/messages/count_tokens"):
		return builtin.FormatClaude
	case strings.HasPrefix(path, "/v1beta/"):
		return builtin.FormatGemini
	case strings.HasSuffix(path, "/responses"):
		return builtin.FormatResponses
	default:
		return builtin.FormatOpenAI
	}
}

// writeProtocolError 按客户端线制写标准错误体（HTTP 层，SSE 尚未开始时）。
func writeProtocolError(c *gin.Context, format builtin.FormatType, mErr *builtin.GatewayError) {
	status, body := builtin.ProtocolErrorBody(format, mErr)
	c.Data(status, contentTypeJSON, body)
}

// setUsageError 区分调用方取消与上游失败。已成功结束的调用不经过此入口。
func setUsageError(record *usageRecord, ctx context.Context, err error) {
	record.Error = truncateForDisplay(err.Error(), errorDetailTruncateBytes)
	record.ErrorKind = ErrorKindUpstream
	if record.StatusCode > 0 && record.StatusCode < 400 {
		record.StatusCode = http.StatusBadGateway
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		record.StatusCode = statusClientClosedRequest
		record.ErrorKind = ErrorKindClientCanceled
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		record.StatusCode = http.StatusGatewayTimeout
	}
}

// waitForRetryOrCancel 等待重试间隔；客户端在等待期断开时返回 false
// （调用方负责落库并终止，见 abortRetryOnClientCancel）。
func waitForRetryOrCancel(c *gin.Context, retryIntervalMs int) bool {
	select {
	case <-c.Request.Context().Done():
		return false
	case <-time.After(time.Duration(retryIntervalMs) * time.Millisecond):
		return true
	}
}

// usageDayKey 把时刻归一为日配额的日期键(acquire/adjust 的跨日守卫共用)。
func usageDayKey(t time.Time) string {
	return t.Format("2006-01-02")
}
