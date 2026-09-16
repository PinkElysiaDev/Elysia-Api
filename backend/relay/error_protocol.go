package relay

import (
	"encoding/json"
	"strings"
)

/*
 * 错误的核心协议转换(Maheshvara 化)。
 *
 * 请求/响应体经 Maheshvara 互转,错误同样需要:失败点产出统一的
 * MaheshvaraError,出口按客户端线制渲染成各自协议的标准错误体。
 * 现行规范依据(2026-09 核验):
 *   - OpenAI(Chat/Responses 共用信封,四字段 required、param/code 可 null):
 *     401 = invalid_request_error + code "invalid_api_key";429 = rate_limit_error
 *     (+细分 code);503 = service_unavailable_error + "server_is_overloaded";
 *     模型不存在 = 404 + code "model_not_found" + param "model"。旧 type 枚举
 *     (authentication_error 等)已废弃,细分一律走 code。
 *   - Anthropic:{"type":"error","error":{"type","message"}},type 取
 *     invalid_request_error/authentication_error/permission_error/
 *     not_found_error/rate_limit_error/api_error/overloaded_error(529)。
 *   - Gemini:{"error":{"code":<HTTP>,"message","status"}},status 取 gRPC 码
 *     INVALID_ARGUMENT/UNAUTHENTICATED/PERMISSION_DENIED/NOT_FOUND/
 *     RESOURCE_EXHAUSTED/UNAVAILABLE/INTERNAL。
 * SDK/Codex 对 ≥500 自动重试:配置类错误必须归 4xx,否则触发客户端重试风暴。
 */

// ErrorClass 是错误的稳定分类:四线制的 type/status/code 均由它派生,
// 细分码(如上游 429 的 slow_down)负载在 MaheshvaraError.Code 上原样透传。
// 与 usage 记录的 errorKind 同值域。
type ErrorClass string

const (
	ErrorClassInvalidRequest ErrorClass = "invalid_request"
	ErrorClassAuthentication ErrorClass = "authentication"
	ErrorClassPermission     ErrorClass = "permission"
	ErrorClassModelNotFound  ErrorClass = "model_not_found"
	ErrorClassRateLimit      ErrorClass = "rate_limit"
	ErrorClassOverloaded     ErrorClass = "overloaded"
	ErrorClassUpstream       ErrorClass = "upstream"
	ErrorClassServer         ErrorClass = "server"
)

// HTTPStatus 返回该分类的建议状态码。客户端不可重试的配置类问题全部 4xx。
func (class ErrorClass) HTTPStatus() int {
	switch class {
	case ErrorClassInvalidRequest:
		return 400
	case ErrorClassAuthentication:
		return 401
	case ErrorClassPermission:
		return 403
	case ErrorClassModelNotFound:
		return 404
	case ErrorClassRateLimit:
		return 429
	case ErrorClassOverloaded:
		return 503
	case ErrorClassUpstream:
		return 502
	default:
		return 500
	}
}

// OrDefault 补全空分类:未分类的错误按服务端内部错误处理。
func (class ErrorClass) OrDefault() ErrorClass {
	if class == "" {
		return ErrorClassServer
	}
	return class
}

// openAITypeCode 返回 OpenAI 信封的 type 与建议 code(细分 code 由 err.Code 覆盖)。
func (class ErrorClass) openAITypeCode() (string, string) {
	switch class {
	case ErrorClassInvalidRequest:
		return "invalid_request_error", ""
	case ErrorClassAuthentication:
		// 现行实践:401 的 type 也是 invalid_request_error,细分靠 code。
		return "invalid_request_error", "invalid_api_key"
	case ErrorClassPermission:
		return "invalid_request_error", ""
	case ErrorClassModelNotFound:
		return "invalid_request_error", "model_not_found"
	case ErrorClassRateLimit:
		return "rate_limit_error", ""
	case ErrorClassOverloaded:
		return "service_unavailable_error", "server_is_overloaded"
	case ErrorClassUpstream:
		return "api_error", ""
	default:
		return "api_error", "server_error"
	}
}

func (class ErrorClass) anthropicType() string {
	switch class {
	case ErrorClassInvalidRequest:
		return "invalid_request_error"
	case ErrorClassAuthentication:
		return "authentication_error"
	case ErrorClassPermission:
		return "permission_error"
	case ErrorClassModelNotFound:
		return "not_found_error"
	case ErrorClassRateLimit:
		return "rate_limit_error"
	case ErrorClassOverloaded:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

func (class ErrorClass) geminiStatus() string {
	switch class {
	case ErrorClassInvalidRequest:
		return "INVALID_ARGUMENT"
	case ErrorClassAuthentication:
		return "UNAUTHENTICATED"
	case ErrorClassPermission:
		return "PERMISSION_DENIED"
	case ErrorClassModelNotFound:
		return "NOT_FOUND"
	case ErrorClassRateLimit:
		return "RESOURCE_EXHAUSTED"
	case ErrorClassOverloaded, ErrorClassUpstream:
		return "UNAVAILABLE"
	default:
		return "INTERNAL"
	}
}

// EffectiveStatus 结合上游真实状态码(若有)给出最终 HTTP 状态:
// 上游状态优先(保真),但 5xx 上游遇到不可重试分类时尊重分类,
// 防止配置类错误被 SDK 自动重试。
func (e *MaheshvaraError) EffectiveStatus() int {
	class := e.Class.OrDefault()
	if e.Status > 0 {
		return e.Status
	}
	return class.HTTPStatus()
}

// ProtocolErrorBody 把核心错误渲染为客户端线制的标准错误体。
// format 为客户端输入协议;返回 (最终 HTTP 状态码, JSON 体)。
func ProtocolErrorBody(format FormatType, e *MaheshvaraError) (int, []byte) {
	class := e.Class.OrDefault()
	status := e.EffectiveStatus()
	var body []byte
	switch format {
	case FormatClaude:
		// Anthropic 的过载分类固定 529;其余沿用分类状态码。
		if class == ErrorClassOverloaded && e.Status == 0 {
			status = 529
		}
		body, _ = json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    class.anthropicType(),
				"message": e.Message,
			},
		})
	case FormatGemini:
		body, _ = json.Marshal(map[string]any{
			"error": map[string]any{
				"code":    status,
				"message": e.Message,
				"status":  class.geminiStatus(),
			},
		})
	default:
		// OpenAI Chat / Responses 共用信封,四字段 required(param/code 可 null)。
		typ, code := class.openAITypeCode()
		if e.Code != "" {
			code = e.Code
		}
		param := any(nil)
		if e.Param != "" {
			param = e.Param
		} else if class == ErrorClassModelNotFound {
			param = "model"
		}
		body, _ = json.Marshal(map[string]any{
			"error": map[string]any{
				"message": e.Message,
				"type":    typ,
				"param":   param,
				"code":    nullableString(code),
			},
		})
	}
	return status, body
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// classFromStatus 按上游 HTTP 状态反推错误分类(解析出体字段前的兜底)。
func classFromStatus(status int) ErrorClass {
	switch {
	case status == 400 || status == 413 || status == 422:
		return ErrorClassInvalidRequest
	case status == 401:
		return ErrorClassAuthentication
	case status == 403:
		return ErrorClassPermission
	case status == 404:
		return ErrorClassModelNotFound
	case status == 429:
		return ErrorClassRateLimit
	case status == 503:
		return ErrorClassOverloaded
	case status >= 500:
		return ErrorClassUpstream
	default:
		return ErrorClassInvalidRequest
	}
}

// ParseUpstreamError 尽力把上游错误体解析为核心错误(跨线制翻译时使用):
// 按上游线制解出 message/type/code 等字段并归入稳定分类,保留上游真实
// 状态码与细分 code。解析失败回退 Upstream 分类 + 原文摘要(截断)。
func ParseUpstreamError(upstreamFormat FormatType, status int, body []byte) *MaheshvaraError {
	mErr := &MaheshvaraError{Status: status}
	var raw map[string]any
	if json.Unmarshal(body, &raw) == nil {
		switch upstreamFormat {
		case FormatClaude:
			if inner, _ := raw["error"].(map[string]any); inner != nil {
				mErr.Message, _ = inner["message"].(string)
				if t, _ := inner["type"].(string); t != "" {
					mErr.Class = classFromAnthropicType(t)
				}
			}
		case FormatGemini:
			if inner, _ := raw["error"].(map[string]any); inner != nil {
				mErr.Message, _ = inner["message"].(string)
				if s, _ := inner["status"].(string); s != "" {
					mErr.Class = classFromGeminiStatus(s)
				}
			}
		default:
			// OpenAI Chat/Responses:{"error":{message,type,param,code}}。
			if inner, _ := raw["error"].(map[string]any); inner != nil {
				mErr.Message, _ = inner["message"].(string)
				mErr.Code, _ = inner["code"].(string)
				mErr.Param, _ = inner["param"].(string)
			}
		}
	}
	if mErr.Class == "" {
		mErr.Class = classFromStatus(status)
	}
	if mErr.Message == "" {
		mErr.Message = upstreamErrorSummary(body)
	}
	return mErr
}

func classFromAnthropicType(t string) ErrorClass {
	switch t {
	case "invalid_request_error":
		return ErrorClassInvalidRequest
	case "authentication_error":
		return ErrorClassAuthentication
	case "permission_error":
		return ErrorClassPermission
	case "not_found_error":
		return ErrorClassModelNotFound
	case "rate_limit_error":
		return ErrorClassRateLimit
	case "overloaded_error":
		return ErrorClassOverloaded
	default:
		return ErrorClassUpstream
	}
}

func classFromGeminiStatus(s string) ErrorClass {
	switch s {
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION":
		return ErrorClassInvalidRequest
	case "UNAUTHENTICATED":
		return ErrorClassAuthentication
	case "PERMISSION_DENIED":
		return ErrorClassPermission
	case "NOT_FOUND":
		return ErrorClassModelNotFound
	case "RESOURCE_EXHAUSTED":
		return ErrorClassRateLimit
	case "UNAVAILABLE":
		return ErrorClassOverloaded
	default:
		return ErrorClassUpstream
	}
}

// upstreamErrorSummary 生成上游原文摘要(解析失败时的兜底消息)。
func upstreamErrorSummary(body []byte) string {
	const max = 240
	text := strings.TrimSpace(string(body))
	if len(text) > max {
		text = text[:max] + "…"
	}
	if text == "" {
		return "upstream request failed"
	}
	return "upstream error: " + text
}
