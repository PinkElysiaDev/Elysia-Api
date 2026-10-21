package server

import (
	"github.com/cloudwego/hertz/pkg/app"
	hprotocol "github.com/cloudwego/hertz/pkg/protocol"

	"github.com/elysia-api/backend/config"
)

// Capture bounded downstream bytes. Buffered responses are read at settlement;
// native streaming writes tee directly into this request-owned capture.
type downstreamCapture struct {
	response  *hprotocol.Response
	streaming bool
	buf       []byte
	limit     int
	truncated bool
}

func (w *downstreamCapture) capture(data []byte) {
	if w.limit <= 0 || w.truncated {
		return
	}
	remaining := w.limit - len(w.buf)
	if remaining <= 0 {
		w.truncated = true
		return
	}
	if len(data) > remaining {
		w.buf = append(w.buf, data[:remaining]...)
		w.truncated = true
		return
	}
	w.buf = append(w.buf, data...)
}

// downstreamBody 返回已捕获的下游响应体，作为 usageBody（content 已是最终写出的
// 原始字节；这里不做 JSON 脱敏，因为下游响应通常不含上游密钥，且需保留原文供导出。
// 媒体外置与最终截断在 recordUsage 物化后经 finalizeDownstreamBody 统一处理）。
func (w *downstreamCapture) downstreamBody() usageBody {
	if w.response != nil && !w.streaming {
		w.buf, w.truncated = nil, false
		w.capture(w.response.Body())
	}
	return usageBody{Content: string(w.buf), Truncated: w.truncated}
}

// downstreamMediaHeadroom 是外置开启时 tee 缓冲在请求体上限之上的媒体余量：
// 大图/音频的 base64 在截断前必须先完整进入 buffer 才能被提取出来。
const downstreamMediaHeadroom = 8 * 1024 * 1024

// downstreamCaptureLimit 由日志配置推导 tee 缓冲上限：不保存请求体（0）时
// 完全不捕获；外置开启时放宽到 max(上限, 8MiB)。
func downstreamCaptureLimit(cfg config.UsageLogResolved) int {
	if cfg.BodyMaxBytes <= 0 {
		return 0
	}
	limit := cfg.BodyMaxBytes
	if cfg.ExternalizeMedia && limit < downstreamMediaHeadroom {
		limit = downstreamMediaHeadroom
	}
	return limit
}

// installDownstreamCapture registers the request's capture with its usage record.
func installDownstreamCapture(c *app.RequestContext, record *usageRecord, limit int) *downstreamCapture {
	if record == nil {
		return nil
	}
	if existing, ok := c.Get("downstreamCapture"); ok {
		record.downstream = existing.(*downstreamCapture)
		return record.downstream
	}
	capture := &downstreamCapture{response: &c.Response, limit: limit}
	c.Set("downstreamCapture", capture)
	if stream := currentResponseStream(c); stream != nil {
		stream.capture = capture
		capture.streaming = true
	}
	record.downstream = capture
	return capture
}
