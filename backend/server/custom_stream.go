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
		outcome := relayFailOutcome(record, isLast, retryable, status, message, func() {
			if body != nil {
				writeUpstreamError(c, inputFormat, targetPlatform, status, body, contentTypeJSON)
				return
			}
			writeProtocolError(c, inputFormat, &relay.MaheshvaraError{Class: relay.ErrorClassUpstream, Status: status, Message: message})
		})
		if outcome.committed {
			return finish(outcome)
		}
		return outcome
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
	response, err := s.openaiAdapter.SendCustomProtocolRequest(c.Request.Context(), selectedModel.BaseURL, selectedModel.APIKey, request, true)
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
	writeSSEHeaders(c.Writer)

	writer := &observingStreamWriter{
		inner:     &ginStreamWriter{writer: c.Writer, flusher: flusher},
		record:    record,
		startTime: startTime,
	}
	// 事件捕获/usage 提取统一由上游观察者承担（下游观察者只做首字节计时与
	// 输出文本累积），此前由下游观察者以 observeUsage 兼任——记录的是渲染后
	// 的下游格式而非上游原文，且与上游双写 ProviderResponse 取决于读写交错。
	observeUpstreamUsage(response, record, targetPlatform)
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
				updateRecordUsageFromMaheshvara(record, event.Usage)
			}
			if event.Error != nil {
				streamErr = event.Error
				break
			}
			if event.Type == relay.MaheshvaraEventResponseFailed {
				streamErr = fmt.Errorf("custom protocol stream failed")
				break
			}
			if event.Type == relay.MaheshvaraEventResponseCompleted {
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
	if streamErr == nil {
		// 终态校验按严重度排序：无终态 > 有终态但无可呈现输出。
		switch {
		case !decoder.TerminalReceived():
			streamErr = fmt.Errorf("custom protocol stream ended before a configured terminal value or finish reason")
		case !decoder.SawOutput():
			streamErr = fmt.Errorf("custom protocol stream completed without representable output")
		}
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
	s.adjustTokenUsage(group.ID, getInt(record.Usage.TotalTokens), startTime.Format("2006-01-02"))
	record.StatusCode = http.StatusOK
	if streamErr != nil {
		record.StatusCode = http.StatusBadGateway
		record.ErrorKind = ErrorKindUpstream
		record.Error = streamErr.Error()
	}
	return finish(relayOutcome{committed: true, statusCode: record.StatusCode, errMsg: record.Error})
}
