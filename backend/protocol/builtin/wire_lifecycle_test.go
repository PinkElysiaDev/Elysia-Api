package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func chatContractFrame(id, model string, created int, delta, finish string) string {
	return fmt.Sprintf(`{"id":%q,"model":%q,"created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`, id, model, created, delta, finish)
}

func responsesContractFrame(kind, id, model, status string, sequence int) string {
	return fmt.Sprintf(`{"type":%q,"sequence_number":%d,"response":{"id":%q,"object":"response","model":%q,"created_at":1,"status":%q,"output":[]}}`, kind, sequence, id, model, status)
}

const anthropicContractStart = `{"type":"message_start","message":{"id":"r","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`
const anthropicContractDelta = `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":0}}`

func TestWireStreamLifecycleRejectsIncompleteContracts(t *testing.T) {
	chatStart := chatContractFrame("r", "m", 1, `{"role":"assistant","content":"hi"}`, `null`)
	responsesStart := responsesContractFrame("response.created", "r", "m", "in_progress", 0)
	for _, tt := range []struct {
		name, codec, path string
		frames            []string
	}{
		{"chat-role-absent", Chat, "/choices/0/delta/role", []string{chatContractFrame("r", "m", 1, `{"content":"hi"}`, `null`), chatContractFrame("r", "m", 1, `{}`, `"stop"`)}},
		{"chat-role-invalid", Chat, "/choices/0/delta/role", []string{chatContractFrame("r", "m", 1, `{"role":"system","content":"hi"}`, `"stop"`)}},
		{"chat-role-null", Chat, "/choices/0/delta/role", []string{chatContractFrame("r", "m", 1, `{"role":null}`, `null`)}},
		{"chat-role-type", Chat, "/choices/0/delta/role", []string{chatContractFrame("r", "m", 1, `{"role":42}`, `null`)}},
		{"chat-id-change", Chat, "/id", []string{chatStart, chatContractFrame("other", "m", 1, `{}`, `"stop"`)}},
		{"chat-model-change", Chat, "/model", []string{chatStart, chatContractFrame("r", "other", 1, `{}`, `"stop"`)}},
		{"chat-time-change", Chat, "/created", []string{chatStart, chatContractFrame("r", "m", 2, `{}`, `"stop"`)}},
		{"chat-empty-choice-after-finish", Chat, "/choices", []string{chatContractFrame("r", "m", 1, `{"role":"assistant"}`, `"stop"`), chatContractFrame("r", "m", 1, `{}`, `null`)}},
		{"responses-missing-start", Responses, "/type", []string{responsesContractFrame("response.completed", "r", "m", "completed", 0)}},
		{"responses-repeated-start", Responses, "/type", []string{responsesStart, responsesContractFrame("response.created", "r", "m", "in_progress", 1)}},
		{"responses-id-change", Responses, "/response/id", []string{responsesStart, responsesContractFrame("response.in_progress", "other", "m", "in_progress", 1)}},
		{"responses-model-change", Responses, "/response/model", []string{responsesStart, responsesContractFrame("response.completed", "r", "other", "completed", 1)}},
		{"responses-status-mismatch", Responses, "/response/status", []string{responsesStart, responsesContractFrame("response.completed", "r", "m", "in_progress", 1)}},
		{"anthropic-missing-start", Anthropic, "/type", []string{anthropicContractDelta, `{"type":"message_stop"}`}},
		{"anthropic-repeated-start", Anthropic, "/type", []string{anthropicContractStart, anthropicContractStart}},
		{"anthropic-unclosed-block", Anthropic, "/type", []string{anthropicContractStart, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hi"}}`, anthropicContractDelta}},
		{"gemini-id-change", Gemini, "/responseId", []string{`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hi"}]}}]}`, `{"responseId":"other","modelVersion":"m","candidates":[{"index":0,"finishReason":"STOP"}]}`}},
		{"gemini-role-invalid", Gemini, "/candidates/0/content/role", []string{`{"candidates":[{"index":0,"content":{"role":"user","parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := shippedProjectionProtocol(t, tt.codec)
			v, err := c.NewWireStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range tt.frames {
				err = v.Consume(t.Context(), testValue(t, body))
				if err != nil {
					break
				}
			}
			if err == nil || !strings.Contains(err.Error(), tt.path) {
				t.Fatalf("incomplete client contract was accepted or misdiagnosed: %v", err)
			}
		})
	}
}

func TestStreamDecodersDoNotReplaceExplicitInvalidRoles(t *testing.T) {
	for _, name := range []string{Chat, Gemini} {
		for _, role := range []string{`"user"`, `"system"`, `null`, `42`} {
			body := chatContractFrame("r", "m", 1, `{"role":`+role+`,"content":"hi"}`, `null`)
			if name == Gemini {
				body = `{"candidates":[{"content":{"role":` + role + `,"parts":[{"text":"hi"}]}}]}`
			}
			if _, err := shippedProjectionProtocol(t, name).DecodeFrame(t.Context(), testValue(t, body), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil || !strings.Contains(err.Error(), "/role") {
				t.Fatal("invalid source role could be silently replaced at a foreign target", name, role, err)
			}
		}
	}
}

func TestWireStreamLifecycleKeepsValidShapes(t *testing.T) {
	for _, tt := range []struct {
		name, codec string
		frames      []string
	}{
		{"delayed-role", Chat, []string{chatContractFrame("r", "m", 1, `{"content":"hi"}`, `null`), chatContractFrame("r", "m", 1, `{"role":"assistant"}`, `"stop"`)}},
		{"repeated-role-and-usage", Chat, []string{chatContractFrame("r", "m", 1, `{"role":"assistant","content":"hi"}`, `null`), chatContractFrame("r", "m", 1, `{"role":"assistant"}`, `"stop"`), `{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`}},
		{"escaped-id", Chat, []string{chatContractFrame("r", "m", 1, `{"role":"assistant"}`, `null`), strings.Replace(chatContractFrame("r", "m", 1, `{"content":"hi"}`, `"stop"`), `"id":"r"`, `"id":"\u0072"`, 1)}},
		{"responses-empty", Responses, []string{responsesContractFrame("response.created", "r", "m", "in_progress", 0), responsesContractFrame("response.completed", "r", "m", "completed", 1)}},
		{"anthropic-empty-with-ping", Anthropic, []string{`{"type":"ping"}`, anthropicContractStart, anthropicContractDelta, `{"type":"message_stop"}`}},
		{"gemini-optional-envelope", Gemini, []string{`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`, `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`}},
		{"chat-error-without-role", Chat, []string{`{"error":{"type":"api_error","message":"failed"}}`}},
		{"responses-error-without-start", Responses, []string{`{"type":"error","sequence_number":0,"error":{"type":"api_error","message":"failed"}}`}},
		{"anthropic-error-without-start", Anthropic, []string{`{"type":"error","error":{"type":"api_error","message":"failed"}}`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := shippedProjectionProtocol(t, tt.codec)
			v, err := c.NewWireStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range tt.frames {
				if err := v.Consume(t.Context(), testValue(t, body)); err != nil {
					t.Fatal(body, err)
				}
			}
			if err := v.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
