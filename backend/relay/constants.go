package relay

const (
	SSEBufInitial          = 64 * 1024
	SSEBufMax              = 16 * 1024 * 1024
	ClaudeDefaultMaxTokens = 65536
	EffortBudgetDefault    = 10000
)

// 思考预算的官方档位边界（Claude budget 档：low/medium/high/xhigh）。
const (
	effortBudgetLow    = 1024
	effortBudgetMedium = 4096
	effortBudgetHigh   = 16384
	effortBudgetMax    = 32000
)

// usageSourceProviderResponse 标记 usage 数据取自上游非流式响应体/流事件。
const usageSourceProviderResponse = "provider_response"

// defaultImageMIME 是无法识别的图片数据的回退 MIME 类型。
const defaultImageMIME = "image/png"
