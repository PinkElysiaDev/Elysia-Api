package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	FormatOpenAIChat FormatType = "openai_chat"
	FormatResponses  FormatType = "openai_responses"
)

const (
	CanonicalContentText       = "text"
	CanonicalContentImage      = "image"
	CanonicalContentFile       = "file"
	CanonicalContentToolOutput = "tool_output"
	CanonicalContentReasoning  = "reasoning"

	CanonicalInputMessage            = "message"
	CanonicalInputFunctionCallOutput = "function_call_output"
	CanonicalInputItemReference      = "item_reference"

	CanonicalToolFunction           = "function"
	CanonicalToolWebSearchPreview   = "web_search_preview"
	CanonicalToolFileSearch         = "file_search"
	CanonicalToolComputerUsePreview = "computer_use_preview"
	CanonicalToolCodeInterpreter    = "code_interpreter"
	CanonicalToolImageGeneration    = "image_generation"

	CanonicalOutputMessage             = "message"
	CanonicalOutputFunctionCall        = "function_call"
	CanonicalOutputReasoning           = "reasoning"
	CanonicalOutputWebSearchCall       = "web_search_call"
	CanonicalOutputFileSearchCall      = "file_search_call"
	CanonicalOutputImageGenerationCall = "image_generation_call"

	CanonicalEventResponseCreated            = "response.created"
	CanonicalEventResponseInProgress         = "response.in_progress"
	CanonicalEventOutputItemAdded            = "response.output_item.added"
	CanonicalEventContentPartAdded           = "response.content_part.added"
	CanonicalEventTextDelta                  = "response.output_text.delta"
	CanonicalEventTextDone                   = "response.output_text.done"
	CanonicalEventReasoningDelta             = "response.reasoning.delta"
	CanonicalEventReasoningDone              = "response.reasoning.done"
	CanonicalEventFunctionCallAdded          = "response.function_call.added"
	CanonicalEventFunctionCallArgumentsDelta = "response.function_call_arguments.delta"
	CanonicalEventFunctionCallArgumentsDone  = "response.function_call_arguments.done"
	CanonicalEventOutputItemDone             = "response.output_item.done"
	CanonicalEventUsageDelta                 = "response.usage.delta"
	CanonicalEventResponseCompleted          = "response.completed"
	CanonicalEventResponseFailed             = "response.failed"
)

type CanonicalRequest struct {
	Model        string `json:"model"`
	Instructions string `json:"instructions,omitempty"`

	Messages   []CanonicalMessage   `json:"messages,omitempty"`
	InputItems []CanonicalInputItem `json:"input_items,omitempty"`

	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	TopK            *int     `json:"top_k,omitempty"`
	Stop            any      `json:"stop,omitempty"`

	Stream        bool                    `json:"stream,omitempty"`
	StreamOptions *CanonicalStreamOptions `json:"stream_options,omitempty"`

	Tools             []CanonicalTool `json:"tools,omitempty"`
	ToolChoice        any             `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`

	ResponseFormat *CanonicalResponseFormat `json:"response_format,omitempty"`
	Reasoning      *CanonicalReasoning      `json:"reasoning,omitempty"`
	Thinking       *CanonicalThinking       `json:"thinking,omitempty"`

	User     string         `json:"user,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`

	PreviousResponseID string   `json:"previous_response_id,omitempty"`
	Store              *bool    `json:"store,omitempty"`
	Include            []string `json:"include,omitempty"`
	Truncation         string   `json:"truncation,omitempty"`
	Background         *bool    `json:"background,omitempty"`
	Conversation       any      `json:"conversation,omitempty"`
	Prompt             any      `json:"prompt,omitempty"`

	PromptCacheKey       string          `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`

	RawExtra map[string]json.RawMessage `json:"-"`
}

type CanonicalStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type CanonicalMessage struct {
	Role       string                     `json:"role"`
	Content    []CanonicalContentPart     `json:"content,omitempty"`
	ToolCalls  []CanonicalToolCall        `json:"tool_calls,omitempty"`
	ToolCallID string                     `json:"tool_call_id,omitempty"`
	RawExtra   map[string]json.RawMessage `json:"-"`
}

type CanonicalContentPart struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	ImageURL    string `json:"image_url,omitempty"`
	ImageBase64 string `json:"image_base64,omitempty"`
	MediaType   string `json:"media_type,omitempty"`

	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileData string `json:"file_data,omitempty"`

	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolOutput string `json:"tool_output,omitempty"`

	ReasoningText string `json:"reasoning_text,omitempty"`

	Raw any `json:"raw,omitempty"`
}

type CanonicalInputItem struct {
	Type     string                     `json:"type"`
	Role     string                     `json:"role,omitempty"`
	Content  []CanonicalContentPart     `json:"content,omitempty"`
	CallID   string                     `json:"call_id,omitempty"`
	Output   string                     `json:"output,omitempty"`
	ItemID   string                     `json:"item_id,omitempty"`
	RawExtra map[string]json.RawMessage `json:"-"`
}

type CanonicalTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`

	SearchContextSize string   `json:"search_context_size,omitempty"`
	VectorStoreIDs    []string `json:"vector_store_ids,omitempty"`

	Raw map[string]any `json:"raw,omitempty"`
}

type CanonicalToolCall struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type CanonicalReasoning struct {
	Effort           string         `json:"effort,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	Text             string         `json:"text,omitempty"`
	EncryptedContent string         `json:"encrypted_content,omitempty"`
	Raw              map[string]any `json:"raw,omitempty"`
}

type CanonicalThinking struct {
	Enabled      bool   `json:"enabled"`
	Effort       string `json:"effort,omitempty"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type CanonicalResponseFormat struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
	Raw         map[string]any `json:"raw,omitempty"`
}

type CanonicalResponse struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"`

	Output []CanonicalOutputItem `json:"output,omitempty"`

	StopReason string `json:"stop_reason,omitempty"`

	Usage *CanonicalUsage `json:"usage,omitempty"`
	Error *CanonicalError `json:"error,omitempty"`

	RawExtra map[string]json.RawMessage `json:"-"`
}

type CanonicalOutputItem struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
	Role   string `json:"role,omitempty"`

	Content []CanonicalContentPart `json:"content,omitempty"`

	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	Summary []CanonicalReasoningSummary `json:"summary,omitempty"`

	Raw map[string]any `json:"raw,omitempty"`
}

type CanonicalReasoningSummary struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type CanonicalUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	CachedInputTokens        int `json:"cached_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`

	TextInputTokens   int `json:"text_input_tokens,omitempty"`
	TextOutputTokens  int `json:"text_output_tokens,omitempty"`
	ImageInputTokens  int `json:"image_input_tokens,omitempty"`
	ImageOutputTokens int `json:"image_output_tokens,omitempty"`
	AudioInputTokens  int `json:"audio_input_tokens,omitempty"`
	AudioOutputTokens int `json:"audio_output_tokens,omitempty"`
	ToolUseTokens     int `json:"tool_use_tokens,omitempty"`

	WebSearchCallCount       int `json:"web_search_call_count,omitempty"`
	FileSearchCallCount      int `json:"file_search_call_count,omitempty"`
	ImageGenerationCallCount int `json:"image_generation_call_count,omitempty"`
	CodeInterpreterCallCount int `json:"code_interpreter_call_count,omitempty"`
	ComputerUseCallCount     int `json:"computer_use_call_count,omitempty"`

	EstimatedInputTokens  int  `json:"estimated_input_tokens,omitempty"`
	EstimatedOutputTokens int  `json:"estimated_output_tokens,omitempty"`
	EstimatedTotalTokens  int  `json:"estimated_total_tokens,omitempty"`
	Estimated             bool `json:"estimated,omitempty"`

	Source string         `json:"source,omitempty"`
	Raw    map[string]any `json:"raw,omitempty"`
}

type CanonicalError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

type CanonicalStreamEvent struct {
	Type string `json:"type"`

	ResponseID string `json:"response_id,omitempty"`
	ItemID     string `json:"item_id,omitempty"`

	OutputIndex  int `json:"output_index,omitempty"`
	ContentIndex int `json:"content_index,omitempty"`

	Role string `json:"role,omitempty"`

	Delta    string `json:"delta,omitempty"`
	TextDone string `json:"text_done,omitempty"`

	ToolCallID         string `json:"tool_call_id,omitempty"`
	ToolName           string `json:"tool_name,omitempty"`
	ToolArgumentsDelta string `json:"tool_arguments_delta,omitempty"`
	ToolArgumentsDone  string `json:"tool_arguments_done,omitempty"`
	ReasoningDelta     string `json:"reasoning_delta,omitempty"`

	Usage    *CanonicalUsage    `json:"usage,omitempty"`
	Response *CanonicalResponse `json:"response,omitempty"`
	Error    *CanonicalError    `json:"error,omitempty"`

	Raw map[string]any `json:"raw,omitempty"`
}

type OpenAIResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input,omitempty"`
	Instructions string          `json:"instructions,omitempty"`

	MaxOutputTokens *uint `json:"max_output_tokens,omitempty"`

	Metadata           map[string]any `json:"metadata,omitempty"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Stream             *bool          `json:"stream,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Truncation  string   `json:"truncation,omitempty"`
	User        string   `json:"user,omitempty"`

	Tools      []map[string]any `json:"tools,omitempty"`
	ToolChoice any              `json:"tool_choice,omitempty"`

	Reasoning map[string]any `json:"reasoning,omitempty"`
	Text      map[string]any `json:"text,omitempty"`

	Include      []string `json:"include,omitempty"`
	Background   *bool    `json:"background,omitempty"`
	Conversation any      `json:"conversation,omitempty"`
	Prompt       any      `json:"prompt,omitempty"`

	RawExtra map[string]json.RawMessage `json:"-"`
}

type OpenAIResponsesResponse struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Status    string            `json:"status"`
	Model     string            `json:"model"`
	Output    []ResponsesOutput `json:"output"`
	Usage     *ResponsesUsage   `json:"usage,omitempty"`
	Error     any               `json:"error,omitempty"`
}

type ResponsesOutput struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
	Role   string `json:"role,omitempty"`

	Content []ResponsesOutputContent `json:"content,omitempty"`

	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	Summary []ResponsesReasoningSummaryPart `json:"summary,omitempty"`

	Quality string `json:"quality,omitempty"`
	Size    string `json:"size,omitempty"`
}

type ResponsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ResponsesReasoningSummaryPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	InputTokensDetails  *ResponsesInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *ResponsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type ResponsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type ResponsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type ResponsesStreamResponse struct {
	Type         string                   `json:"type"`
	Response     *OpenAIResponsesResponse `json:"response,omitempty"`
	Delta        string                   `json:"delta,omitempty"`
	Item         *ResponsesOutput         `json:"item,omitempty"`
	ItemID       string                   `json:"item_id,omitempty"`
	OutputIndex  int                      `json:"output_index,omitempty"`
	ContentIndex int                      `json:"content_index,omitempty"`
	Part         any                      `json:"part,omitempty"`
}

func contentPartsToInterface(parts []CanonicalContentPart) any {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 && parts[0].Type == CanonicalContentText {
		return parts[0].Text
	}
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case CanonicalContentText:
			out = append(out, map[string]any{"type": "text", "text": part.Text})
		case CanonicalContentImage:
			if url := imagePartToOpenAIURL(part); url != "" {
				out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
		default:
			if part.Raw != nil {
				out = append(out, part.Raw)
			}
		}
	}
	return out
}

func interfaceToContentParts(content any) []CanonicalContentPart {
	if content == nil {
		return nil
	}
	if s, ok := content.(string); ok {
		return []CanonicalContentPart{{Type: CanonicalContentText, Text: s}}
	}
	arr, ok := content.([]any)
	if !ok {
		return []CanonicalContentPart{{Type: CanonicalContentText, Text: fmt.Sprintf("%v", content)}}
	}
	parts := make([]CanonicalContentPart, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		switch t {
		case "text", "input_text", "output_text":
			text, _ := m["text"].(string)
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentText, Text: text, Raw: m})
		case "image_url", "input_image", "image":
			url := ""
			if imageURL, ok := m["image_url"].(map[string]any); ok {
				url, _ = imageURL["url"].(string)
			}
			if url == "" {
				url, _ = m["image_url"].(string)
			}
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentImage, ImageURL: url, Raw: m})
		case "input_file", "file":
			fileID, _ := m["file_id"].(string)
			fileName, _ := m["filename"].(string)
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentFile, FileID: fileID, FileName: fileName, Raw: m})
		default:
			parts = append(parts, CanonicalContentPart{Type: t, Raw: m})
		}
	}
	return parts
}

func canonicalText(parts []CanonicalContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Type == CanonicalContentText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func newCanonicalResponseID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
