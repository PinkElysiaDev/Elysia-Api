package builtin

// ErrorClass identifies a gateway failure independently of provider wire formats.
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

func (e *GatewayError) EffectiveStatus() int {
	class := e.Class.OrDefault()
	if e.Status > 0 {
		return e.Status
	}
	return class.HTTPStatus()
}

// ClassFromStatus classifies an observed HTTP failure without guessing a wire body.
func ClassFromStatus(status int) ErrorClass {
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
	case status == 503 || status == 529:
		return ErrorClassOverloaded
	case status >= 500:
		return ErrorClassUpstream
	default:
		return ErrorClassInvalidRequest
	}
}
