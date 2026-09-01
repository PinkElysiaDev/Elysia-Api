package server

import (
	"time"

	"github.com/gin-gonic/gin"
)

// failRequest records a failed request and sends a flat error JSON response.
// Used by chatCompletions and other non-Responses endpoints.
func (s *Server) failRequest(c *gin.Context, record *usageRecord, startTime time.Time, statusCode int, errMsg string) {
	s.failRequestKind(c, record, startTime, statusCode, "", errMsg)
}

// failRequestKind 是 failRequest 的带归类版本：kind 填 ErrorKind* 常量
// （conversion/upstream），空串表示未归类。先写响应再落记录——错误体要先进
// 下游捕获器，记录里的第四段「返回下游」才有内容。
func (s *Server) failRequestKind(c *gin.Context, record *usageRecord, startTime time.Time, statusCode int, kind, errMsg string) {
	record.StatusCode = statusCode
	record.Error = errMsg
	record.ErrorKind = kind
	record.EndedAt = time.Now()
	record.DurationMs = time.Since(startTime).Milliseconds()
	c.JSON(statusCode, gin.H{"error": errMsg})
	s.recordUsage(record)
}

// failRequestTyped records a failed request and sends a typed error JSON response
// matching the OpenAI error object format: {"error": {"message": ..., "type": ...}}.
// Used by Responses API endpoints.
func (s *Server) failRequestTyped(c *gin.Context, record *usageRecord, startTime time.Time, statusCode int, errType, errMsg string) {
	s.failRequestTypedKind(c, record, startTime, statusCode, errType, "", errMsg)
}

// failRequestTypedKind 是 failRequestTyped 的带归类版本。
func (s *Server) failRequestTypedKind(c *gin.Context, record *usageRecord, startTime time.Time, statusCode int, errType, kind, errMsg string) {
	record.StatusCode = statusCode
	record.Error = errMsg
	record.ErrorKind = kind
	record.EndedAt = time.Now()
	record.DurationMs = time.Since(startTime).Milliseconds()
	c.JSON(statusCode, gin.H{"error": gin.H{"message": errMsg, "type": errType}})
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
