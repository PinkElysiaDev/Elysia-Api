package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

func (caller *agentStreamCaller) callBoundProtocol(ctx context.Context, input agent.CallRequest, model storage.Model, legacy *relay.MaheshvaraRequest, callbacks agent.StreamCallbacks) (*agent.CallResult, bool, error) {
	bindings, err := caller.server.store.ListProtocolBindings(ctx)
	if err != nil {
		return nil, false, err
	}
	ref := config.ModelRef{ID: model.ID, Name: model.Name, SourceID: model.SourceID, BaseURL: model.BaseURL, APIKey: model.APIKey, Platform: model.Platform, ToolsCapable: model.ToolsCapable, VisionCapable: model.VisionCapable}
	entry, isBound := selectProtocolBinding(bindings, ref)
	if !isBound {
		if caller.server.isProtocolRuntimeRequired.Load() {
			return nil, true, gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/binding", "Agent model requires a verified protocol binding")
		}
		return nil, false, nil
	} // Removed after atomic legacy migration.
	service, err := caller.server.protocolService()
	if err != nil {
		return nil, true, err
	}
	if !model.ToolsCapable || !entry.Binding.Capabilities[protocol.FunctionToolsCapability] {
		return nil, true, gatewayIssue(protocol.Identity{}, protocol.UnsupportedCapability, "/binding/capabilities/tools.function", "Agent requires a model and protocol binding supporting function tools")
	}
	compiled, _ := service.Pin(entry.Binding.ProtocolID)
	if err := protocol.IssuesError(protocol.CheckBinding(entry.Binding, compiled)); err != nil {
		return nil, true, err
	}
	transport := protocol.HTTPJSON
	if entry.Binding.Operation != "" {
		transport = compiled.Operations()[entry.Binding.Operation].Transport
	} else {
		for _, preferred := range []protocol.Transport{protocol.SSE, protocol.NDJSON, protocol.HTTPJSON} {
			if slices.Contains(entry.Binding.Transports, preferred) {
				transport = preferred
				break
			}
		}
	}
	candidate, failure := makeGatewayCandidate(service.View(), bindings, ref, transport, "generate")
	if failure != nil {
		return nil, true, failure
	}
	if candidate.operation.Kind != "generate" {
		return nil, true, fmt.Errorf("Agent requires a synchronous generation operation")
	}
	legacy.Stream = transport != protocol.HTTPJSON
	request, err := relay.SnapshotProtocolRequest(legacy, protocol.Identity{Family: "elysia-agent", WireVersion: "1", DefinitionID: "agent", Revision: "1"}, candidate.scope)
	if err != nil {
		return nil, true, err
	}
	started := time.Now()
	logConfig := caller.server.usageLogConfig()
	record := &usageRecord{RequestID: usageRequestID(started), StartedAt: started, KeyName: AgentUsageKeyName, RequestedModelGroup: input.Model, ModelName: model.Name, SourceID: model.SourceID, Platform: model.Platform, TargetFormat: entry.Binding.ProtocolID, UpstreamRevision: compiled.Hash(), RelayMode: agentRelayMode, Stream: legacy.Stream, StatusCode: http.StatusOK, bodyOpts: usageBodyOptions{maxBytes: logConfig.BodyMaxBytes, externalize: logConfig.ExternalizeMedia}}
	record.assets = newAssetSink(record.RequestID)
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
	if err != nil {
		setUsageError(record, ctx, err)
		return nil, true, err
	}
	result, err := agentResultFromProtocol(response)
	if err != nil {
		setUsageError(record, ctx, err)
		return nil, true, err
	}
	if !legacy.Stream {
		if callbacks.OnText != nil && result.Text != "" {
			callbacks.OnText(result.Text)
		}
		if callbacks.OnReasoning != nil && result.Reasoning != "" {
			callbacks.OnReasoning(result.Reasoning)
		}
	}
	return result, true, nil
}

func agentResultFromProtocol(response *protocol.Response) (*agent.CallResult, error) {
	result := &agent.CallResult{}
	var text, reasoning strings.Builder
	var collect func([]protocol.Node) error
	collect = func(nodes []protocol.Node) error {
		for _, node := range nodes {
			switch node.Kind {
			case protocol.MessageNode:
				if err := collect(node.Children); err != nil {
					return err
				}
			case protocol.TextNode, protocol.ReasoningNode:
				var value string
				if err := node.Payload.Decode(&value); err != nil {
					return err
				}
				if node.Kind == protocol.TextNode {
					text.WriteString(value)
				} else {
					reasoning.WriteString(value)
				}
			case protocol.ToolCallNode:
				if node.Input == nil || node.Input.Kind != protocol.JSONInput {
					return fmt.Errorf("Agent tools require JSON function arguments")
				}
				var call relay.MaheshvaraToolCall
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
	if usage := response.Usage; usage != nil {
		fields := map[string]int64{}
		for name, count := range map[string]*protocol.Counter{"input_tokens": usage.Input, "output_tokens": usage.Output, "total_tokens": usage.Total, "cached_input_tokens": usage.CacheRead, "cache_creation_input_tokens": usage.CacheCreation} {
			if count != nil {
				fields[name] = count.Count
			}
		}
		body, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &result.Usage); err != nil {
			return nil, err
		}
	}
	return result, nil
}
