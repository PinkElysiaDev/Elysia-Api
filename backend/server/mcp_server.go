package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MCP Streamable HTTP（2026-07-28）：无握手，每请求声明版本，SSE 断流即取消。

const (
	mcpServerName            = "elysia-api-agent"
	mcpServerVersion         = "1.0.0"
	mcpModernProtocolVersion = "2026-07-28"
	mcpMetaProtocolVersion   = "io.modelcontextprotocol/protocolVersion"
	mcpErrHeaderMismatch     = -32020
	mcpErrUnsupportedVersion = -32022
	mcpInstructions          = "MCP 只提供 elysia_cli，用于执行 Elysia API 网关运维命令。首次使用先查 elysia help。每次调用无状态；需要复用协议草稿或测试目标时，请把相关命令放在同一次 command 批处理中。"
)

var mcpSupportedVersions = []string{mcpModernProtocolVersion}

// handleMCP 是 /mcp 的统一入口（gin Any 注册，方法白名单在内部收口）。
func (s *Server) handleMCP(c *gin.Context) {
	// 规范：GET（服务端主动推送流）与 DELETE（终结会话）均可选；
	// 本实现无状态、不做服务端推送，二者一律 405。
	if c.Request.Method != http.MethodPost {
		c.Header("Allow", http.MethodPost)
		c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{"error": "MCP endpoint accepts POST only"})
		return
	}
	// 防浏览器端 DNS rebinding：Origin 存在时必须与本服务同源（或回环）。
	if origin := strings.TrimSpace(c.Request.Header.Get("Origin")); origin != "" && !s.mcpOriginAllowed(c, origin) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}
	// Accept 必须同时接受 JSON 与 SSE 两种响应形态。
	if !mcpAcceptsBoth(c) {
		c.AbortWithStatusJSON(http.StatusNotAcceptable, gin.H{"error": "Accept must include application/json and text/event-stream"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, agentMaxBodyBytes))
	if err != nil {
		mcpWriteJSON(c, jsonrpcFail(nil, jsonrpcParseError, "read body failed: "+err.Error()), http.StatusBadRequest)
		return
	}
	var req jsonrpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		mcpWriteJSON(c, jsonrpcFail(nil, jsonrpcParseError, err.Error()), http.StatusBadRequest)
		return
	}
	// 批量请求在 2025-06-18 起已从规范移除；body 是数组时 Unmarshal 已报错。
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		mcpWriteJSON(c, jsonrpcFail(req.ID, jsonrpcInvalidRequest, "jsonrpc must be \"2.0\""), http.StatusBadRequest)
		return
	}

	eraCode, eraErr := mcpValidateVersion(c, req)
	if eraErr != "" {
		if eraCode == 0 {
			eraCode = mcpErrUnsupportedVersion
		}
		data := any(nil)
		if eraCode == mcpErrUnsupportedVersion {
			data = map[string]any{"supported": mcpSupportedVersions}
		}
		mcpWriteJSON(c, jsonrpcFailData(req.ID, eraCode, eraErr, data), http.StatusBadRequest)
		return
	}

	// 通知（无 id）：接受即 202，无响应体。通知不得回应错误，未知通知静默接受。
	if len(req.ID) == 0 || string(req.ID) == "null" {
		c.AbortWithStatus(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "server/discover":
		s.mcpHandleDiscover(c, req)
	case "ping":
		mcpWriteJSON(c, jsonrpcOK(req.ID, gin.H{}), http.StatusOK)
	case "tools/list":
		s.mcpHandleToolsList(c, req)
	case "tools/call":
		s.mcpHandleToolsCall(c, req)
	default:
		mcpWriteJSON(c, jsonrpcFail(req.ID, jsonrpcMethodNotFound, "method not found: "+req.Method), http.StatusNotFound)
	}
}

// mcpValidateVersion validates the current protocol version and mirror headers.
func mcpValidateVersion(c *gin.Context, req jsonrpcRequest) (int, string) {
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if raw, ok := params.Meta[mcpMetaProtocolVersion]; ok {
		var version string
		if err := json.Unmarshal(raw, &version); err != nil || version != mcpModernProtocolVersion {
			return mcpErrUnsupportedVersion, "unsupported protocol version"
		}
		header := strings.TrimSpace(c.Request.Header.Get("MCP-Protocol-Version"))
		if header != "" && header != version {
			return mcpErrHeaderMismatch, "MCP-Protocol-Version header does not match _meta"
		}
		// 镜像头存在时必须与 body 一致（Modern REQUIRED；宽松处理缺省）。
		if mirror := strings.TrimSpace(c.Request.Header.Get("Mcp-Method")); mirror != "" && mirror != req.Method {
			return mcpErrHeaderMismatch, "Mcp-Method header does not match body method"
		}
		if req.Method == "tools/call" {
			var callParams struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &callParams)
			if mirror := strings.TrimSpace(c.Request.Header.Get("Mcp-Name")); mirror != "" && callParams.Name != "" && mirror != callParams.Name {
				return mcpErrHeaderMismatch, "Mcp-Name header does not match tool name"
			}
		}
		return 0, ""
	}
	return mcpErrUnsupportedVersion, "params._meta must declare the current protocol version"
}

// mcpHandleDiscover Modern 的 server/discover：一次返回版本/能力/信息。
func (s *Server) mcpHandleDiscover(c *gin.Context, req jsonrpcRequest) {
	mcpWriteJSON(c, jsonrpcOK(req.ID, gin.H{
		"supportedVersions": mcpSupportedVersions,
		"capabilities":      gin.H{"tools": gin.H{"listChanged": false}},
		"serverInfo":        gin.H{"name": mcpServerName, "title": "Elysia API Gateway Agent", "version": mcpServerVersion},
		"instructions":      mcpInstructions,
		"resultType":        "complete",
	}), http.StatusOK)
}

// mcpHandleToolsList 全量单页返回（工具数少，不实现 cursor）。
func (s *Server) mcpHandleToolsList(c *gin.Context, req jsonrpcRequest) {
	definitions := make([]gin.H, 0)
	for _, tool := range mcpToolset() {
		definitions = append(definitions, gin.H{
			"name": tool.name, "title": tool.title, "description": tool.description,
			"inputSchema": tool.schema,
			"annotations": gin.H{"title": tool.title},
		})
	}
	result := gin.H{"tools": definitions, "resultType": "complete", "ttlMs": 300_000, "cacheScope": "user"}
	mcpWriteJSON(c, jsonrpcOK(req.ID, result), http.StatusOK)
}

// mcpHandleToolsCall 工具调用：elysia_cli 通过 SSE 返回 progress 通知和终帧
// JSON-RPC response。
func (s *Server) mcpHandleToolsCall(c *gin.Context, req jsonrpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Meta      struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		mcpWriteJSON(c, jsonrpcFail(req.ID, jsonrpcInvalidParams, err.Error()), http.StatusOK)
		return
	}
	tool := mcpFindTool(params.Name)
	if tool == nil {
		mcpWriteJSON(c, jsonrpcFail(req.ID, jsonrpcInvalidParams, "Unknown tool: "+params.Name), http.StatusOK)
		return
	}
	if !tool.streaming {
		result, err := tool.invoke(c.Request.Context(), s, params.Arguments, nil)
		mcpWriteJSON(c, mcpToolCallResult(req.ID, result, err), http.StatusOK)
		return
	}
	// 流式路径：SSE 响应。Modern 语义下客户端断开 SSE 即取消——尽力停止
	// 轮次（引擎侧 Stop 最多等 15s 收尾，这里异步执行不阻塞出口）。
	s.mcpStreamToolCall(c, req, tool, params.Arguments, params.Meta.ProgressToken)
}

// mcpToolCallResult 组装工具调用的响应（业务失败 → isError:true 而非
// JSON-RPC error，让客户端模型可自我纠正）。
func mcpToolCallResult(id json.RawMessage, result any, err error) jsonrpcResponse {
	content := []gin.H{}
	if result != nil {
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			result = nil
			err = fmt.Errorf("encode tool result: %w", encodeErr)
		} else {
			// MCP tool results include text content alongside structured output.
			content = append(content, gin.H{"type": "text", "text": string(encoded)})
		}
	}
	if err != nil {
		content = append(content, gin.H{"type": "text", "text": err.Error()})
	}
	if len(content) == 0 {
		content = append(content, gin.H{"type": "text", "text": "done"})
	}
	payload := gin.H{"content": content, "isError": err != nil, "resultType": "complete"}
	// 批处理部分失败时仍返回已经完成的结果与错误明细。
	if result != nil {
		payload["structuredContent"] = result
	}
	return jsonrpcOK(id, payload)
}

// mcpStreamToolCall 的 SSE 响应：progress 通知（progressToken 存在时）→
// 终帧 JSON-RPC response（含 structuredContent）。CLI 直接继承 HTTP context，
// 断流或取消会停止当前批处理，不查找也不停止任何 Agent 会话。
func (s *Server) mcpStreamToolCall(c *gin.Context, req jsonrpcRequest, tool *mcpTool, args json.RawMessage, progressToken json.RawMessage) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := c.Writer.(http.Flusher)
	write := func(payload jsonrpcResponse) bool {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "event: message\ndata: %s\n\n", encoded); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}
	hasProgress := len(progressToken) > 0 && string(progressToken) != "null"
	step := 0
	progress := func(message string) {
		if !hasProgress || message == "" {
			return
		}
		step++
		write(jsonrpcNotification("notifications/progress", gin.H{
			"progressToken": json.RawMessage(progressToken),
			"progress":      step, "message": message,
		}))
	}
	result, err := tool.invoke(c.Request.Context(), s, args, progress)
	write(mcpToolCallResult(req.ID, result, err))
}

// mcpOriginAllowed 校验浏览器 Origin（同源或本机回环）。非浏览器客户端
// 不带 Origin，天然放行。
func (s *Server) mcpOriginAllowed(c *gin.Context, origin string) bool {
	if strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://[::1]:") {
		return true
	}
	if publicURL := strings.TrimSpace(s.publicBaseURL(c)); publicURL != "" && origin == publicURL {
		return true
	}
	// 同源判定：Origin 的 host 与请求 Host 一致。
	if at := strings.Index(origin, "://"); at > 0 {
		if host := origin[at+3:]; host == c.Request.Host {
			return true
		}
	}
	return false
}

// mcpAcceptsBoth 检查 Accept 同时含 application/json 与 text/event-stream。
func mcpAcceptsBoth(c *gin.Context) bool {
	accept := c.Request.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && strings.Contains(accept, "text/event-stream")
}

// mcpWriteJSON 写单个 JSON-RPC 响应。
func mcpWriteJSON(c *gin.Context, payload jsonrpcResponse, status int) {
	c.JSON(status, payload)
}

// publicBaseURL 从配置或请求推导对外基础地址（Agent Card / Origin 校验共用）。
func (s *Server) publicBaseURL(c *gin.Context) string {
	if configured := strings.TrimSpace(s.config.GetAgentRemote().PublicURL); configured != "" {
		return configured
	}
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}
