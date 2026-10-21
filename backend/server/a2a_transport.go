package server

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"strings"
	"time"

	legacy "github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/errordetails"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/elysia-api/backend/protocol"
	"github.com/rs/zerolog"
)

func a2aVersion(ctx context.Context, c *app.RequestContext) (a2a.ProtocolVersion, bool) {
	switch strings.TrimSpace(string(c.GetHeader("A2A-Version"))) {
	case "", "0.3":
		return a2av0.Version, true
	case "1.0":
		return a2a.Version, true
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, utils.H{"error": "unsupported A2A-Version (supported: 0.3, 1.0)"})
		return "", false
	}
}

func (s *Server) handleA2A(ctx context.Context, c *app.RequestContext) {
	version, ok := a2aVersion(ctx, c)
	if !ok {
		return
	}
	body, err := protocol.ReadBoundedBody(requestBody(c), agentMaxBodyBytes)
	if err != nil {
		status := http.StatusBadRequest
		var limit *protocol.ResourceLimitError
		if errors.As(err, &limit) {
			status = http.StatusRequestEntityTooLarge
		}
		c.String(status, "%s", err)
		return
	}
	c.Header("X-Accel-Buffering", "no")
	serveA2ARPC(ctx, c, s.a2aService(), version, body)
}

type a2aRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type a2aRPCError struct {
	Code    int                   `json:"code"`
	Message string                `json:"message"`
	Data    []*errordetails.Typed `json:"data,omitempty"`
}
type a2aRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *a2aRPCError    `json:"error,omitempty"`
}

func a2aRPCResult(id json.RawMessage, result any, err error) a2aRPCResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	response := a2aRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
	if err == nil {
		if result == nil {
			response.Result = json.RawMessage("null")
		}
		return response
	}
	code := -32603
	for _, entry := range []struct {
		code int
		err  error
	}{
		{-32700, a2a.ErrParseError}, {-32600, a2a.ErrInvalidRequest}, {-32601, a2a.ErrMethodNotFound}, {-32602, a2a.ErrInvalidParams},
		{-32001, a2a.ErrTaskNotFound}, {-32002, a2a.ErrTaskNotCancelable}, {-32003, a2a.ErrPushNotificationNotSupported},
		{-32004, a2a.ErrUnsupportedOperation}, {-32005, a2a.ErrUnsupportedContentType}, {-32006, a2a.ErrInvalidAgentResponse},
		{-32007, a2a.ErrExtendedCardNotConfigured},
		{-32000, a2a.ErrServerError}, {-32008, a2a.ErrExtensionSupportRequired}, {-32009, a2a.ErrVersionNotSupported},
		{-31401, a2a.ErrUnauthenticated}, {-31403, a2a.ErrUnauthorized},
	} {
		if errors.Is(err, entry.err) {
			code = entry.code
			break
		}
	}
	response.Result, response.Error = nil, &a2aRPCError{Code: code, Message: err.Error()}
	var detail *a2a.Error
	hasReason := false
	if errors.As(err, &detail) {
		if len(detail.Details) > 0 {
			response.Error.Data = append(response.Error.Data, errordetails.NewFromStruct(detail.Details))
		}
		for _, value := range detail.TypedDetails {
			if value == nil {
				continue
			}
			hasReason = hasReason || value.TypeURL == errordetails.ErrorInfoType
			response.Error.Data = append(response.Error.Data, value)
		}
	}
	if !hasReason {
		response.Error.Data = append(response.Error.Data, errordetails.NewErrorInfo(a2a.ErrorReason(err), a2a.ProtocolDomain, nil))
	}
	return response
}

func serveA2ARPC(ctx context.Context, c *app.RequestContext, handler a2asrv.RequestHandler, version a2a.ProtocolVersion, body []byte) {
	var request a2aRPCRequest
	if err := json.Unmarshal(body, &request); err != nil {
		failure := a2a.ErrParseError
		if json.Valid(body) {
			failure = a2a.ErrInvalidRequest
		}
		c.JSON(200, a2aRPCResult(nil, nil, failure))
		return
	}
	var id any
	if json.Unmarshal(request.ID, &id) != nil {
		c.JSON(200, a2aRPCResult(nil, nil, a2a.ErrInvalidRequest))
		return
	}
	switch id.(type) {
	case string, float64, nil:
	default:
		c.JSON(200, a2aRPCResult(nil, nil, a2a.ErrInvalidRequest))
		return
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		c.JSON(200, a2aRPCResult(request.ID, nil, a2a.ErrInvalidRequest))
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	metadata := make(map[string][]string)
	c.Request.Header.VisitAll(func(key, value []byte) {
		name := string(key)
		metadata[name] = append(metadata[name], string(value))
	})
	// SDK interceptors and handlers obtain service parameters through its
	// documented transport extension point, never through a Hertz context.
	ctx, _ = a2asrv.NewCallContext(ctx, a2asrv.NewServiceParams(metadata))
	result, events, err := dispatchA2ARPC(ctx, handler, version, request.Method, request.Params)
	if events == nil {
		c.JSON(200, a2aRPCResult(request.ID, result, err))
		return
	}
	w := startSSE(c)
	streamCtx := ctx
	// Iterator execution may block waiting for an Agent event. A bounded handoff
	// permits heartbeats while keeping all network access on this goroutine.
	results := make(chan a2aRPCResponse)
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		defer close(results)
		defer func() {
			if recovered := recover(); recovered != nil {
				zerolog.Ctx(ctx).Error().Interface("panic", recovered).Msg("A2A stream panic")
				select {
				case <-streamCtx.Done():
				case results <- a2aRPCResult(request.ID, nil, a2a.ErrInternalError):
				}
			}
		}()
		for event, err := range events {
			var value any
			if err == nil {
				if version == a2av0.Version {
					value, err = a2av0.FromV1Event(event)
				} else {
					value = a2a.StreamResponse{Event: event}
				}
			}
			select {
			case <-streamCtx.Done():
				return
			case results <- a2aRPCResult(request.ID, value, err):
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.WriteKeepAlive() != nil {
				return
			}
		case response, ok := <-results:
			if !ok {
				return
			}
			encoded, err := json.Marshal(response)
			if err != nil || w.WriteEvent("", "", encoded) != nil {
				return
			}
		}
	}
}

func decodeA2AParams[T any](raw json.RawMessage) (*T, error) {
	var value T
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return nil, a2a.ErrInvalidParams
	}
	return &value, nil
}

// The binding depends on the SDK's transport-independent interface, never on
// the application's task store, engine or concrete service implementation.
func dispatchA2ARPC(ctx context.Context, h a2asrv.RequestHandler, version a2a.ProtocolVersion, method string, raw json.RawMessage) (any, iter.Seq2[a2a.Event, error], error) {
	old := version == a2av0.Version
	if old {
		switch method {
		case "message/send", "message/stream":
			params, err := decodeA2AParams[legacy.MessageSendParams](raw)
			if err != nil {
				return nil, nil, err
			}
			req, err := a2av0.ToV1SendMessageRequest(params)
			if err != nil {
				return nil, nil, a2a.ErrInvalidParams
			}
			if method == "message/stream" {
				return nil, h.SendStreamingMessage(ctx, req), nil
			}
			result, err := h.SendMessage(ctx, req)
			if err != nil {
				return nil, nil, err
			}
			out, err := a2av0.FromV1Event(result)
			return out, nil, err
		case "tasks/get":
			params, err := decodeA2AParams[legacy.TaskQueryParams](raw)
			if err != nil {
				return nil, nil, err
			}
			result, err := h.GetTask(ctx, a2av0.ToV1GetTaskRequest(params))
			if err != nil {
				return nil, nil, err
			}
			return a2av0.FromV1Task(result), nil, nil
		case "tasks/cancel", "tasks/resubscribe":
			params, err := decodeA2AParams[legacy.TaskIDParams](raw)
			if err != nil {
				return nil, nil, err
			}
			if method == "tasks/resubscribe" {
				return nil, h.SubscribeToTask(ctx, a2av0.ToV1SubscribeToTaskRequest(params)), nil
			}
			result, err := h.CancelTask(ctx, a2av0.ToV1CancelTaskRequest(params))
			if err != nil {
				return nil, nil, err
			}
			return a2av0.FromV1Task(result), nil, nil
		case "tasks/pushNotificationConfig/get":
			req, err := decodeA2AParams[legacy.GetTaskPushConfigParams](raw)
			if err != nil {
				return nil, nil, err
			}
			result, err := h.GetTaskPushConfig(ctx, a2av0.ToV1GetTaskPushConfigRequest(req))
			if err != nil {
				return nil, nil, err
			}
			return a2av0.FromV1PushConfig(result), nil, nil
		case "tasks/pushNotificationConfig/set":
			req, err := decodeA2AParams[legacy.TaskPushConfig](raw)
			if err != nil {
				return nil, nil, err
			}
			result, err := h.CreateTaskPushConfig(ctx, a2av0.ToV1PushConfig(req))
			if err != nil {
				return nil, nil, err
			}
			return a2av0.FromV1PushConfig(result), nil, nil
		case "tasks/pushNotificationConfig/list":
			req, err := decodeA2AParams[legacy.ListTaskPushConfigParams](raw)
			if err != nil {
				return nil, nil, err
			}
			result, err := h.ListTaskPushConfigs(ctx, a2av0.ToV1ListTaskPushConfigRequest(req))
			if err != nil {
				return nil, nil, err
			}
			return a2av0.FromV1PushConfigs(result.Configs), nil, nil
		case "tasks/pushNotificationConfig/delete":
			req, err := decodeA2AParams[legacy.DeleteTaskPushConfigParams](raw)
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, h.DeleteTaskPushConfig(ctx, a2av0.ToV1DeleteTaskPushConfigRequest(req))
		case "agent/getAuthenticatedExtendedCard":
			result, err := h.GetExtendedAgentCard(ctx, &a2a.GetExtendedAgentCardRequest{})
			if err != nil {
				return nil, nil, err
			}
			return a2av0.FromV1AgentCard(result), nil, nil
		default:
			return nil, nil, a2a.ErrMethodNotFound
		}
	}
	switch method {
	case "SendMessage", "SendStreamingMessage":
		req, err := decodeA2AParams[a2a.SendMessageRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		if method == "SendStreamingMessage" {
			return nil, h.SendStreamingMessage(ctx, req), nil
		}
		result, err := h.SendMessage(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		return a2a.StreamResponse{Event: result}, nil, nil
	case "GetTask":
		req, err := decodeA2AParams[a2a.GetTaskRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		result, err := h.GetTask(ctx, req)
		return result, nil, err
	case "ListTasks":
		req, err := decodeA2AParams[a2a.ListTasksRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		result, err := h.ListTasks(ctx, req)
		return result, nil, err
	case "CancelTask":
		req, err := decodeA2AParams[a2a.CancelTaskRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		result, err := h.CancelTask(ctx, req)
		return result, nil, err
	case "SubscribeToTask":
		req, err := decodeA2AParams[a2a.SubscribeToTaskRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		return nil, h.SubscribeToTask(ctx, req), nil
	case "GetTaskPushNotificationConfig":
		req, err := decodeA2AParams[a2a.GetTaskPushConfigRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		result, err := h.GetTaskPushConfig(ctx, req)
		return result, nil, err
	case "CreateTaskPushNotificationConfig":
		req, err := decodeA2AParams[a2a.PushConfig](raw)
		if err != nil {
			return nil, nil, err
		}
		result, err := h.CreateTaskPushConfig(ctx, req)
		return result, nil, err
	case "ListTaskPushNotificationConfigs":
		req, err := decodeA2AParams[a2a.ListTaskPushConfigRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		result, err := h.ListTaskPushConfigs(ctx, req)
		return result, nil, err
	case "DeleteTaskPushNotificationConfig":
		req, err := decodeA2AParams[a2a.DeleteTaskPushConfigRequest](raw)
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, h.DeleteTaskPushConfig(ctx, req)
	case "GetExtendedAgentCard":
		result, err := h.GetExtendedAgentCard(ctx, &a2a.GetExtendedAgentCardRequest{})
		return result, nil, err
	default:
		return nil, nil, a2a.ErrMethodNotFound
	}
}
