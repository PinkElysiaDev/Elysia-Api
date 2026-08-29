package relay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 批次三回归：引用与溯源三线——Claude citations 原样往返、Gemini grounding
// 解析/回写（非流式 + 流式）、Chat 目标 annotations 下发。

const claudeCitations = `[{"type":"web_search_result_location","cited_text":"奥运","url":"https://example.com/olympics","title":"Example","start_index":0,"end_index":2}]`

func TestClaudeCitationsRoundTrip(t *testing.T) {
	var citations json.RawMessage = []byte(claudeCitations)
	resp, err := AnthropicResponseToMaheshvara(&ClaudeResponse{
		ID: "msg_1", Model: "claude-x", StopReason: "end_turn",
		Content: []ClaudeContent{{Type: "text", Text: "引用文本", Citations: citations}},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Output) == 0 || len(resp.Output[0].Content) == 0 {
		t.Fatalf("text part missing: %+v", resp.Output)
	}
	part := resp.Output[0].Content[0]
	if string(part.Citations) != claudeCitations {
		t.Fatalf("citations not preserved verbatim: %s", part.Citations)
	}
	rendered, err := MaheshvaraToAnthropicResponse(resp)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(rendered.Content) == 0 || string(rendered.Content[0].Citations) != claudeCitations {
		t.Fatalf("citations not written back: %+v", rendered.Content)
	}

	// 请求侧：客户端回传带 citations 的 assistant text block，转发给 Claude
	// 上游时原样保留。
	req, err := AnthropicToMaheshvara([]byte(`{"model":"grp","max_tokens":16,"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"text","text":"引用文本","citations":` + claudeCitations + `}]}]}`))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	target, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatalf("render request: %v", err)
	}
	if !strings.Contains(string(target), `"citations"`) || !strings.Contains(string(target), "example.com/olympics") {
		t.Fatalf("citations lost on request path: %s", target)
	}
}

func geminiGroundingResponse() *GeminiResponse {
	var grounding json.RawMessage = []byte(`{"groundingChunks":[{"web":{"uri":"https://example.com/source","title":"Source"}}],"webSearchQueries":["olympics"]}`)
	return &GeminiResponse{
		ResponseID: "gem_1", ModelVersion: "gemini-x",
		Candidates: []GeminiCandidate{{
			Content:           GeminiContent{Role: "model", Parts: []GeminiPart{{Text: "据实回答"}}},
			FinishReason:      "STOP",
			GroundingMetadata: grounding,
		}},
	}
}

// Gemini grounding：解析进 annotations（原始 JSON 保真），回写 Gemini 响应的
// candidate.groundingMetadata。
func TestGeminiGroundingRoundTrip(t *testing.T) {
	resp, err := GeminiResponseToMaheshvara(geminiGroundingResponse())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var textPart *MaheshvaraContentPart
	for i := range resp.Output {
		for j := range resp.Output[i].Content {
			if resp.Output[i].Content[j].Type == MaheshvaraContentText {
				textPart = &resp.Output[i].Content[j]
			}
		}
	}
	if textPart == nil || len(textPart.Annotations) == 0 {
		t.Fatalf("grounding not attached to text part: %+v", resp.Output)
	}
	if _, ok := textPart.Annotations[0][MaheshvaraAnnotationGeminiGrounding]; !ok {
		t.Fatalf("grounding wrapper key missing: %+v", textPart.Annotations)
	}
	rendered, err := MaheshvaraToGeminiResponse(resp)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(rendered.Candidates) == 0 || len(rendered.Candidates[0].GroundingMetadata) == 0 {
		t.Fatalf("groundingMetadata not written back: %+v", rendered.Candidates)
	}
	if !strings.Contains(string(rendered.Candidates[0].GroundingMetadata), "example.com/source") {
		t.Fatalf("grounding content lost: %s", rendered.Candidates[0].GroundingMetadata)
	}
}

// Gemini grounding → OpenAI Chat 目标：文本 part 带 annotations 下发。
func TestGeminiGroundingToChatAnnotations(t *testing.T) {
	resp, err := GeminiResponseToMaheshvara(geminiGroundingResponse())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rendered, err := MaheshvaraToOpenAIChatResponse(resp)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	encoded, err := json.Marshal(rendered.Choices[0].Message.Content)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), "annotations") || !strings.Contains(string(encoded), "example.com/source") {
		t.Fatalf("annotations not delivered on chat target: %s", encoded)
	}
}

// Gemini 流式：grounding 随 candidate chunk 到达，下游 finish chunk 回写
// candidate.groundingMetadata。
func TestGeminiStreamGroundingWriteback(t *testing.T) {
	body := strings.Join([]string{
		`data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"据实回答"}]},"groundingMetadata":{"groundingChunks":[{"web":{"uri":"https://example.com/source"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatGemini, FormatGemini, writer, "gemini-target"); err != nil {
		t.Fatalf("transform: %v\n%s", err, writer.String())
	}
	out := writer.String()
	if !strings.Contains(out, "groundingMetadata") || !strings.Contains(out, "example.com/source") {
		t.Fatalf("groundingMetadata missing on downstream stream:\n%s", out)
	}
}
