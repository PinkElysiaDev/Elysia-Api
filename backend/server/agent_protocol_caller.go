package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/protocol"

	"github.com/elysia-api/backend/storage"
)

func (caller *agentStreamCaller) callBoundProtocol(ctx context.Context, input agent.CallRequest, model storage.Model, callbacks agent.StreamCallbacks) (*agent.CallResult, error) {
	bindings, err := caller.server.store.ListProtocolBindings(ctx)
	if err != nil {
		return nil, err
	}
	ref := modelReference(model)
	service, err := caller.server.protocolService()
	if err != nil {
		return nil, err
	}
	view := service.View()
	readiness := checkAgentModel(view, bindings, model)
	if !readiness.Available {
		return nil, agentReadinessError{readiness}
	}
	entry, _ := selectProtocolBinding(bindings, ref)
	compiled, _ := view.Pin(entry.Binding.ProtocolID)
	transport := agentTransport(entry.Binding, compiled)
	candidate, failure := makeGatewayCandidate(view, bindings, ref, transport, "generate")
	if failure != nil {
		return nil, failure
	}
	if candidate.operation.Kind != "generate" {
		return nil, fmt.Errorf("Agent requires a synchronous generation operation")
	}
	content := append([]protocol.Node(nil), input.Content...)
	if input.Instructions != "" {
		content = append([]protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("system"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(input.Instructions)}}}}, content...)
	}
	preferences := input.Preferences
	preferences.MaxOutputTokens = agentStreamMaxOutputTokens
	preferences.Stream = transport != protocol.HTTPJSON
	request, err := compiled.BuildAgentRequest(ctx, protocol.Request{SchemaVersion: protocol.SemanticSchemaVersion, Source: protocol.AgentIdentity(), Model: protocol.StringValue(model.Name), Content: content, Tools: input.Tools}, preferences)
	if err != nil {
		return nil, err
	}
	isStream := transport != protocol.HTTPJSON
	started := time.Now()
	logConfig := caller.server.usageLogConfig()
	record := &usageRecord{RequestID: usageRequestID(started), StartedAt: started, KeyName: AgentUsageKeyName, RequestedModelGroup: input.Model, ModelName: model.Name, SourceID: model.SourceID, Platform: model.Platform, TargetFormat: entry.Binding.ProtocolID, UpstreamRevision: compiled.Hash(), RelayMode: agentRelayMode, Stream: isStream, StatusCode: http.StatusOK, bodyOpts: usageBodyOptions{maxBytes: logConfig.BodyMaxBytes, externalize: logConfig.ExternalizeMedia}}
	record.assets = newAssetSink(record.RequestID)
	if record.bodyOpts.maxBytes > 0 {
		body, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		record.IncomingBody = record.sanitizeBody(body)
	}
	defer func() {
		record.EndedAt = time.Now()
		record.DurationMs = record.EndedAt.Sub(started).Milliseconds()
		caller.server.recordUsage(record)
	}()
	callCtx, cancel := context.WithTimeout(ctx, caller.server.probeTimeout(agentCallTimeoutSec*time.Second))
	defer cancel()
	response, err := caller.server.collectProtocolGeneration(callCtx, candidate, request, record, func(kind protocol.NodeKind, delta string) {
		if record.FirstByteMs == 0 {
			record.FirstByteMs = time.Since(started).Milliseconds()
		}
		if kind == protocol.TextNode && callbacks.OnText != nil {
			callbacks.OnText(delta)
		}
		if kind == protocol.ReasoningNode && callbacks.OnReasoning != nil {
			callbacks.OnReasoning(delta)
		}
	})
	var result *agent.CallResult
	if response != nil {
		var projectionErr error
		result, projectionErr = agentResultFromProtocol(response)
		err = errors.Join(err, projectionErr)
	}
	if err != nil {
		setUsageError(record, ctx, err)
	}
	if record.bodyOpts.maxBytes > 0 {
		body, marshalErr := json.Marshal(struct {
			Result *agent.CallResult `json:"result"`
			Error  string            `json:"error,omitempty"`
		}{result, record.Error})
		if marshalErr != nil {
			return nil, errors.Join(err, marshalErr)
		}
		record.DownstreamResponse = record.sanitizeBody(body)
	}
	if err == nil && !isStream {
		if callbacks.OnText != nil && result.Text != "" {
			callbacks.OnText(result.Text)
		}
		if callbacks.OnReasoning != nil && result.Reasoning != "" {
			callbacks.OnReasoning(result.Reasoning)
		}
	}
	return result, err
}

func agentResultFromProtocol(response *protocol.Response) (*agent.CallResult, error) {
	result := &agent.CallResult{Content: response.Content, Usage: response.Usage}
	var text, reasoning strings.Builder
	var collect func([]protocol.Node) error
	collect = func(nodes []protocol.Node) error {
		for _, node := range nodes {
			switch node.Kind {
			case protocol.MessageNode:
				if err := collect(node.Children); err != nil {
					return err
				}
			case protocol.TextNode, protocol.ReasoningNode, protocol.RefusalNode:
				if node.Kind == protocol.ReasoningNode && node.Payload.IsZero() {
					for _, child := range node.Children {
						var summary string
						if child.Kind != protocol.TextNode {
							return fmt.Errorf("Agent reasoning summary requires text children")
						}
						if err := child.Payload.Decode(&summary); err != nil {
							return err
						}
						reasoning.WriteString(summary)
					}
					continue
				}
				var value string
				if err := node.Payload.Decode(&value); err != nil {
					return err
				}
				if node.Kind != protocol.ReasoningNode {
					text.WriteString(value)
				} else {
					reasoning.WriteString(value)
				}
			case protocol.ToolCallNode:
				if response.Status == protocol.StringValue("incomplete") {
					continue
				}
				if node.Input == nil || node.Input.Kind != protocol.JSONInput || !node.Input.Value.IsObject() {
					return fmt.Errorf("Agent tools require JSON function arguments")
				}
				var call agent.FunctionCall
				if err := node.CallID.Decode(&call.ID); err != nil {
					return err
				}
				if err := node.Name.Decode(&call.Name); err != nil {
					return err
				}
				call.Type, call.Arguments = "function", json.RawMessage(node.Input.Value.Bytes())
				result.ToolCalls = append(result.ToolCalls, call)
			default:
				return fmt.Errorf("Agent cannot consume response content kind %q", node.Kind)
			}
		}
		return nil
	}
	if err := collect(response.Content); err != nil {
		return nil, err
	}
	result.Text, result.Reasoning = text.String(), reasoning.String()
	return result, nil
}
