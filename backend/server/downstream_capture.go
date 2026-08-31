package server

import (
	"github.com/elysia-api/backend/config"
	"github.com/gin-gonic/gin"
)

// downstreamCaptureWriter 包裹 gin.ResponseWriter，把所有写回下游客户端的字节
// tee 一份到封顶 buffer，用于 usage 日志的第四段「返回下游」内容捕获。
//
// 它内嵌 gin.ResponseWriter 接口，因此 Flush/Status/Header/Hijack 等方法自动透传，
// 只覆盖 Write/WriteString 累积字节。buffer 封顶 limit（由日志配置决定：外置
// 开启时给媒体内容留 8MiB 余量，物化后再外置+截断；limit<=0 时不捕获），
// 超出仅丢弃多余部分并置 truncated（不影响实际写出给客户端的内容）。
type downstreamCaptureWriter struct {
	gin.ResponseWriter
	buf       []byte
	limit     int
	truncated bool
}

func newDownstreamCaptureWriter(inner gin.ResponseWriter, limit int) *downstreamCaptureWriter {
	return &downstreamCaptureWriter{ResponseWriter: inner, limit: limit}
}

func (w *downstreamCaptureWriter) capture(data []byte) {
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

func (w *downstreamCaptureWriter) Write(data []byte) (int, error) {
	w.capture(data)
	return w.ResponseWriter.Write(data)
}

func (w *downstreamCaptureWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// downstreamBody 返回已捕获的下游响应体，作为 usageBody（content 已是最终写出的
// 原始字节；这里不做 JSON 脱敏，因为下游响应通常不含上游密钥，且需保留原文供导出。
// 媒体外置与最终截断在 recordUsage 物化后经 finalizeDownstreamBody 统一处理）。
func (w *downstreamCaptureWriter) downstreamBody() usageBody {
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

// installDownstreamCapture 在 relay 入口处用 capture writer 包裹 gin 的 ResponseWriter，
// 并把它登记到 usageRecord 上。后续 recordUsage 会统一回读捕获的下游响应体。
// 返回的 writer 已替换 c.Writer；若已是 capture writer 则复用（幂等）。
func installDownstreamCapture(c *gin.Context, record *usageRecord, limit int) *downstreamCaptureWriter {
	if record == nil {
		return nil
	}
	if existing, ok := c.Writer.(*downstreamCaptureWriter); ok {
		record.downstream = existing
		return existing
	}
	capture := newDownstreamCaptureWriter(c.Writer, limit)
	c.Writer = capture
	record.downstream = capture
	return capture
}
