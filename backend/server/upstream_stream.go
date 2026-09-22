package server

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

// upstreamStreamConn 是已确认 200 的上游流式连接：resp 供转换/透传消费，
// format 为该连接的上游线制。
type upstreamStreamConn struct {
	resp   *http.Response
	format relay.FormatType
}

// upstreamStreamFailure 统一四条平台的建连失败。transport 为真时 err 是
// 传输层原始错误（OpenAI 系适配器会把非 200 转成 UpstreamStatusError 归入
// 此类，保持各入口既有的消息口径）；否则是上游返回的非 200——错误体已
// 读出、连接已关闭，status/body 供错误透传与重试判定。
type upstreamStreamFailure struct {
	transport bool
	err       error
	status    int
	body      []byte
}

func (f *upstreamStreamFailure) Error() string {
	if f.transport {
		return f.err.Error()
	}
	return fmt.Sprintf("upstream returned status %d: %s", f.status, f.body)
}

// openUpstreamStream 按上游线制建立流式连接，把「发送 → 传输判错 → 非 200
// 读体」的四条平台分支收敛为一处。OpenAI 系非 200 已在适配器内转为
// UpstreamStatusError，这里的非 200 分支实际由 Claude/Gemini 触达。
// 调用方拿到连接后负责写出 SSE 头并转发 resp。
func (s *Server) openUpstreamStream(ctx context.Context, format relay.FormatType, model config.ModelRef, body []byte) (*upstreamStreamConn, *upstreamStreamFailure) {
	var resp *http.Response
	var err error
	switch format {
	case relay.FormatResponses:
		resp, err = s.openaiAdapter.SendResponsesStream(ctx, model.BaseURL, model.APIKey, body)
	case relay.FormatClaude:
		resp, err = s.claudeAdapter.SendRequest(ctx, model.BaseURL, model.APIKey, body, true)
	case relay.FormatGemini:
		resp, err = s.geminiAdapter.SendRequest(ctx, model.BaseURL, model.APIKey, model.Name, body, true)
	default:
		resp, err = s.openaiAdapter.SendRequestStream(ctx, model.BaseURL, model.APIKey, body)
	}
	if err != nil {
		return nil, &upstreamStreamFailure{transport: true, err: err}
	}
	if resp.StatusCode != http.StatusOK {
		// 非 200 的 body 是 JSON 错误而非 SSE，直接转换会扫不到 data: 行、
		// 发出伪造的空流并吞掉错误（R3）：读出错误体交由调用方故障转移。
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &upstreamStreamFailure{status: resp.StatusCode, body: respBody}
	}
	return &upstreamStreamConn{resp: resp, format: format}, nil
}
