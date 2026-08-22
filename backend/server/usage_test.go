package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

func TestUsageFromProviderBodyWithoutUsageStaysEmpty(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"id":"chatcmpl","choices":[]}`))

	if usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil || usage.CacheHitTokens != nil {
		t.Fatalf("expected missing usage fields to stay nil, got %+v", usage)
	}
}

func TestUsageFromProviderBodyKeepsExplicitZero(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"usage":{"total_tokens":0}}`))

	if usage.TotalTokens == nil || *usage.TotalTokens != 0 {
		t.Fatalf("expected explicit zero total token to be preserved, got %+v", usage)
	}
	if usage.InputTokens != nil || usage.OutputTokens != nil {
		t.Fatalf("expected absent input/output tokens to stay nil, got %+v", usage)
	}
}

func TestOpenAIChatUsageFields(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"prompt_tokens_details":{"cached_tokens":4}}}`))

	if usage.InputTokens == nil || *usage.InputTokens != 10 || usage.OutputTokens == nil || *usage.OutputTokens != 20 || usage.TotalTokens == nil || *usage.TotalTokens != 30 || usage.CacheHitTokens == nil || *usage.CacheHitTokens != 4 {
		t.Fatalf("expected OpenAI chat usage fields, got %+v", usage)
	}
}

func TestOpenAIResponsesUsageFields(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150,"input_tokens_details":{"cached_tokens":25}}}`))

	if usage.InputTokens == nil || *usage.InputTokens != 100 || usage.OutputTokens == nil || *usage.OutputTokens != 50 || usage.TotalTokens == nil || *usage.TotalTokens != 150 || usage.CacheHitTokens == nil || *usage.CacheHitTokens != 25 {
		t.Fatalf("expected OpenAI responses usage fields, got %+v", usage)
	}
}

func TestOpenAIPromptCacheHitTokens(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_cache_hit_tokens":40}}`))

	if usage.CacheHitTokens == nil || *usage.CacheHitTokens != 40 {
		t.Fatalf("expected prompt_cache_hit_tokens to map to cache hit tokens, got %+v", usage)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 150 {
		t.Fatalf("expected total to derive from prompt and completion, got %+v", usage)
	}
}

func TestOpenAILlamaTimingsCacheTokens(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"timings":{"cache_n":17}}`))

	if usage.CacheHitTokens == nil || *usage.CacheHitTokens != 17 {
		t.Fatalf("expected llama.cpp timings cache_n to map to cache hit tokens, got %+v", usage)
	}
}

func TestOpenAIMoonshotChoiceCacheTokens(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformOpenAI, []byte(`{"choices":[{"usage":{"cached_tokens":23}}]}`))

	if usage.CacheHitTokens == nil || *usage.CacheHitTokens != 23 {
		t.Fatalf("expected choices usage cached_tokens to map to cache hit tokens, got %+v", usage)
	}
}

func TestClaudeUsageIncludesCacheCreationInInput(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformAnthropic, []byte(`{"usage":{"input_tokens":100,"output_tokens":200,"cache_read_input_tokens":30,"cache_creation_input_tokens":50}}`))

	if usage.InputTokens == nil || *usage.InputTokens != 180 {
		t.Fatalf("expected Claude input to include cache read and creation tokens, got %+v", usage)
	}
	if usage.OutputTokens == nil || *usage.OutputTokens != 200 || usage.TotalTokens == nil || *usage.TotalTokens != 380 || usage.CacheHitTokens == nil || *usage.CacheHitTokens != 30 {
		t.Fatalf("expected Claude output/total/cache usage, got %+v", usage)
	}
}

func TestStreamUsageWithoutUsageFieldsStaysEmpty(t *testing.T) {
	_, ok := parseStreamUsage(`{"type":"content_block_delta","delta":{"text":"hello"}}`)
	if ok {
		t.Fatal("expected stream event without usage fields to not produce token usage")
	}
}

func TestClaudeStreamUsageMergesStartAndDelta(t *testing.T) {
	start, ok := parseStreamUsage(`{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":30,"cache_creation_input_tokens":50}}}`)
	if !ok {
		t.Fatal("expected message_start usage")
	}
	delta, ok := parseStreamUsage(`{"type":"message_delta","usage":{"output_tokens":200}}`)
	if !ok {
		t.Fatal("expected message_delta usage")
	}
	usage := mergeUsage(start, delta)

	if usage.InputTokens == nil || *usage.InputTokens != 180 || usage.OutputTokens == nil || *usage.OutputTokens != 200 || usage.CacheHitTokens == nil || *usage.CacheHitTokens != 30 {
		t.Fatalf("expected merged Claude usage, got %+v", usage)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 380 {
		t.Fatalf("expected merged Claude total, got %+v", usage)
	}
}

func TestGeminiUsageIncludesToolAndThoughtTokens(t *testing.T) {
	usage := usageFromProviderBody(relay.PlatformGemini, []byte(`{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":5,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":42,"cachedContentTokenCount":3}}`))

	if usage.InputTokens == nil || *usage.InputTokens != 15 {
		t.Fatalf("expected Gemini input to include tool use prompt tokens, got %+v", usage)
	}
	if usage.OutputTokens == nil || *usage.OutputTokens != 27 {
		t.Fatalf("expected Gemini output to include thoughts tokens, got %+v", usage)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 42 || usage.CacheHitTokens == nil || *usage.CacheHitTokens != 3 {
		t.Fatalf("expected Gemini total/cache usage, got %+v", usage)
	}
}

func TestOpenAIStreamUsageChunk(t *testing.T) {
	usage, ok := parsePlatformStreamUsage(relay.PlatformOpenAI, `{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	if !ok {
		t.Fatal("expected OpenAI stream usage chunk")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 11 || usage.OutputTokens == nil || *usage.OutputTokens != 7 || usage.TotalTokens == nil || *usage.TotalTokens != 18 {
		t.Fatalf("expected OpenAI stream usage fields, got %+v", usage)
	}
}

func TestGeminiStreamUsageMetadata(t *testing.T) {
	usage, ok := parsePlatformStreamUsage(relay.PlatformGemini, `{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":2,"candidatesTokenCount":8,"thoughtsTokenCount":3,"totalTokenCount":23}}`)
	if !ok {
		t.Fatal("expected Gemini stream usage metadata")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 12 || usage.OutputTokens == nil || *usage.OutputTokens != 11 || usage.TotalTokens == nil || *usage.TotalTokens != 23 {
		t.Fatalf("expected Gemini stream usage fields, got %+v", usage)
	}
}

func TestConvertedStreamWriterDoesNotRecordPlaceholderUsage(t *testing.T) {
	record := &usageRecord{}
	writer := &observingStreamWriter{
		inner:        nopStreamWriter{},
		record:       record,
		observeUsage: false,
	}

	_, err := writer.WriteString("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n")
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if record.Usage.InputTokens != nil || record.Usage.OutputTokens != nil || record.Usage.TotalTokens != nil {
		t.Fatalf("expected converted placeholder usage to be ignored, got %+v", record.Usage)
	}
	if record.ProviderResponse.Content != "" {
		t.Fatalf("expected converted stream not to overwrite provider response, got %+v", record.ProviderResponse)
	}
}

func TestUpstreamObserverRecordsRawUsage(t *testing.T) {
	record := &usageRecord{}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":9,\"total_tokens\":21}}\n\n"))}
	observeUpstreamUsage(resp, record, relay.PlatformOpenAI)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if record.Usage.InputTokens == nil || *record.Usage.InputTokens != 12 || record.Usage.OutputTokens == nil || *record.Usage.OutputTokens != 9 || record.Usage.TotalTokens == nil || *record.Usage.TotalTokens != 21 {
		t.Fatalf("expected upstream raw stream usage, got %+v", record.Usage)
	}
	if record.ProviderResponse.Content == "" {
		t.Fatal("expected upstream raw stream sample to be recorded")
	}
}

func TestUpstreamObserverParsesDataWithoutSpace(t *testing.T) {
	record := &usageRecord{}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("data:{\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":9,\"total_tokens\":21}}\n\n"))}
	observeUpstreamUsage(resp, record, relay.PlatformOpenAI)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if record.Usage.InputTokens == nil || *record.Usage.InputTokens != 12 || record.Usage.OutputTokens == nil || *record.Usage.OutputTokens != 9 || record.Usage.TotalTokens == nil || *record.Usage.TotalTokens != 21 {
		t.Fatalf("expected usage from data without space, got %+v", record.Usage)
	}
}

func TestUpstreamObserverFlushesFinalLineWithoutNewline(t *testing.T) {
	record := &usageRecord{}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":9,\"total_tokens\":21}}"))}
	observeUpstreamUsage(resp, record, relay.PlatformOpenAI)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if record.Usage.InputTokens == nil || *record.Usage.InputTokens != 12 || record.Usage.OutputTokens == nil || *record.Usage.OutputTokens != 9 || record.Usage.TotalTokens == nil || *record.Usage.TotalTokens != 21 {
		t.Fatalf("expected usage from final line without newline, got %+v", record.Usage)
	}
}

func TestUpstreamObserverKeepsUsageAfterNullUsageChunks(t *testing.T) {
	record := &usageRecord{}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":9,\"total_tokens\":21}}\n\n"))}
	observeUpstreamUsage(resp, record, relay.PlatformOpenAI)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if record.Usage.InputTokens == nil || *record.Usage.InputTokens != 12 || record.Usage.OutputTokens == nil || *record.Usage.OutputTokens != 9 || record.Usage.TotalTokens == nil || *record.Usage.TotalTokens != 21 {
		t.Fatalf("expected final usage after null usage chunks, got %+v", record.Usage)
	}
}

func TestStreamUsageNullDoesNotClearExistingUsage(t *testing.T) {
	existing := usageTokenUsage{InputTokens: intPtr(12), OutputTokens: intPtr(9), TotalTokens: intPtr(21)}
	next, ok := parsePlatformStreamUsage(relay.PlatformOpenAI, `{"choices":[{"delta":{"content":"hi"}}],"usage":null}`)
	if ok {
		existing = mergeUsage(existing, next)
	}
	if existing.InputTokens == nil || *existing.InputTokens != 12 || existing.OutputTokens == nil || *existing.OutputTokens != 9 || existing.TotalTokens == nil || *existing.TotalTokens != 21 {
		t.Fatalf("expected null usage to keep existing usage, got %+v", existing)
	}
}

type nopStreamWriter struct{}

func (nopStreamWriter) Write(data []byte) (int, error)       { return len(data), nil }
func (nopStreamWriter) WriteString(data string) (int, error) { return len(data), nil }
func (nopStreamWriter) Flush() error                         { return nil }

func TestEstimatedTokensDoNotAffectSummaryTotals(t *testing.T) {
	summary := summarizeUsage([]usageRecord{{Usage: usageTokenUsage{EstimatedTokens: 32000, Estimated: true}}})

	if summary.TotalTokens != 0 || summary.EstimatedTokens != 32000 {
		t.Fatalf("expected estimated tokens to be tracked separately without inflating total, got %+v", summary)
	}
}

func TestUsageStatusMatches(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		filter     string
		want       bool
	}{
		{name: "success accepts 2xx", statusCode: 200, filter: "success", want: true},
		{name: "success accepts 3xx", statusCode: 302, filter: "success", want: true},
		{name: "success rejects 4xx", statusCode: 404, filter: "success", want: false},
		{name: "failed accepts 4xx", statusCode: 429, filter: "failed", want: true},
		{name: "failed accepts 5xx", statusCode: 500, filter: "failed", want: true},
		{name: "failed rejects 2xx", statusCode: 200, filter: "failed", want: false},
		{name: "exact status matches", statusCode: 401, filter: "401", want: true},
		{name: "exact status rejects other codes", statusCode: 403, filter: "401", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageStatusMatches(tc.statusCode, tc.filter); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestUsageValueMatches(t *testing.T) {
	if !usageValueMatches(nil, "caller-a") {
		t.Fatal("expected empty filters to match")
	}
	if !usageValueMatches([]string{"caller-a", "caller-b"}, "caller-b") {
		t.Fatal("expected multi-value filter to match included value")
	}
	if usageValueMatches([]string{"caller-a", "caller-b"}, "caller-c") {
		t.Fatal("expected multi-value filter to reject missing value")
	}
}

func TestSummarizeUsageIgnoresNilTokenFields(t *testing.T) {
	summary := summarizeUsage([]usageRecord{{Usage: usageTokenUsage{}}})

	if summary.InputTokens != 0 || summary.OutputTokens != 0 || summary.TotalTokens != 0 || summary.CacheHitTokens != 0 {
		t.Fatalf("expected nil usage fields to summarize as zero, got %+v", summary)
	}
}

func TestUsageDetailAndBuiltinToolUsageFromCanonical(t *testing.T) {
	canonical := &relay.CanonicalUsage{
		InputTokens:              100,
		OutputTokens:             50,
		TotalTokens:              150,
		CachedInputTokens:        25,
		CacheCreationInputTokens: 5,
		ReasoningTokens:          7,
		TextInputTokens:          80,
		TextOutputTokens:         30,
		ImageInputTokens:         12,
		ImageOutputTokens:        2,
		AudioInputTokens:         3,
		AudioOutputTokens:        4,
		ToolUseTokens:            9,
		WebSearchCallCount:       1,
		FileSearchCallCount:      2,
		ImageGenerationCallCount: 3,
		CodeInterpreterCallCount: 4,
		ComputerUseCallCount:     5,
		Estimated:                true,
		Source:                   "provider_response",
	}

	detail := usageDetailFromCanonical(canonical)
	if detail.InputTokens == nil || *detail.InputTokens != 100 ||
		detail.OutputTokens == nil || *detail.OutputTokens != 50 ||
		detail.TotalTokens == nil || *detail.TotalTokens != 150 ||
		detail.CachedInputTokens == nil || *detail.CachedInputTokens != 25 ||
		detail.CacheCreationInputTokens == nil || *detail.CacheCreationInputTokens != 5 ||
		detail.ReasoningTokens == nil || *detail.ReasoningTokens != 7 ||
		detail.TextInputTokens == nil || *detail.TextInputTokens != 80 ||
		detail.TextOutputTokens == nil || *detail.TextOutputTokens != 30 ||
		detail.ImageInputTokens == nil || *detail.ImageInputTokens != 12 ||
		detail.ImageOutputTokens == nil || *detail.ImageOutputTokens != 2 ||
		detail.AudioInputTokens == nil || *detail.AudioInputTokens != 3 ||
		detail.AudioOutputTokens == nil || *detail.AudioOutputTokens != 4 ||
		detail.ToolUseTokens == nil || *detail.ToolUseTokens != 9 ||
		!detail.Estimated {
		t.Fatalf("expected detailed canonical usage mapping, got %+v", detail)
	}

	builtin := builtinToolUsageFromCanonical(canonical)
	if builtin.WebSearchCalls != 1 || builtin.FileSearchCalls != 2 || builtin.ImageGenerationCalls != 3 || builtin.CodeInterpreterCalls != 4 || builtin.ComputerUseCalls != 5 {
		t.Fatalf("expected builtin tool usage mapping, got %+v", builtin)
	}

	record := &usageRecord{}
	updateRecordUsageFromCanonical(record, canonical)
	if record.UsageSource != "provider_response" || record.Usage.InputTokens == nil || *record.Usage.InputTokens != 100 || record.BuiltinToolUsage.WebSearchCalls != 1 {
		t.Fatalf("expected record to be updated from canonical usage, got %+v", record)
	}
}

func TestEstimateCanonicalRequestUsageCountsTextFilesImagesAndTools(t *testing.T) {
	req := &relay.CanonicalRequest{
		Instructions:    "system",
		MaxOutputTokens: 20,
		Messages: []relay.CanonicalMessage{{
			Role: "user",
			Content: []relay.CanonicalContentPart{
				{Type: relay.CanonicalContentText, Text: "hello world"},
				{Type: relay.CanonicalContentImage, ImageURL: "https://example.com/a.png"},
				{Type: relay.CanonicalContentFile, FileData: strings.Repeat("x", 2048)},
			},
			ToolCalls: []relay.CanonicalToolCall{{Name: "lookup", Arguments: []byte(`{"q":"x"}`)}},
		}},
		InputItems: []relay.CanonicalInputItem{{
			Type:   relay.CanonicalInputFunctionCallOutput,
			Output: "tool result",
		}},
		Tools: []relay.CanonicalTool{
			{Type: relay.CanonicalToolWebSearchPreview},
			{Type: relay.CanonicalToolFileSearch},
			{Type: relay.CanonicalToolImageGeneration},
		},
		ResponseFormat: &relay.CanonicalResponseFormat{Type: "json_schema", Name: "answer"},
	}

	usage := estimateCanonicalRequestUsage(req, config.UsageConfig{
		CharsPerToken:               4,
		DefaultOutputTokenEstimate:  1024,
		ImageInputTokenEstimate:     300,
		FileInputTokenEstimatePerKB: 128,
	})

	if usage == nil || !usage.Estimated || usage.Source != "canonical_estimate" {
		t.Fatalf("expected estimated canonical usage, got %+v", usage)
	}
	if usage.OutputTokens != 20 || usage.EstimatedOutputTokens != 20 {
		t.Fatalf("expected max output tokens to drive estimate, got %+v", usage)
	}
	if usage.ImageInputTokens != 300 {
		t.Fatalf("expected image estimate, got %+v", usage)
	}
	if usage.InputTokens <= usage.ImageInputTokens || usage.TotalTokens != usage.InputTokens+usage.OutputTokens {
		t.Fatalf("expected input/total estimate to include text, file and image tokens, got %+v", usage)
	}
	if usage.WebSearchCallCount != 1 || usage.FileSearchCallCount != 1 || usage.ImageGenerationCallCount != 1 {
		t.Fatalf("expected builtin tool estimates, got %+v", usage)
	}
}

func TestCanonicalEstimateDoesNotPopulateActualUsage(t *testing.T) {
	canonical := &relay.CanonicalUsage{
		InputTokens:           10,
		OutputTokens:          1000000,
		TotalTokens:           1000010,
		EstimatedTotalTokens:  1000010,
		EstimatedOutputTokens: 1000000,
		Estimated:             true,
		Source:                "canonical_estimate",
	}

	usage := usageTokenUsageFromCanonical(canonical)
	if usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil {
		t.Fatalf("expected request estimate to stay out of actual usage fields, got %+v", usage)
	}
	if !usage.Estimated || usage.EstimatedTokens != 1000010 {
		t.Fatalf("expected estimated tokens to be tracked separately, got %+v", usage)
	}
}

func TestResponsesCompletedStreamUsageNested(t *testing.T) {
	usage, ok := parseStreamUsage(`{"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":9,"total_tokens":21,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}}`)
	if !ok {
		t.Fatal("expected nested Responses stream usage")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 12 || usage.OutputTokens == nil || *usage.OutputTokens != 9 || usage.TotalTokens == nil || *usage.TotalTokens != 21 || usage.CacheHitTokens == nil || *usage.CacheHitTokens != 4 {
		t.Fatalf("expected nested Responses usage fields, got %+v", usage)
	}
}

func TestResponsesOutputItemDoneRecordsBuiltinTool(t *testing.T) {
	result := extractProviderUsageFromStreamEvent(relay.PlatformOpenAI, relay.FormatResponses, `{"type":"response.output_item.done","item":{"type":"web_search_call"}}`)
	if !result.HasUsage || result.Builtin.WebSearchCalls != 1 {
		t.Fatalf("expected Responses output item builtin tool usage, got %+v", result)
	}
}

func TestLocalResponseEstimateDoesNotUseMaxOutputEstimate(t *testing.T) {
	record := &usageRecord{Usage: usageTokenUsage{EstimatedTokens: 1000000, Estimated: true}}
	applyLocalResponseEstimate(record, "hello", config.UsageConfig{CharsPerToken: 1})

	if record.Usage.OutputTokens == nil || *record.Usage.OutputTokens != 5 {
		t.Fatalf("expected local estimate from response text, got %+v", record.Usage)
	}
	if record.Usage.OutputTokens != nil && *record.Usage.OutputTokens == 1000000 {
		t.Fatalf("expected max output estimate not to become actual output tokens, got %+v", record.Usage)
	}
	if record.UsageSource != "local_response_estimate" {
		t.Fatalf("expected local response estimate source, got %q", record.UsageSource)
	}
}

func TestSummarizeUsageIncludesCanonicalDetailAndResponsesModes(t *testing.T) {
	now := time.Now()
	summary := summarizeUsage([]usageRecord{{
		StartedAt:     now,
		StatusCode:    200,
		Stream:        true,
		SourceFormat:  string(relay.FormatResponses),
		ResponsesMode: "native_responses",
		Usage: usageTokenUsage{
			InputTokens:     intPtr(100),
			OutputTokens:    intPtr(50),
			TotalTokens:     intPtr(150),
			CacheHitTokens:  intPtr(25),
			EstimatedTokens: 200,
			Estimated:       true,
		},
		UsageDetail: usageDetail{
			ReasoningTokens: intPtr(7),
		},
		BuiltinToolUsage: builtinToolUsage{
			WebSearchCalls:       1,
			FileSearchCalls:      2,
			ImageGenerationCalls: 3,
		},
	}})

	if summary.Requests != 1 || summary.Success != 1 || summary.StreamRequests != 1 || summary.EstimatedRequests != 1 {
		t.Fatalf("expected request counters, got %+v", summary)
	}
	if summary.ResponsesRequests != 1 || summary.NativeResponsesRequests != 1 || summary.TransformedResponsesRequests != 0 {
		t.Fatalf("expected responses counters, got %+v", summary)
	}
	if summary.InputTokens != 100 || summary.OutputTokens != 50 || summary.TotalTokens != 150 || summary.CacheHitTokens != 25 || summary.EstimatedTokens != 200 || summary.ReasoningTokens != 7 {
		t.Fatalf("expected token counters, got %+v", summary)
	}
	if summary.WebSearchCalls != 1 || summary.FileSearchCalls != 2 || summary.ImageGenerationCalls != 3 {
		t.Fatalf("expected builtin counters, got %+v", summary)
	}
	if summary.CacheHitRate != 0.25 {
		t.Fatalf("expected cache hit rate 0.25, got %f", summary.CacheHitRate)
	}
}

func TestSummarizeUsageTracksFirstAndLastUsedAt(t *testing.T) {
	base := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	summary := summarizeUsage([]usageRecord{
		{StartedAt: base.Add(10 * time.Minute), StatusCode: 200, ModelName: "middle-model"},
		{StartedAt: base, StatusCode: 200, ModelName: "first-model"},
		{StartedAt: base.Add(30 * time.Minute), StatusCode: 200, ModelName: "last-model"},
	})

	if !summary.FirstUsedAt.Equal(base) {
		t.Fatalf("expected first used at %s, got %s", base, summary.FirstUsedAt)
	}
	if !summary.LastUsedAt.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("expected last used at %s, got %s", base.Add(30*time.Minute), summary.LastUsedAt)
	}
	if summary.FirstModelName != "first-model" {
		t.Fatalf("expected first model first-model, got %s", summary.FirstModelName)
	}
	if summary.LastModelName != "last-model" {
		t.Fatalf("expected last model last-model, got %s", summary.LastModelName)
	}
}

// 回归：并发请求可能拿到相同的 UnixNano（Windows 时钟粒度下概率可观），
// 请求 ID 必须带随机后缀，否则 INSERT OR REPLACE 会静默覆盖彼此的记录。
func TestUsageRequestIDUniqueForSameNanosecond(t *testing.T) {
	now := time.Now()
	if usageRequestID(now) == usageRequestID(now) {
		t.Fatal("usage request ids must carry a random suffix")
	}
}

// 回归：时间窗口按本地时钟对齐。t.Truncate 按 Unix 纪元取整，在非整小时
// 时区（如 UTC+5:30）会把"小时"桶切到半点上，图表标签与数据错位。
func TestTruncateUsageWindowAlignsToLocalClock(t *testing.T) {
	zone := time.FixedZone("IST", 5*3600+30*60)
	input := time.Date(2026, 8, 1, 12, 7, 33, 0, zone)

	hour := truncateUsageWindow(input, "hour")
	if hour.Hour() != 12 || hour.Minute() != 0 {
		t.Fatalf("hour bucket must align to the local clock hour, got %v", hour)
	}
	five := truncateUsageWindow(input, "5m")
	if five.Minute() != 5 {
		t.Fatalf("5m bucket must align to local 5-minute boundary, got %v", five)
	}
	fifteen := truncateUsageWindow(input, "15m")
	if fifteen.Minute() != 0 {
		t.Fatalf("15m bucket must align to local 15-minute boundary, got %v", fifteen)
	}
}
