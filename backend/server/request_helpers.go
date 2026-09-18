package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// inputFormatFromPath 按 URL 推导客户端线制。错误出口可能出现在协议解析
// 之前的阶段（鉴权中间件、读请求体），与入口处的格式推导共用本函数。
func inputFormatFromPath(path string) relay.FormatType {
	switch {
	case strings.HasSuffix(path, "/messages"), strings.HasSuffix(path, "/messages/count_tokens"):
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

// readRequestBody 统一两入口的请求体读取:失败时按客户端线制渲染带底层
// 原因(超限给配置上限)的标准错误体,成功返回原始字节。
func (s *Server) readRequestBody(c *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(c.Request.Body)
	if err == nil {
		return body, true
	}
	msg := fmt.Sprintf("failed to read request body: %v", err)
	if strings.Contains(err.Error(), "request body too large") {
		msg = fmt.Sprintf("request body exceeds the configured limit (%d bytes)", s.config.GetMaxBodyBytes())
	}
	log.Printf("Error reading request body: %v", err)
	writeProtocolError(c, inputFormatFromPath(c.Request.URL.Path), &relay.MaheshvaraError{
		Class: relay.ErrorClassInvalidRequest, Message: msg,
	})
	return nil, false
}

// upstreamFetchResult 是非流式取回的统一产物:Maheshvara 响应(或错误)、
// 原始响应体与上游状态码(供错误渲染/透传与 usage 记录)。
type upstreamFetchResult struct {
	maheshvara *relay.MaheshvaraResponse
	respBody   []byte
	status     int
}

// fetchAsMaheshvara 完成非流式转发的一致骨架:按目标格式发送上游请求,
// 非 2xx 读尽错误体并解析为 Maheshvara 错误,2xx 转换为核心响应。
// 四平台分支(server/responses 两入口)此前各持一份近克隆,行为曾漂移。
func (s *Server) fetchAsMaheshvara(ctx context.Context, model config.ModelRef, targetFormat relay.FormatType, body []byte) (*upstreamFetchResult, error) {
	switch targetFormat {
	case relay.FormatResponses:
		resp, respBody, status, err := s.openaiAdapter.SendResponsesRawWithBody(ctx, model.BaseURL, model.APIKey, body)
		if err != nil {
			return &upstreamFetchResult{respBody: respBody, status: status}, err
		}
		mResp, convErr := relay.OpenAIResponsesResponseToMaheshvara(resp)
		if convErr != nil {
			return &upstreamFetchResult{respBody: respBody, status: status}, convErr
		}
		return &upstreamFetchResult{maheshvara: mResp, respBody: respBody, status: status}, nil
	case relay.FormatClaude:
		resp, err := s.claudeAdapter.SendRequest(ctx, model.BaseURL, model.APIKey, body, false)
		if err != nil {
			return &upstreamFetchResult{status: upstreamErrorStatus(err, http.StatusBadGateway), respBody: upstreamErrorBody(err)}, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, relay.MaxUpstreamBodyBytes))
		if resp.StatusCode != http.StatusOK {
			return &upstreamFetchResult{respBody: respBody, status: resp.StatusCode}, fmt.Errorf("upstream returned %s", resp.Status)
		}
		var claudeResp relay.ClaudeResponse
		if err := json.Unmarshal(respBody, &claudeResp); err != nil {
			return &upstreamFetchResult{respBody: respBody, status: resp.StatusCode}, err
		}
		mResp, convErr := relay.AnthropicResponseToMaheshvara(&claudeResp)
		if convErr != nil {
			return &upstreamFetchResult{respBody: respBody, status: resp.StatusCode}, convErr
		}
		return &upstreamFetchResult{maheshvara: mResp, respBody: respBody, status: resp.StatusCode}, nil
	case relay.FormatGemini:
		resp, err := s.geminiAdapter.SendRequest(ctx, model.BaseURL, model.APIKey, model.Name, body, false)
		if err != nil {
			return &upstreamFetchResult{status: upstreamErrorStatus(err, http.StatusBadGateway), respBody: upstreamErrorBody(err)}, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, relay.MaxUpstreamBodyBytes))
		if resp.StatusCode != http.StatusOK {
			return &upstreamFetchResult{respBody: respBody, status: resp.StatusCode}, fmt.Errorf("upstream returned %s", resp.Status)
		}
		var geminiResp relay.GeminiResponse
		if err := json.Unmarshal(respBody, &geminiResp); err != nil {
			return &upstreamFetchResult{respBody: respBody, status: resp.StatusCode}, err
		}
		mResp, convErr := relay.GeminiResponseToMaheshvara(&geminiResp)
		if convErr != nil {
			return &upstreamFetchResult{respBody: respBody, status: resp.StatusCode}, convErr
		}
		return &upstreamFetchResult{maheshvara: mResp, respBody: respBody, status: resp.StatusCode}, nil
	default:
		resp, respBody, status, err := s.openaiAdapter.SendRequestRawWithBody(ctx, model.BaseURL, model.APIKey, body)
		if err != nil {
			return &upstreamFetchResult{respBody: respBody, status: status}, err
		}
		mResp, convErr := relay.OpenAIChatResponseToMaheshvara(resp)
		if convErr != nil {
			return &upstreamFetchResult{respBody: respBody, status: status}, convErr
		}
		return &upstreamFetchResult{maheshvara: mResp, respBody: respBody, status: status}, nil
	}
}

// usageDayKey 把时刻归一为日配额的日期键(acquire/adjust 的跨日守卫共用)。
func usageDayKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// settleMaheshvaraUsage 是 Maheshvara 响应的统一结算点:真实 usage 优先、
// 缺失时本地估算,实际消耗按 acquire 当日计入组级日配额。
func (s *Server) settleMaheshvaraUsage(group *config.ModelGroupConfig, record *usageRecord, startTime time.Time, resp *relay.MaheshvaraResponse) {
	if resp == nil {
		return
	}
	updateRecordUsageFromMaheshvara(record, resp.Usage)
	applyLocalResponseEstimate(record, extractOutputTextFromMaheshvaraResponse(resp), s.config.GetUsageConfig())
	s.adjustTokenUsage(group.ID, derefInt(record.Usage.TotalTokens), usageDayKey(startTime))
}

// settleProviderBodyUsage 与 settleMaheshvaraUsage 同语义,供未经 Maheshvara
// 的线制原生响应(按平台解析响应体)使用。
func (s *Server) settleProviderBodyUsage(group *config.ModelGroupConfig, record *usageRecord, startTime time.Time, platform relay.Platform, respBody []byte) {
	applyProviderUsageToRecord(record, extractProviderUsageFromBody(platform, "", respBody))
	applyLocalResponseEstimate(record, extractOutputTextFromProviderBody(platform, "", respBody), s.config.GetUsageConfig())
	s.adjustTokenUsage(group.ID, derefInt(record.Usage.TotalTokens), usageDayKey(startTime))
}

// settleStreamUsage 是流式收尾结算:下游观察流已累计文本与 usage,此处补
// 本地估算并把实际消耗计入日配额(无组概念的调用方传 nil 跳过配额)。
func (s *Server) settleStreamUsage(group *config.ModelGroupConfig, record *usageRecord, startTime time.Time) {
	applyLocalResponseEstimate(record, "", s.config.GetUsageConfig())
	if group != nil {
		s.adjustTokenUsage(group.ID, derefInt(record.Usage.TotalTokens), usageDayKey(startTime))
	}
}
