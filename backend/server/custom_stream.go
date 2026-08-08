package server

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleCustomStreamRequest(
	c *gin.Context,
	group *config.ModelGroupConfig,
	selectedModel config.ModelRef,
	request *relay.CustomProtocolRequestResult,
	targetPlatform relay.Platform,
	inputFormat relay.FormatType,
	startTime time.Time,
	record *usageRecord,
	isLast bool,
) relayOutcome {
	finish := func(result relayOutcome) relayOutcome {
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
		return result
	}
	fail := func(status int, message string, body []byte, retryable bool) relayOutcome {
		if retryable && !isLast {
			return relayOutcome{committed: false, statusCode: status, errMsg: message}
		}
		record.StatusCode = status
		record.Error = message
		if body != nil {
			c.Data(status, "application/json", body)
		} else {
			c.JSON(status, gin.H{"error": message})
		}
		return finish(relayOutcome{committed: true, statusCode: status, errMsg: message})
	}

	if request == nil {
		return fail(http.StatusInternalServerError, "custom protocol request was not rendered", nil, false)
	}
	protocol, ok := relay.GetCustomProtocol(relay.CustomProtocolID(targetPlatform))
	if !ok {
		return fail(http.StatusInternalServerError, fmt.Sprintf("custom protocol %q is not registered", relay.CustomProtocolID(targetPlatform)), nil, false)
	}
	decoder, err := relay.NewCustomProtocolStreamDecoder(protocol)
	if err != nil {
		return fail(http.StatusInternalServerError, fmt.Sprintf("custom protocol stream config is invalid: %v", err), nil, false)
	}
	response, err := s.openaiAdapter.SendCustomProtocolRequest(selectedModel.BaseURL, selectedModel.APIKey, request, true)
	if err != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to forward custom protocol stream: %v", err), nil, true)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return fail(response.StatusCode, string(body), body, shouldRetryStatus(response.StatusCode))
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return fail(http.StatusInternalServerError, "streaming is not supported", nil, false)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	writer := &observingStreamWriter{
		inner:        &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:       record,
		startTime:    startTime,
		observeUsage: true,
	}
	renderer := relay.NewMaheshvaraStreamRenderer(inputFormat, writer, selectedModel.Name)
	reader := relay.NewSSEEventReader(response.Body)
	defer reader.Close()
	var streamErr error
	var terminalEvents []relay.MaheshvaraStreamEvent
	for {
		wireEvent, hasMore, readErr := reader.Read(c.Request.Context(), relay.DefaultSSEIdleTimeout)
		if readErr != nil {
			streamErr = readErr
			break
		}
		if !hasMore {
			break
		}
		events, done, decodeErr := decoder.Decode(wireEvent)
		if decodeErr != nil {
			streamErr = decodeErr
			break
		}
		for index := range events {
			event := events[index]
			if event.Usage != nil {
				updateRecordUsageFromCanonical(record, event.Usage)
			}
			if event.Error != nil || event.Type == relay.CanonicalEventResponseFailed {
				message := "custom protocol stream failed"
				if event.Error != nil && event.Error.Message != "" {
					message = event.Error.Message
				}
				streamErr = fmt.Errorf("%s", message)
				break
			}
			if event.Type == relay.CanonicalEventResponseCompleted {
				terminalEvents = append(terminalEvents, event)
				continue
			}
			if renderErr := renderer.Write(&event); renderErr != nil {
				streamErr = renderErr
				break
			}
		}
		if streamErr != nil || done {
			break
		}
	}
	if streamErr == nil && !decoder.TerminalReceived() {
		streamErr = fmt.Errorf("custom protocol stream ended before a configured terminal value or finish reason")
	}
	if streamErr == nil && !decoder.SawOutput() {
		streamErr = fmt.Errorf("custom protocol stream completed without representable output")
	}
	if streamErr == nil {
		for index := range terminalEvents {
			if renderErr := renderer.Write(&terminalEvents[index]); renderErr != nil {
				streamErr = renderErr
				break
			}
		}
	}
	if streamErr == nil {
		streamErr = renderer.Finish()
	} else {
		_ = renderer.Abort(streamErr)
	}

	applyLocalResponseEstimate(record, writer.responseText.String(), s.config.GetUsageConfig())
	s.adjustTokenUsage(group.ID, getInt(record.Usage.TotalTokens))
	record.StatusCode = http.StatusOK
	if streamErr != nil {
		record.StatusCode = http.StatusBadGateway
		record.Error = streamErr.Error()
	}
	return finish(relayOutcome{committed: true, statusCode: record.StatusCode, errMsg: firstNonEmpty(record.Error, "")})
}
