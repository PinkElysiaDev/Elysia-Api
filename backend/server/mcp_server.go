package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MCP 服务端（Streamable HTTP，单端点 POST /mcp）。双世代：
//   - Legacy（2024-11-05 ~ 2025-11-25 语义，存量客户端）：initialize 握手、
//     notifications/initialized、ping、tools/list、tools/call；无状态（不签发
//     Mcp-Session-Id，规范允许）；GET/DELETE → 405。
//   - Modern（2026-07-28）：无握手，每请求 params._meta 携带版本与客户端
//     能力，MCP-Protocol-Version 头与 body 一致性校验，镜像头 Mcp-Method/
//     Mcp-Name，server/discover，resultType:"complete"，SSE 断流即取消。
//
// 世代判定：请求 params._meta 带 io.modelcontextprotocol/protocolVersion
// 键即 Modern；否则 Legacy。

const (
	mcpServerName            = "elysia-api-agent"
	mcpServerVersion         = "1.0.0"
	mcpModernProtocolVersion = "2026-07-28"
	mcpLatestLegacyVersion   = "2025-11-25"
	mcpMetaProtocolVersion   = "io.modelcontextprotocol/protocolVersion"
	mcpErrHeaderMismatch     = -32020
	mcpErrUnsupportedVersion = -32022
)

// mcpSupportedVersions 是本服务端认识的全部协议版本（协商用）。
var mcpSupportedVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", mcpModernProtocolVersion}

type mcpEra int

const (
	mcpEraLegacy mcpEra = iota
	mcpEraModern
)

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

	era, eraVersion, eraCode, eraErr := mcpDetectEra(c, req)
	if eraErr != "" {
		if eraCode == 0 {
			eraCode = mcpErrUnsupportedVersion
		}
		data := any(nil)
		if eraCode == mcpErrUnsupportedVersion {
			data = map[string]any{"supported": mcpSupportedVersions}
		}
		c.Status(http.StatusBadRequest)
		mcpWriteJSON(c, jsonrpcFailData(req.ID, eraCode, eraErr, data), http.StatusBadRequest)
		return
	}

	// 通知（无 id）：接受即 202，无响应体。通知不得回应错误，未知通知静默接受。
	if len(req.ID) == 0 || string(req.ID) == "null" {
		// Legacy 的取消通道：轮次与 HTTP 请求解耦，断开/取消通知都不终止已
		// 启动的轮次（结果会落库，可再查）。
		c.AbortWithStatus(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		s.mcpHandleInitialize(c, req, era, eraVersion)
	case "server/discover":
		// Modern MUST；对 Legacy 客户端同样可用（无害）。
		s.mcpHandleDiscover(c, req)
	case "ping":
		mcpWriteJSON(c, jsonrpcOK(req.ID, gin.H{}), http.StatusOK)
	case "tools/list":
		s.mcpHandleToolsList(c, req, era)
	case "tools/call":
		s.mcpHandleToolsCall(c, req, era)
	default:
		mcpWriteJSON(c, jsonrpcFail(req.ID, jsonrpcMethodNotFound, "method not found: "+req.Method), http.StatusNotFound)
	}
}

// mcpDetectEra 判定请求世代并完成版本/镜像头校验。eraErr 非空时 eraCode
// 给出错误码：-32022（版本不支持，data 带 supported）或 -32020（头不一致）。
func mcpDetectEra(c *gin.Context, req jsonrpcRequest) (mcpEra, string, int, string) {
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if raw, ok := params.Meta[mcpMetaProtocolVersion]; ok {
		var version string
		if err := json.Unmarshal(raw, &version); err != nil || !mcpVersionSupported(version) {
			return mcpEraModern, "", mcpErrUnsupportedVersion, "unsupported protocol version"
		}
		header := strings.TrimSpace(c.Request.Header.Get("MCP-Protocol-Version"))
		if header != "" && header != version {
			return mcpEraModern, version, mcpErrHeaderMismatch, "MCP-Protocol-Version header does not match _meta"
		}
		// 镜像头存在时必须与 body 一致（Modern REQUIRED；宽松处理缺省）。
		if mirror := strings.TrimSpace(c.Request.Header.Get("Mcp-Method")); mirror != "" && mirror != req.Method {
			return mcpEraModern, version, mcpErrHeaderMismatch, "Mcp-Method header does not match body method"
		}
		if req.Method == "tools/call" {
			var callParams struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &callParams)
			if mirror := strings.TrimSpace(c.Request.Header.Get("Mcp-Name")); mirror != "" && callParams.Name != "" && mirror != callParams.Name {
				return mcpEraModern, version, mcpErrHeaderMismatch, "Mcp-Name header does not match tool name"
			}
		}
		return mcpEraModern, version, 0, ""
	}
	// Legacy：MCP-Protocol-Version 头非法值拒绝；缺省按 2025-03-26 处理。
	header := strings.TrimSpace(c.Request.Header.Get("MCP-Protocol-Version"))
	if header != "" && !mcpVersionSupported(header) {
		return mcpEraLegacy, "", mcpErrUnsupportedVersion, "unsupported protocol version"
	}
	if header == "" {
		header = "2025-03-26"
	}
	return mcpEraLegacy, header, 0, ""
}

func mcpVersionSupported(version string) bool {
	for _, item := range mcpSupportedVersions {
		if item == version {
			return true
		}
	}
	return false
}

// mcpHandleInitialize Legacy 握手：回显支持的版本（客户端版本可处理时原样
// 回显，否则回最新 Legacy 版本）。
func (s *Server) mcpHandleInitialize(c *gin.Context, req jsonrpcRequest, era mcpEra, eraVersion string) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &params)
	negotiated := mcpLatestLegacyVersion
	if mcpVersionSupported(params.ProtocolVersion) && params.ProtocolVersion != mcpModernProtocolVersion {
		negotiated = params.ProtocolVersion
	}
	result := gin.H{
		"protocolVersion": negotiated,
		"capabilities":    gin.H{"tools": gin.H{"listChanged": false}},
		"serverInfo":      gin.H{"name": mcpServerName, "title": "Elysia API Gateway Agent", "version": mcpServerVersion},
		"instructions":    "通过 agent_* 工具驱动网关内置 AI 助手：先 agent_list_sessions/agent_create_session，再 agent_send_message 驱动一轮；等待审批时用 agent_respond 处理。会话内权限档（allowSave 等）决定哪些工具调用需要人工确认。",
	}
	if era == mcpEraModern {
		result["resultType"] = "complete"
	}
	mcpWriteJSON(c, jsonrpcOK(req.ID, result), http.StatusOK)
}

// mcpHandleDiscover Modern 的 server/discover：一次返回版本/能力/信息。
func (s *Server) mcpHandleDiscover(c *gin.Context, req jsonrpcRequest) {
	mcpWriteJSON(c, jsonrpcOK(req.ID, gin.H{
		"supportedVersions": mcpSupportedVersions,
		"capabilities":      gin.H{"tools": gin.H{"listChanged": false}},
		"serverInfo":        gin.H{"name": mcpServerName, "title": "Elysia API Gateway Agent", "version": mcpServerVersion},
		"instructions":      "通过 agent_* 工具驱动网关内置 AI 助手（会话/消息/审批三原语）。",
		"resultType":        "complete",
	}), http.StatusOK)
}

// mcpHandleToolsList 全量单页返回（工具数少，不实现 cursor）。
func (s *Server) mcpHandleToolsList(c *gin.Context, req jsonrpcRequest, era mcpEra) {
	definitions := make([]gin.H, 0)
	for _, tool := range mcpToolset() {
		definitions = append(definitions, gin.H{
			"name": tool.name, "title": tool.title, "description": tool.description,
			"inputSchema": tool.schema,
			"annotations": gin.H{"title": tool.title},
		})
	}
	result := gin.H{"tools": definitions}
	if era == mcpEraModern {
		result["resultType"] = "complete"
		result["ttlMs"] = 300_000
		result["cacheScope"] = "user"
	}
	mcpWriteJSON(c, jsonrpcOK(req.ID, result), http.StatusOK)
}

// mcpHandleToolsCall 工具调用：短工具纯 JSON 响应；流式工具（send/respond）
// 回 SSE——progress 通知逐帧推送，最后一帧是 JSON-RPC response。
func (s *Server) mcpHandleToolsCall(c *gin.Context, req jsonrpcRequest, era mcpEra) {
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
		mcpWriteJSON(c, mcpToolCallResult(req.ID, era, result, err), http.StatusOK)
		return
	}
	// 流式路径：SSE 响应。Modern 语义下客户端断开 SSE 即取消——尽力停止
	// 轮次（引擎侧 Stop 最多等 15s 收尾，这里异步执行不阻塞出口）。
	s.mcpStreamToolCall(c, req, era, tool, params.Arguments, params.Meta.ProgressToken)
}

// mcpToolCallResult 组装工具调用的响应（业务失败 → isError:true 而非
// JSON-RPC error，让客户端模型可自我纠正）。
func mcpToolCallResult(id json.RawMessage, era mcpEra, result any, err error) jsonrpcResponse {
	content := gin.H{"type": "text", "text": "done"}
	if err != nil {
		content = gin.H{"type": "text", "text": err.Error()}
	}
	payload := gin.H{"content": []gin.H{content}, "isError": err != nil}
	if err == nil {
		payload["structuredContent"] = result
	}
	if era == mcpEraModern {
		payload["resultType"] = "complete"
	}
	return jsonrpcOK(id, payload)
}

// mcpStreamToolCall 流式工具的 SSE 响应：progress 通知（progressToken 存在
// 时）→ 终帧 JSON-RPC response（含 structuredContent）。
func (s *Server) mcpStreamToolCall(c *gin.Context, req jsonrpcRequest, era mcpEra, tool *mcpTool, args json.RawMessage, progressToken json.RawMessage) {
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
	done := make(chan struct{})
	go func() {
		select {
		case <-c.Request.Context().Done():
			// Modern：断流即取消。从参数里抠 sessionId 尽力停轮次（引擎侧
			// 收尾最多 15s，异步执行不阻塞出口）。
			var probe struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(args, &probe) == nil && probe.SessionID != "" {
				go s.stopRemoteTurn(probe.SessionID)
			}
		case <-done:
		}
	}()
	defer close(done)
	result, err := tool.invoke(c.Request.Context(), s, args, progress)
	write(mcpToolCallResult(req.ID, era, result, err))
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
