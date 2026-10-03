package server

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

type liveStreamEvidence struct {
	Frames       int                        `json:"frames"`
	Events       map[protocol.EventType]int `json:"events"`
	Extensions   []string                   `json:"extensionPaths,omitempty"`
	NativeReplay bool                       `json:"nativeReplay"`
}

// inspectLiveStream checks the forwarding contract, including native frames.
// The returned response is only a projection for usage/tool assertions, not a
// conversion of the full event stream into an Agent response. Extensions remain
// in the checked frames and are recorded separately, never cleared for a codec.
func inspectLiveStream(compiled *protocol.Compiled, raw []byte) (*protocol.Response, *liveStreamEvidence, error) {
	ctx := context.Background()
	replay, err := protocol.NewEventReplay(protocol.Target{Protocol: compiled.Identity(), Direction: protocol.EncodeEvent, Scope: liveInspectionScope(), Capabilities: compiled.Capabilities(protocol.EncodeEvent)}, compiled.ResourceLimits())
	if err != nil {
		return nil, nil, err
	}
	evidence := &liveStreamEvidence{Events: map[protocol.EventType]int{}, NativeReplay: true}
	response := &protocol.Response{SchemaVersion: protocol.SemanticSchemaVersion}
	decodeOptions := protocol.EvaluationContext{State: protocol.NewEvaluationState(), Scope: liveInspectionScope()}
	encodeOptions := protocol.EvaluationContext{State: protocol.NewEvaluationState(), Scope: liveInspectionScope()}
	identities := protocol.NewItemIdentities(compiled.ResourceLimits().StateItems)
	items := map[string]*protocol.Node{}
	arguments := map[string]*strings.Builder{}
	err = protocol.ReadFrames(ctx, bytes.NewReader(raw), liveOperation(compiled, true), liveBodyLimit, func(value protocol.Value, metadata protocol.Object) error {
		decodeOptions.Values = metadata
		frame, err := compiled.DecodeFrame(ctx, value, decodeOptions)
		if err != nil {
			return err
		}
		evidence.Frames++
		for _, event := range frame.Events {
			isAccepted, err := replay.Consume(event)
			if err != nil {
				return err
			}
			if !isAccepted {
				continue
			}
			evidence.Events[event.Type]++
			if event.Unmapped != nil {
				fields, err := event.Unmapped.Value.ReadObject()
				if err != nil {
					return err
				}
				for path := range fields {
					evidence.Extensions = append(evidence.Extensions, path)
				}
			}
			if event.Type == protocol.OperationFailed || event.Type == protocol.OperationCancelled {
				return fmt.Errorf("provider ended with %s", event.Type)
			}
			if event.Response != nil {
				if err := protocol.CheckGenerationOutcome(event.Response); err != nil {
					return err
				}
			}
			if err := collectLiveTool(event, identities, items, arguments, response); err != nil {
				return err
			}
		}
		frames, err := compiled.EncodeFrame(ctx, frame, encodeOptions)
		if err != nil {
			return err
		}
		if len(frames) != 1 || !bytes.Equal(frames[0].Bytes(), value.Bytes()) {
			evidence.NativeReplay = false
			return fmt.Errorf("same-wire stream frame changed")
		}
		return nil
	})
	if err == nil {
		err = replay.Finish()
	}
	response.Usage = replay.Usage()
	return response, evidence, err
}

func collectLiveTool(event protocol.Event, identities *protocol.ItemIdentities, items map[string]*protocol.Node, arguments map[string]*strings.Builder, response *protocol.Response) error {
	if event.Type == protocol.ResponseFinished {
		for key, input := range arguments {
			if err := finishLiveTool(items[key], input, response); err != nil {
				return err
			}
			delete(arguments, key)
		}
		return nil
	}

	if event.Type != protocol.ItemStarted && event.Type != protocol.ItemDelta && event.Type != protocol.ItemSnapshot && event.Type != protocol.ItemFinished {
		return nil
	}
	key, err := identities.Resolve(event)
	if err != nil {
		return err
	}
	if event.Type == protocol.ItemStarted && event.Item != nil && event.Item.Kind == protocol.ToolCallNode {
		item := *event.Item
		items[key], arguments[key] = &item, &strings.Builder{}
	}
	item := items[key]
	if item == nil {
		return nil
	}
	if event.Item != nil {
		if !event.Item.Name.IsZero() {
			item.Name = event.Item.Name
		}
		if !event.Item.CallID.IsZero() {
			item.CallID = event.Item.CallID
		}
	}
	if !event.CallID.IsZero() {
		item.CallID = event.CallID
	}
	if event.Type == protocol.ItemDelta && !event.Delta.IsZero() {
		var delta string
		if err := event.Delta.Decode(&delta); err != nil {
			return err
		}
		arguments[key].WriteString(delta)
	} else if event.Item != nil && event.Item.Input != nil && !event.Item.Input.Value.IsZero() {
		arguments[key].Reset()
		if event.Item.Input.Kind == protocol.JSONInput {
			arguments[key].Write(event.Item.Input.Value.Bytes())
		} else {
			var value string
			if err := event.Item.Input.Value.Decode(&value); err != nil {
				return err
			}
			arguments[key].WriteString(value)
		}
	}
	if event.Type == protocol.ItemFinished {
		if err := finishLiveTool(item, arguments[key], response); err != nil {
			return err
		}
		delete(arguments, key)
	}

	return nil
}

func finishLiveTool(item *protocol.Node, input *strings.Builder, response *protocol.Response) error {
	value := protocol.StringValue(input.String())
	if item.Input.Kind == protocol.JSONInput {
		var err error
		value, err = protocol.ParseValue([]byte(input.String()))
		if err != nil {
			return err
		}
	}
	item.Input = &protocol.ToolInput{Kind: item.Input.Kind, Value: value}
	response.Content = append(response.Content, *item)
	return nil
}
