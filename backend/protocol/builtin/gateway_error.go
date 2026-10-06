package builtin

// GatewayError describes gateway-owned failures before wire decoding, such as
// authentication and model authorization. Provider failures use semantic payloads.
type GatewayError struct {
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
	// Class 是错误的稳定分类(四线制渲染的权威来源,见 error_protocol.go);
	// Status 为上游真实 HTTP 状态(跨协议翻译时携带,0 表示按分类推导)。
	Class  ErrorClass `json:"class,omitempty"`
	Status int        `json:"status,omitempty"`
}

func (e *GatewayError) Error() string { return e.Message }
