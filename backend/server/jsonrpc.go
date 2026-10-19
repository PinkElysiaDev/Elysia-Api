package server

import (
	"encoding/json"

	"github.com/bytedance/sonic"
)

// JSON-RPC 2.0 envelopes for A2A. MCP uses the official SDK's transport.

const (
	jsonrpcParseError     = -32700
	jsonrpcInvalidRequest = -32600
	jsonrpcMethodNotFound = -32601
	jsonrpcInvalidParams  = -32602
	jsonrpcInternalError  = -32603
)

// jsonrpcRequest 是入站请求/通知。id 用 RawMessage 保留原样回显能力
// （string 与数字都合法）。
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcError 是标准错误对象。
type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// jsonrpcResponse 是出站响应；Result 与 Error 互斥。
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// jsonrpcOK 构造成功响应（id 原样回显）。
func jsonrpcOK(id json.RawMessage, result any) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// jsonrpcFail 构造错误响应。
func jsonrpcFail(id json.RawMessage, code int, message string) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: message}}
}

// jsonrpcFailData 构造带 data 载荷的错误响应。
func jsonrpcFailData(id json.RawMessage, code int, message string, data any) jsonrpcResponse {
	encoded, err := sonic.ConfigStd.Marshal(data)
	if err != nil {
		return jsonrpcFail(id, code, message)
	}
	return jsonrpcResponse{JSONRPC: "2.0", ID: id,
		Error: &jsonrpcError{Code: code, Message: message, Data: encoded}}
}
