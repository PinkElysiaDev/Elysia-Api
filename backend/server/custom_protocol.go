package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func (s *Server) syncCustomProtocols() {
	rawConfigs := s.config.GetCustomProtocols()
	configs := make([]relay.CustomProtocolConfig, 0, len(rawConfigs))
	for index, raw := range rawConfigs {
		var protocol relay.CustomProtocolConfig
		if err := json.Unmarshal(raw, &protocol); err != nil {
			log.Printf("custom protocol config %d is invalid JSON: %v", index, err)
			return
		}
		configs = append(configs, protocol)
	}
	if err := relay.ReplaceCustomProtocols(configs); err != nil {
		log.Printf("custom protocol reload was rejected; keeping the previous registry: %v", err)
	}
}

func filterCanonicalVisionInputsIfNeeded(group *config.ModelGroupConfig, request *relay.CanonicalRequest) (changed bool, filteredParts int) {
	if request == nil || group == nil || group.VisionCapable == nil || *group.VisionCapable {
		return false, 0
	}
	for index := range request.Messages {
		message := &request.Messages[index]
		kept := message.Content[:0]
		for _, part := range message.Content {
			if part.Type == relay.CanonicalContentImage {
				changed = true
				filteredParts++
				continue
			}
			kept = append(kept, part)
		}
		message.Content = kept
	}
	for index := range request.InputItems {
		item := &request.InputItems[index]
		kept := item.Content[:0]
		itemChanged := false
		for _, part := range item.Content {
			if part.Type == relay.CanonicalContentImage {
				changed = true
				filteredParts++
				itemChanged = true
				continue
			}
			kept = append(kept, part)
		}
		item.Content = kept
		if itemChanged {
			// The raw Responses item may still contain the removed image. Force
			// the target renderer to rebuild this item from canonical content.
			item.RawExtra = nil
		}
	}
	return changed, filteredParts
}

func (s *Server) handleCustomNormalRequest(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, request *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, inputFormat relay.FormatType, startTime time.Time, record *usageRecord, isLast bool) relayOutcome {
	defer func() {
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()
	fail := func(status int, message string, body []byte) relayOutcome {
		record.StatusCode = status
		record.Error = message
		if body != nil {
			c.Data(status, "application/json", body)
		} else {
			c.JSON(status, gin.H{"error": message})
		}
		return relayOutcome{committed: true, statusCode: status, errMsg: message}
	}
	if request == nil {
		return fail(http.StatusInternalServerError, "custom protocol request was not rendered", nil)
	}
	protocol, ok := relay.GetCustomProtocol(relay.CustomProtocolID(targetPlatform))
	if !ok {
		return fail(http.StatusInternalServerError, fmt.Sprintf("custom protocol %q is not registered", relay.CustomProtocolID(targetPlatform)), nil)
	}
	response, err := s.openaiAdapter.SendCustomProtocolRequest(selectedModel.BaseURL, selectedModel.APIKey, request, false)
	if err != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to forward custom protocol request: %v", err), nil)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	record.ProviderResponse = sanitizeUsageBody(body)
	if readErr != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to read custom protocol response: %v", readErr), nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fail(response.StatusCode, string(body), body)
	}
	canonicalResponse, err := relay.CustomProtocolResponseToCanonical(body, protocol)
	if err != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to parse custom protocol response: %v", err), nil)
	}
	if canonicalResponse.Model == "" {
		canonicalResponse.Model = selectedModel.Name
	}
	updateRecordUsageFromCanonical(record, canonicalResponse.Usage)
	applyLocalResponseEstimate(record, extractOutputTextFromCanonicalResponse(canonicalResponse), s.config.GetUsageConfig())
	s.adjustTokenUsage(group.ID, getInt(record.Usage.TotalTokens))

	var output any
	switch inputFormat {
	case relay.FormatClaude:
		output, err = relay.CanonicalToClaudeResponse(canonicalResponse)
	case relay.FormatGemini:
		output, err = relay.CanonicalToGeminiResponse(canonicalResponse)
	default:
		output, err = relay.CanonicalToOpenAIChatResponse(canonicalResponse)
	}
	if err != nil {
		return fail(http.StatusInternalServerError, fmt.Sprintf("failed to render custom protocol response: %v", err), nil)
	}
	record.StatusCode = http.StatusOK
	c.JSON(http.StatusOK, output)
	return relayOutcome{committed: true, statusCode: http.StatusOK}
}

func (s *Server) handleCustomResponsesNormal(c *gin.Context, group *config.ModelGroupConfig, selectedModel config.ModelRef, request *relay.CustomProtocolRequestResult, targetPlatform relay.Platform, startTime time.Time, record *usageRecord, isLast bool) relayOutcome {
	defer func() {
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		s.recordUsage(record)
	}()
	fail := func(status int, message string, body []byte) relayOutcome {
		record.StatusCode = status
		record.Error = message
		if body != nil {
			c.Data(status, "application/json", body)
		} else {
			c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "api_error"}})
		}
		return relayOutcome{committed: true, statusCode: status, errMsg: message}
	}
	if request == nil {
		return fail(http.StatusInternalServerError, "custom protocol request was not rendered", nil)
	}
	protocol, ok := relay.GetCustomProtocol(relay.CustomProtocolID(targetPlatform))
	if !ok {
		return fail(http.StatusInternalServerError, fmt.Sprintf("custom protocol %q is not registered", relay.CustomProtocolID(targetPlatform)), nil)
	}
	response, err := s.openaiAdapter.SendCustomProtocolRequest(selectedModel.BaseURL, selectedModel.APIKey, request, false)
	if err != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to forward custom protocol request: %v", err), nil)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	record.ProviderResponse = sanitizeUsageBody(body)
	if readErr != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to read custom protocol response: %v", readErr), nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fail(response.StatusCode, string(body), body)
	}
	canonicalResponse, err := relay.CustomProtocolResponseToCanonical(body, protocol)
	if err != nil {
		return fail(http.StatusBadGateway, fmt.Sprintf("failed to parse custom protocol response: %v", err), nil)
	}
	if canonicalResponse.Model == "" {
		canonicalResponse.Model = selectedModel.Name
	}
	updateRecordUsageFromCanonical(record, canonicalResponse.Usage)
	applyLocalResponseEstimate(record, extractOutputTextFromCanonicalResponse(canonicalResponse), s.config.GetUsageConfig())
	s.adjustTokenUsage(group.ID, getInt(record.Usage.TotalTokens))
	output, err := relay.CanonicalToResponsesResponse(canonicalResponse)
	if err != nil {
		return fail(http.StatusInternalServerError, fmt.Sprintf("failed to render custom Responses response: %v", err), nil)
	}
	record.StatusCode = http.StatusOK
	c.JSON(http.StatusOK, output)
	return relayOutcome{committed: true, statusCode: http.StatusOK}
}

func renderCanonicalChatResponse(response *relay.CanonicalResponse, inputFormat relay.FormatType) (any, error) {
	switch inputFormat {
	case relay.FormatClaude:
		return relay.CanonicalToClaudeResponse(response)
	case relay.FormatGemini:
		return relay.CanonicalToGeminiResponse(response)
	default:
		return relay.CanonicalToOpenAIChatResponse(response)
	}
}
