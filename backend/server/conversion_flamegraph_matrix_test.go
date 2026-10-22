package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func flameMatrixProtocol(t *testing.T, name string) *protocol.Compiled {
	t.Helper()
	return compileFixtureDefinition(t, presetDefinition(t, name))
}

func flameMatrixProvider(t *testing.T, target *protocol.Compiled) http.Handler {
	frames, body := flameMatrixResponse(t, target)
	var expected *protocol.Request
	var expectedText string
	if workload := os.Getenv("ELYSIA_FLAME_REQUEST"); workload != "" {
		expected = loadRequest(t, workload)
		expectedText = flameRequestText(expected.Content)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		input, err := target.DecodeRequest(r.Context(), raw, protocol.EvaluationContext{Scope: protocol.Scope{Model: "m"}})
		if err != nil || len(input.Content) == 0 {
			t.Errorf("matrix upstream input: %v", err)
			w.WriteHeader(400)
			return
		}
		if expected != nil {
			valid := flameRequestText(input.Content) == expectedText && len(input.Tools) == len(expected.Tools)
			if valid {
				for i, tool := range input.Tools {
					valid = valid && tool.Name == expected.Tools[i].Name && !tool.InputSchema.IsZero()
				}
			}
			if !valid {
				t.Error("matrix upstream request lost history or tools")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		var isStream bool
		_ = input.Parameters["stream"].Decode(&isStream)
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			isStream = true
		}
		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			w.Write(frame)
			w.(http.Flusher).Flush()
		}
	})
}

func flameRequestText(nodes []protocol.Node) string {
	var result strings.Builder
	var visit func([]protocol.Node)
	visit = func(nodes []protocol.Node) {
		for _, node := range nodes {
			if node.Kind == protocol.TextNode {
				var text string
				_ = node.Payload.Decode(&text)
				result.WriteString(text)
			}
			visit(node.Children)
		}
	}
	visit(nodes)
	return result.String()
}

func flameMatrixResponse(t *testing.T, compiled *protocol.Compiled) ([][]byte, []byte) {
	t.Helper()
	usage := &protocol.Usage{Input: &protocol.Counter{Count: 100, Origin: protocol.ObservedCount}, Output: &protocol.Counter{Count: 5, Origin: protocol.ObservedCount}, Total: &protocol.Counter{Count: 105, Origin: protocol.ObservedCount}}
	attributes := protocol.Object{"created_at": mustProtocolValue(t, "1"), "finishReason": protocol.StringValue("stop")}
	response := &protocol.Response{SchemaVersion: 1, ID: protocol.StringValue("r"), Model: protocol.StringValue("m"), Status: protocol.StringValue("completed"), Content: []protocol.Node{{Kind: protocol.MessageNode, ID: protocol.StringValue("msg"), Role: protocol.StringValue("assistant"), Status: protocol.StringValue("completed"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("ok")}}}}, Usage: usage, Attributes: attributes}
	if compiled.Identity().DefinitionID != "openai-responses" {
		response.Content[0].ID = protocol.Value{}
		response.Content[0].Status = protocol.Value{}
	}
	body, err := compiled.EncodeResponse(t.Context(), response, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	identity := &protocol.Response{SchemaVersion: 1, ID: response.ID, Model: response.Model, Status: protocol.StringValue("in_progress"), Usage: &protocol.Usage{Input: usage.Input, Output: &protocol.Counter{Count: 0, Origin: protocol.ObservedCount}}, Attributes: protocol.Object{"created_at": mustProtocolValue(t, "1")}}
	events := []protocol.Event{{SchemaVersion: 1, Type: protocol.ResponseStarted, Response: identity}, {SchemaVersion: 1, Type: protocol.ItemStarted, ItemID: protocol.StringValue("text"), Index: &index, Item: &protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue("")}}}
	frameCount, frameBytes, err := flameStreamShape()
	if err != nil {
		t.Fatal(err)
	}
	for range frameCount {
		event := protocol.Event{SchemaVersion: 1, Type: protocol.ItemDelta, ItemID: protocol.StringValue("text"), Index: &index, Delta: protocol.StringValue(strings.Repeat("x", frameBytes))}
		if os.Getenv("ELYSIA_REVIEW_LOGPROBS") == "1" {
			event.Metadata = []protocol.ResponseMetadata{{Codec: "openai-chat", Location: "choice", Name: "logprobs", Value: mustProtocolValue(t, `{"content":[{"token":"x","logprob":-0.5,"bytes":[120],"top_logprobs":[]}]}`)}}
		}
		events = append(events, event)
	}
	events = append(events, protocol.Event{SchemaVersion: 1, Type: protocol.ItemFinished, ItemID: protocol.StringValue("text"), Index: &index}, protocol.Event{SchemaVersion: 1, Type: protocol.ResponseFinished, Response: &protocol.Response{SchemaVersion: 1, ID: response.ID, Model: response.Model, Status: protocol.StringValue("completed"), Content: []protocol.Node{}, Usage: usage, Attributes: attributes}}, protocol.Event{SchemaVersion: 1, Type: protocol.UsageUpdated, Usage: usage})
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	var frames [][]byte
	write := func(values []protocol.Value, err error) {
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			var buffer bytes.Buffer
			if err := protocol.WriteFrame(&buffer, compiled.Operations()["stream"], value); err != nil {
				t.Fatal(err)
			}
			frames = append(frames, buffer.Bytes())
		}
	}
	for _, event := range events {
		write(compiled.EncodeFrames(t.Context(), event, options))
	}
	write(compiled.FinishEvents(t.Context(), options))
	return frames, body
}

func flameMatrixRequest(t *testing.T, source *protocol.Compiled, stream bool) (string, string) {
	t.Helper()
	workload := os.Getenv("ELYSIA_FLAME_REQUEST")
	if workload == "" {
		workload = "short"
	}
	switch workload {
	case "short", "prefix32k", "history256k", "tools32":
	default:
		t.Fatal("invalid matrix request workload")
	}
	request := loadRequest(t, workload)
	request.Parameters["stream"] = mustProtocolValue(t, fmt.Sprintf("%t", stream))
	if os.Getenv("ELYSIA_FLAME_MATRIX_TOOLS") == "1" {
		request = loadRequest(t, "tools32")
		request.Parameters["stream"] = mustProtocolValue(t, fmt.Sprintf("%t", stream))
	}
	if source.Identity().DefinitionID == "openai-chat-completions" && stream {
		output, err := protocol.ParseClientOutput(mustProtocolValue(t, `{"include_usage":true}`))
		if err != nil {
			t.Fatal(err)
		}
		request.ClientOutput = output
	}
	wire, err := source.EncodeRequest(t.Context(), request, protocol.EvaluationContext{Scope: protocol.Scope{Model: "grp"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/messages"
	switch source.Identity().DefinitionID {
	case "openai-chat-completions":
		path = "/v1/chat/completions"
	case "openai-responses":
		path = "/v1/responses"
	case "google-generate-content":
		path = "/v1beta/models/grp:generateContent"
		if stream {
			path = "/v1beta/models/grp:streamGenerateContent?alt=sse"
		}
	}
	return string(wire), path
}

func validateFlameMatrixOutput(ctx context.Context, source *protocol.Compiled, body []byte, stream bool) error {
	expected := "ok"
	if stream {
		frames, width, err := flameStreamShape()
		if err != nil {
			return err
		}
		expected = strings.Repeat("x", frames*width)
	}
	return validateFlameOutput(ctx, source, body, stream, expected, 100, 5)
}

func flameStreamShape() (int, int, error) {
	values := []int{16, 4096}
	for i, name := range []string{"ELYSIA_FLAME_FRAMES", "ELYSIA_FLAME_FRAME_BYTES"} {
		if text := os.Getenv(name); text != "" {
			value, err := strconv.Atoi(text)
			if err != nil || value < 1 || value > 4096 {
				return 0, 0, fmt.Errorf("invalid %s", name)
			}
			values[i] = value
		}
	}
	return values[0], values[1], nil
}

func validateFlameOutput(ctx context.Context, source *protocol.Compiled, body []byte, stream bool, expected string, inputCount, outputCount int64) error {
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState(), Scope: protocol.Scope{Model: "grp"}}
	var response *protocol.Response
	var err error
	if !stream {
		response, err = source.DecodeResponse(ctx, body, options)
	} else {
		collector, createErr := protocol.NewResponseCollector(protocol.Target{Protocol: source.Identity(), Direction: protocol.EncodeEvent, Capabilities: source.Capabilities(protocol.EncodeEvent)}, protocol.DefaultLimits())
		if createErr != nil {
			return createErr
		}
		err = protocol.ReadFrames(ctx, bytes.NewReader(body), source.Operations()["stream"], 8<<20, func(frame protocol.Value, headers protocol.Object) error {
			decoded, decodeErr := source.DecodeFrame(ctx, frame, options)
			if decodeErr != nil {
				return decodeErr
			}
			for _, event := range decoded.Events {
				if _, _, consumeErr := collector.Consume(event); consumeErr != nil {
					return consumeErr
				}
			}
			return nil
		})
		if err == nil {
			response, err = collector.Finish()
		}
	}
	if err != nil {
		return err
	}
	if response == nil || response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != inputCount || response.Usage.Output == nil || response.Usage.Output.Count != outputCount || len(response.Content) == 0 {
		return fmt.Errorf("matrix output content or usage lost")
	}
	var text strings.Builder
	var collect func([]protocol.Node) error
	collect = func(nodes []protocol.Node) error {
		for _, node := range nodes {
			if node.Kind == protocol.TextNode {
				var value string
				if err := node.Payload.Decode(&value); err != nil {
					return err
				}
				text.WriteString(value)
			}
			if err := collect(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(response.Content); err != nil {
		return err
	}
	if text.String() != expected {
		return fmt.Errorf("response text differs: received %d bytes, expected %d", text.Len(), len(expected))
	}
	if os.Getenv("ELYSIA_REVIEW_LOGPROBS") == "1" {
		count := 0
		var visit func([]protocol.Node)
		visit = func(nodes []protocol.Node) {
			for _, node := range nodes {
				for _, metadata := range node.Metadata {
					if metadata.Name == "logprobs" {
						fields, _ := metadata.Value.ReadObject()
						var entries []protocol.Value
						if err := fields["content"].Decode(&entries); err == nil {
							count += len(entries)
						}
					}
				}
				visit(node.Children)
			}
		}
		visit(response.Content)
		frames, _, _ := flameStreamShape()
		if count != frames {
			return fmt.Errorf("logprobs count differs: got %d, expected %d", count, frames)
		}
	}
	return nil
}
