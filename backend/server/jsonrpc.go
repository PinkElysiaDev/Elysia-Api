package server

import "encoding/json"

// JSON-RPC 2.0 信封（MCP 与 A2A 共用）。两个协议都是「单条消息一个
// POST、响应可为纯 JSON 或 SSE（每帧一条完整消息）」，信封与标准错误码
// 在这里统一；协议专属错误码（MCP -32020 段 / A2A -32000 段）在各协议
// 文件里定义。

const (
	jsonrpcParseError     = -32700
	jsonrpcInvalidRequest = -32600
	jsonrpcMethodNotFound = -32601
	jsonrpcInvalidParams  = -32602
	jsonrpcInternalError  = -32603
)

// jsonrpcRequest 是入站请求/通知。id 用 RawMessage 保留原样回显能力
// （string 与数字都合法；通知无 id，MCP 还禁止 null id）。
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

// jsonrpcResponse 是出站响应；Result 与 Error 互斥。Method/Params 仅在
// 承载通知（SSE 推送帧，无 id）时使用。
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
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
	encoded, err := json.Marshal(data)
	if err != nil {
		return jsonrpcFail(id, code, message)
	}
	return jsonrpcResponse{JSONRPC: "2.0", ID: id,
		Error: &jsonrpcError{Code: code, Message: message, Data: encoded}}
}

// jsonrpcNotification 构造通知（无 id；作为 SSE 推送帧编码）。
func jsonrpcNotification(method string, params any) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", Method: method, Params: params}
}
