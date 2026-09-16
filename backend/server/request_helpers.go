package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// inputFormatFromPath 按 URL 推导客户端线制。错误出口可能出现在协议解析
// 之前的阶段（鉴权中间件、读请求体），与入口处的格式推导共用本函数。
func inputFormatFromPath(path string) relay.FormatType {
	switch {
	case strings.HasSuffix(path, "/messages"):
		return relay.FormatClaude
	case strings.HasPrefix(path, "/v1beta/"):
		return relay.FormatGemini
	case strings.HasSuffix(path, "/responses"):
		return relay.FormatResponses
	default:
		return relay.FormatOpenAI
	}
}

// writeProtocolError 按客户端线制写标准错误体（HTTP 层，SSE 尚未开始时）。
func writeProtocolError(c *gin.Context, format relay.FormatType, mErr *relay.MaheshvaraError) {
	status, body := relay.ProtocolErrorBody(format, mErr)
	c.Data(status, contentTypeJSON, body)
}

// failRequestError 是转发路径的统一失败出口：按客户端线制渲染标准错误体
// 并落 usage 记录（class 即 errorKind）。先写响应再落记录——错误体要先进
// 下游捕获器，记录里的第四段「返回下游」才有内容。
func (s *Server) failRequestError(c *gin.Context, record *usageRecord, startTime time.Time, format relay.FormatType, mErr *relay.MaheshvaraError) {
	status, body := relay.ProtocolErrorBody(format, mErr)
	record.StatusCode = status
	record.Error = mErr.Message
	record.ErrorKind = string(mErr.Class.OrDefault())
	record.EndedAt = time.Now()
	record.DurationMs = time.Since(startTime).Milliseconds()
	c.Data(status, contentTypeJSON, body)
	s.recordUsage(record)
}

// abortRetryOnClientCancel 非阻塞检查客户端取消：已断开时补全记录
// （499 + 错误/耗时）并落库，返回 true。重试等待期与每轮循环顶部共用——
// interval=0 时没有等待期可拦截，断连后仍会向剩余候选逐个扇出。
func (s *Server) abortRetryOnClientCancel(c *gin.Context, record *usageRecord, startTime time.Time) bool {
	select {
	case <-c.Request.Context().Done():
		record.StatusCode = statusClientClosedRequest
		record.Error = "client canceled during retry wait"
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		return true
	default:
		return false
	}
}

// commitLastAttemptFailure 提交末次尝试的失败：补全记录并按客户端线制写
// 标准错误体。先写响应再落记录（错误体先进下游捕获器，第四段才有内容）。
// 调用方负责置位 committed。
func (s *Server) commitLastAttemptFailure(c *gin.Context, record *usageRecord, startTime time.Time, format relay.FormatType, mErr *relay.MaheshvaraError) {
	s.failRequestError(c, record, startTime, format, mErr)
}

// upstreamErrorStatus 从错误中提取上游真实状态码（UpstreamStatusError），
// 提取不到时用 fallback。流式与非流式的失败路径共用。
func upstreamErrorStatus(err error, fallback int) int {
	var statusErr *relay.UpstreamStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode > 0 {
		return statusErr.StatusCode
	}
	return fallback
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

// writeSSEHeaders 写出 SSE 响应头。调用方负责时机：应在确认上游建连成功、
// 即将写出响应体之前调用（头一旦发出就无法再改 HTTP 状态码，也就无法重试）。
// 不手动设 Transfer-Encoding：Go 的 http.Server 对无 Content-Length 的流式
// 响应自动 chunked，手动设是冗余且在错误路径易制造 TE+Content-Length 冲突。
func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}
