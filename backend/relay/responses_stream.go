package relay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// chatUsageToResponsesUsage 把上游（Chat Completions / Claude / Gemini）的 usage
// 重映射成 OpenAI Responses API 要求的结构。这是 codex "stream closed before
// response.completed" 1 秒断连的根因修复：codex 严格要求 response.completed.usage
// 一旦存在，就必须含整数 input_tokens / output_tokens / total_tokens；而 Chat
// Completions 上游给的是 prompt_tokens / completion_tokens，字段名不匹配会让
// codex 反序列化 ResponseCompletedUsage 失败 → 整个 ResponseCompleted 解析失败 →
// 立即断连。借鉴 cc-switch 的 chat_usage_to_responses_usage。
//
// 入参 raw 可为 nil（上游未返回 usage）：返回全 0 但结构完整的对象，绝不返回 nil
// 或缺字段，避免 codex 解析失败。
func chatUsageToResponsesUsage(raw any) map[string]any {
	usage, _ := raw.(map[string]any)

	pick := func(keys ...string) (int64, bool) {
		for _, k := range keys {
			if v, ok := usage[k]; ok {
				switch n := v.(type) {
				case float64:
					return int64(n), true
				case int64:
					return n, true
				case int:
					return int64(n), true
				case json.Number:
					if iv, err := n.Int64(); err == nil {
						return iv, true
					}
				}
			}
		}
		return 0, false
	}

	// prompt_tokens → input_tokens（已是 responses 命名也兼容）。
	inputTokens, _ := pick("prompt_tokens", "input_tokens", "promptTokenCount")
	// completion_tokens → output_tokens。
	outputTokens, _ := pick("completion_tokens", "output_tokens", "candidatesTokenCount")
	totalTokens, hasTotal := pick("total_tokens", "totalTokenCount")
	if !hasTotal {
		totalTokens = inputTokens + outputTokens
	}

	// reasoning tokens（若上游提供）放进 output_tokens_details；始终保证该字段存在。
	reasoningTokens := int64(0)
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		if v, ok := details["reasoning_tokens"]; ok {
			switch n := v.(type) {
			case float64:
				reasoningTokens = int64(n)
			case json.Number:
				if iv, err := n.Int64(); err == nil {
					reasoningTokens = iv
				}
			}
		}
	}

	result := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  totalTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": reasoningTokens,
		},
	}

	// 缓存命中：prompt_tokens_details.cached_tokens → input_tokens_details.cached_tokens。
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		if v, ok := details["cached_tokens"]; ok {
			result["input_tokens_details"] = map[string]any{"cached_tokens": v}
		}
	}

	return result
}

func ForwardResponsesStream(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		_, _ = writer.WriteString(line + "\n")
		if strings.TrimSpace(line) == "" {
			_ = writer.Flush()
		}
	}
	// 关键：循环结束后必须再 flush 一次。否则若上游最后一个事件（含
	// response.completed）后没有紧跟空行就 EOF，缓冲里的数据不会被推给下游，
	// 客户端会在收到 response.completed 之前就看到连接关闭
	// （codex 报 "stream closed before response.completed"）。
	_ = writer.Flush()
	return scanner.Err()
}

// fnCall 累积 Chat 流式里一个 tool_call 的状态，用于在 Responses 流中补齐
// output_item.added / function_call_arguments.done / completed.output（修复 R1）。
type fnCall struct {
	callID      string
	name        string
	args        strings.Builder
	outputIndex int
	added       bool
}

func ConvertOpenAIChatStreamToResponsesStream(resp *http.Response, writer StreamResponseWriter, model string) error {
	defer resp.Body.Close()

	responseID := newCanonicalResponseID("resp")
	itemID := newCanonicalResponseID("msg")
	createdAt := time.Now().Unix()
	textStarted := false

	writeResponsesEvent := func(eventType string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := writer.WriteString("event: " + eventType + "\n"); err != nil {
			return err
		}
		if _, err := writer.WriteString("data: " + string(data) + "\n\n"); err != nil {
			return err
		}
		return writer.Flush()
	}

	baseResponse := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": createdAt,
		"status":     "in_progress",
		"model":      model,
		"output":     []any{},
	}

	if err := writeResponsesEvent("response.created", map[string]any{
		"type":     "response.created",
		"response": baseResponse,
	}); err != nil {
		return err
	}
	if err := writeResponsesEvent("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":      itemID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	}); err != nil {
		return err
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	var fullText strings.Builder
	var finalUsage any
	finishReason := "stop"

	// 工具调用累积：按 OpenAI tool_call index 跟踪 function_call 项，分配顺序
	// output_index（message 占 0，工具从 1 起），补齐 Responses 协议要求的
	// output_item.added / function_call_arguments.done / output_item.done 事件，
	// 并把 function_call 项纳入 completed.output（修复 R1：此前只发 arguments.delta，
	// 客户端收到指向「未声明项」的增量、且完成响应里没有 function_call）。
	type fnCall struct {
		callID      string
		name        string
		args        strings.Builder
		outputIndex int
		added       bool
	}
	toolCalls := map[int]*fnCall{}
	var toolOrder []int
	nextOutputIndex := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}

		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			finalUsage = usage
		}

		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if choice == nil {
			continue
		}
		if fr := stringValue(choice["finish_reason"]); fr != "" {
			finishReason = fr
		}
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		if text := stringValue(delta["content"]); text != "" {
			if !textStarted {
				textStarted = true
				if err := writeResponsesEvent("response.content_part.added", map[string]any{
					"type":          "response.content_part.added",
					"item_id":       itemID,
					"output_index":  0,
					"content_index": 0,
					"part":          map[string]any{"type": "output_text", "text": ""},
				}); err != nil {
					return err
				}
			}
			fullText.WriteString(text)
			if err := writeResponsesEvent("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       itemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         text,
			}); err != nil {
				return err
			}
		}
		if toolCalls0, ok := delta["tool_calls"].([]any); ok {
			for _, toolCall := range toolCalls0 {
				tc, _ := toolCall.(map[string]any)
				if tc == nil {
					continue
				}
				idx := 0
				if v, ok := numberValue(tc["index"]); ok {
					idx = int(v)
				}
				call := toolCalls[idx]
				if call == nil {
					call = &fnCall{outputIndex: nextOutputIndex}
					nextOutputIndex++
					toolCalls[idx] = call
					toolOrder = append(toolOrder, idx)
				}
				if id := stringValue(tc["id"]); id != "" {
					call.callID = id
				}
				fn, _ := tc["function"].(map[string]any)
				if fn != nil {
					if name := stringValue(fn["name"]); name != "" {
						call.name = name
					}
				}
				// 首见该工具项时补发 output_item.added（function_call），
				// 让下游知道随后的 arguments.delta 属于哪个已声明项。
				if !call.added {
					call.added = true
					if call.callID == "" {
						call.callID = fmt.Sprintf("call_%d", idx)
					}
					if err := writeResponsesEvent("response.output_item.added", map[string]any{
						"type":         "response.output_item.added",
						"output_index": call.outputIndex,
						"item": map[string]any{
							"id":        call.callID,
							"type":      "function_call",
							"status":    "in_progress",
							"call_id":   call.callID,
							"name":      call.name,
							"arguments": "",
						},
					}); err != nil {
						return err
					}
				}
				if fn != nil {
					args := stringValue(fn["arguments"])
					if args == "" {
						continue
					}
					call.args.WriteString(args)
					if err := writeResponsesEvent("response.function_call_arguments.delta", map[string]any{
						"type":         "response.function_call_arguments.delta",
						"item_id":      call.callID,
						"output_index": call.outputIndex,
						"delta":        args,
					}); err != nil {
						return err
					}
				}
			}
		}
	}

	if textStarted {
		if err := writeResponsesEvent("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       itemID,
			"output_index":  0,
			"content_index": 0,
			"text":          fullText.String(),
		}); err != nil {
			return err
		}
	}

	outputItems := []map[string]any{{
		"id":      itemID,
		"type":    "message",
		"status":  "completed",
		"role":    "assistant",
		"content": []map[string]any{{"type": "output_text", "text": fullText.String()}},
	}}
	if err := writeResponsesEvent("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item":         outputItems[0],
	}); err != nil {
		return err
	}

	// 工具调用项收尾：按出现顺序为每个 function_call 补发 arguments.done +
	// output_item.done，并纳入最终 output（修复 R1——此前只发 arguments.delta，
	// 既不声明项也不收尾、completed.output 里也没有 function_call）。
	for _, idx := range toolOrder {
		call := toolCalls[idx]
		if call == nil {
			continue
		}
		if err := writeResponsesEvent("response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      call.callID,
			"output_index": call.outputIndex,
			"arguments":    call.args.String(),
		}); err != nil {
			return err
		}
		fnItem := map[string]any{
			"id":        call.callID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   call.callID,
			"name":      call.name,
			"arguments": call.args.String(),
		}
		if err := writeResponsesEvent("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": call.outputIndex,
			"item":         fnItem,
		}); err != nil {
			return err
		}
		outputItems = append(outputItems, fnItem)
	}

	completed := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": createdAt,
		"status":     "completed",
		"model":      model,
		"output":     outputItems,
	}
	// usage 始终重映射为 Responses 命名（input/output/total_tokens）；上游未给 usage
	// 时也输出全 0 的完整结构，避免 codex 解析失败。
	completed["usage"] = chatUsageToResponsesUsage(finalUsage)
	if finishReason != "" {
		completed["incomplete_details"] = nil
	}

	return writeResponsesEvent("response.completed", map[string]any{
		"type":     "response.completed",
		"response": completed,
	})
}

func ConvertClaudeStreamToResponsesStream(resp *http.Response, writer StreamResponseWriter, model string) error {
	return convertGenericTextSSEToResponses(resp, writer, model, "claude")
}

func ConvertGeminiStreamToResponsesStream(resp *http.Response, writer StreamResponseWriter, model string) error {
	return convertGenericTextSSEToResponses(resp, writer, model, "gemini")
}

func convertGenericTextSSEToResponses(resp *http.Response, writer StreamResponseWriter, model string, source string) error {
	defer resp.Body.Close()

	responseID := newCanonicalResponseID("resp")
	itemID := newCanonicalResponseID("msg")
	createdAt := time.Now().Unix()

	write := func(eventType string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := writer.WriteString("event: " + eventType + "\n"); err != nil {
			return err
		}
		if _, err := writer.WriteString("data: " + string(data) + "\n\n"); err != nil {
			return err
		}
		return writer.Flush()
	}

	if err := write("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": createdAt,
			"status":     "in_progress",
			"model":      model,
		},
	}); err != nil {
		return err
	}
	if err := write("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":      itemID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	}); err != nil {
		return err
	}
	if err := write("response.content_part.added", map[string]any{
		"type":          "response.content_part.added",
		"item_id":       itemID,
		"output_index":  0,
		"content_index": 0,
		"part":          map[string]any{"type": "output_text", "text": ""},
	}); err != nil {
		return err
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	var fullText strings.Builder
	// finalUsage 用合并语义累积：Claude 的 input_tokens 来自 message_start，
	// output_tokens 来自后续 message_delta，必须合并而非互相覆盖（否则丢 input_tokens）。
	finalUsage := map[string]any{}
	mergeUsage := func(src map[string]any) {
		for k, v := range src {
			finalUsage[k] = v
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		text := ""
		if source == "claude" {
			if delta, ok := event["delta"].(map[string]any); ok {
				text = stringValue(delta["text"])
				if text == "" {
					text = stringValue(delta["thinking"])
				}
			}
			if usage, ok := event["usage"].(map[string]any); ok {
				mergeUsage(usage)
			}
			if msg, ok := event["message"].(map[string]any); ok {
				if usage, ok := msg["usage"].(map[string]any); ok {
					mergeUsage(usage)
				}
			}
		} else {
			if usage, ok := event["usageMetadata"].(map[string]any); ok {
				mergeUsage(usage)
			}
			if candidates, ok := event["candidates"].([]any); ok {
				for _, candRaw := range candidates {
					cand, _ := candRaw.(map[string]any)
					content, _ := cand["content"].(map[string]any)
					parts, _ := content["parts"].([]any)
					for _, partRaw := range parts {
						part, _ := partRaw.(map[string]any)
						text += stringValue(part["text"])
					}
				}
			}
		}

		if text != "" {
			fullText.WriteString(text)
			if err := write("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       itemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         text,
			}); err != nil {
				return err
			}
		}
	}

	_ = write("response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       itemID,
		"output_index":  0,
		"content_index": 0,
		"text":          fullText.String(),
	})
	_ = write("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item": map[string]any{
			"id":      itemID,
			"type":    "message",
			"status":  "completed",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": fullText.String()}},
		},
	})

	completed := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": createdAt,
		"status":     "completed",
		"model":      model,
		"output": []map[string]any{{
			"id":      itemID,
			"type":    "message",
			"status":  "completed",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": fullText.String()}},
		}},
	}
	completed["usage"] = chatUsageToResponsesUsage(finalUsage)

	return write("response.completed", map[string]any{
		"type":     "response.completed",
		"response": completed,
	})
}
