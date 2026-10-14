package builtin

// GatewayError describes gateway-owned failures before wire decoding, such as
// authentication and model authorization. Provider failures use semantic payloads.
type GatewayError struct {
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
	// Class is the stable failure category, independent of wire error codes.
	// Status retains the original HTTP status; zero requests class-based inference.
	Class  ErrorClass `json:"class,omitempty"`
	Status int        `json:"status,omitempty"`
}

func (e *GatewayError) Error() string { return e.Message }
