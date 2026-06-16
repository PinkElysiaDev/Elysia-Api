package server

import (
	"time"

	"github.com/gin-gonic/gin"
)

// failRequest records a failed request and sends a flat error JSON response.
// Used by chatCompletions and other non-Responses endpoints.
func (s *Server) failRequest(c *gin.Context, record *usageRecord, startTime time.Time, statusCode int, errMsg string) {
	record.StatusCode = statusCode
	record.Error = errMsg
	record.EndedAt = time.Now()
	record.DurationMs = time.Since(startTime).Milliseconds()
	s.recordUsage(record)
	c.JSON(statusCode, gin.H{"error": errMsg})
}

// failRequestTyped records a failed request and sends a typed error JSON response
// matching the OpenAI error object format: {"error": {"message": ..., "type": ...}}.
// Used by Responses API endpoints.
func (s *Server) failRequestTyped(c *gin.Context, record *usageRecord, startTime time.Time, statusCode int, errType, errMsg string) {
	record.StatusCode = statusCode
	record.Error = errMsg
	record.EndedAt = time.Now()
	record.DurationMs = time.Since(startTime).Milliseconds()
	s.recordUsage(record)
	c.JSON(statusCode, gin.H{"error": gin.H{"message": errMsg, "type": errType}})
}
