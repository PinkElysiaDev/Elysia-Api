package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	FormatOpenAIChat      FormatType = "openai_chat"
	FormatResponses       FormatType = "openai_responses"
	FormatChatCompletions FormatType = FormatOpenAIChat
)

const (
	CanonicalSignatureProviderAnthropic  = "anthropic"
	CanonicalSignatureProviderGemini     = "gemini"
	CanonicalSignatureProviderOpenAI     = "openai"
	CanonicalSignatureProviderMaheshvara = "maheshvara"

	CanonicalContentText       = "text"
	CanonicalContentImage      = "image"
	CanonicalContentAudio      = "audio"
	CanonicalContentVideo      = "video"
	CanonicalContentFile       = "file"
	CanonicalContentDocument   = "document"
	CanonicalContentToolOutput = "tool_output"
	CanonicalContentToolCall   = "tool_call"
	CanonicalContentReasoning  = "reasoning"
	CanonicalContentRefusal    = "refusal"

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
	CanonicalEventContentPartDone            = "response.content_part.done"
	CanonicalEventTextDelta                  = "response.output_text.delta"
	CanonicalEventTextDone                   = "response.output_text.done"
	CanonicalEventReasoningDelta             = "response.reasoning.delta"
	CanonicalEventReasoningDone              = "response.reasoning.done"
	CanonicalEventReasoningSummaryDelta      = "response.reasoning_summary_text.delta"
	CanonicalEventReasoningSummaryDone       = "response.reasoning_summary_text.done"
	CanonicalEventReasoningSignatureDelta    = "response.reasoning_signature.delta"
	CanonicalEventRefusalDelta               = "response.refusal.delta"
	CanonicalEventRefusalDone                = "response.refusal.done"
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
	MinOutputTokens int      `json:"min_output_tokens,omitempty"`
	MaxToolCalls    *int     `json:"max_tool_calls,omitempty"`
	N               *int     `json:"n,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	TopK            *int     `json:"top_k,omitempty"`
	Stop            any      `json:"stop,omitempty"`
	Seed            *int64   `json:"seed,omitempty"`

	PresencePenalty   *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty  *float64 `json:"frequency_penalty,omitempty"`
	RepetitionPenalty *float64 `json:"repetition_penalty,omitempty"`
	LogProbs          *bool    `json:"logprobs,omitempty"`
	TopLogProbs       *int     `json:"top_logprobs,omitempty"`
	TypicalP          *float64 `json:"typical_p,omitempty"`
	MinP              *float64 `json:"min_p,omitempty"`
	TopA              *float64 `json:"top_a,omitempty"`

	Stream        bool                    `json:"stream,omitempty"`
	StreamOptions *CanonicalStreamOptions `json:"stream_options,omitempty"`

	Tools             []CanonicalTool `json:"tools,omitempty"`
	ToolChoice        any             `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`

	ResponseFormat   *CanonicalResponseFormat `json:"response_format,omitempty"`
	Reasoning        *CanonicalReasoning      `json:"reasoning,omitempty"`
	Thinking         *CanonicalThinking       `json:"thinking,omitempty"`
	Modalities       []string                 `json:"modalities,omitempty"`
	Audio            *CanonicalAudioConfig    `json:"audio,omitempty"`
	Prediction       any                      `json:"prediction,omitempty"`
	ServiceTier      string                   `json:"service_tier,omitempty"`
	SafetyIdentifier string                   `json:"safety_identifier,omitempty"`
	Verbosity        string                   `json:"verbosity,omitempty"`
	SafetySettings   []CanonicalSafetySetting `json:"safety_settings,omitempty"`
	CacheControl     any                      `json:"cache_control,omitempty"`

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
	RequestID            string          `json:"request_id,omitempty"`
	SessionID            string          `json:"session_id,omitempty"`
	TimeoutMS            int             `json:"timeout_ms,omitempty"`

	RawExtra map[string]json.RawMessage `json:"-"`
}

type CanonicalStreamOptions struct {
	IncludeUsage       bool           `json:"include_usage,omitempty"`
	IncludeObfuscation *bool          `json:"include_obfuscation,omitempty"`
	Raw                map[string]any `json:"raw,omitempty"`
}

type CanonicalMessage struct {
	Role         string                     `json:"role"`
	Content      []CanonicalContentPart     `json:"content,omitempty"`
	ToolCalls    []CanonicalToolCall        `json:"tool_calls,omitempty"`
	ToolCallID   string                     `json:"tool_call_id,omitempty"`
	Name         string                     `json:"name,omitempty"`
	Audio        *CanonicalAudioConfig      `json:"audio,omitempty"`
	CacheControl any                        `json:"cache_control,omitempty"`
	Metadata     map[string]any             `json:"metadata,omitempty"`
	RawExtra     map[string]json.RawMessage `json:"-"`
}

type CanonicalContentPart struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	ImageURL    string `json:"image_url,omitempty"`
	ImageBase64 string `json:"image_base64,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	URI         string `json:"uri,omitempty"`
	Detail      string `json:"detail,omitempty"`

	AudioURL    string `json:"audio_url,omitempty"`
	AudioBase64 string `json:"audio_base64,omitempty"`
	VideoURL    string `json:"video_url,omitempty"`
	VideoBase64 string `json:"video_base64,omitempty"`
	Data        string `json:"data,omitempty"`
	Thought     bool   `json:"thought,omitempty"`

	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileData string `json:"file_data,omitempty"`

	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolOutput string `json:"tool_output,omitempty"`

	ReasoningText     string                      `json:"reasoning_text,omitempty"`
	Signature         string                      `json:"signature,omitempty"`
	SignatureProvider string                      `json:"signature_provider,omitempty"`
	EncryptedContent  string                      `json:"encrypted_content,omitempty"`
	ReasoningSummary  []CanonicalReasoningSummary `json:"reasoning_summary,omitempty"`
	CacheControl      any                         `json:"cache_control,omitempty"`
	Annotations       []map[string]any            `json:"annotations,omitempty"`
	Metadata          map[string]any              `json:"metadata,omitempty"`

	Raw any `json:"raw,omitempty"`
}

type CanonicalInputItem struct {
	Type      string                     `json:"type"`
	Role      string                     `json:"role,omitempty"`
	Content   []CanonicalContentPart     `json:"content,omitempty"`
	CallID    string                     `json:"call_id,omitempty"`
	Output    string                     `json:"output,omitempty"`
	ItemID    string                     `json:"item_id,omitempty"`
	Reasoning *CanonicalReasoning        `json:"reasoning,omitempty"`
	RawExtra  map[string]json.RawMessage `json:"-"`
}

type CanonicalTool struct {
	Type         string         `json:"type"`
	Name         string         `json:"name,omitempty"`
	Description  string         `json:"description,omitempty"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	Strict       *bool          `json:"strict,omitempty"`
	Provider     string         `json:"provider,omitempty"`
	Config       map[string]any `json:"config,omitempty"`
	CacheControl any            `json:"cache_control,omitempty"`

	SearchContextSize string   `json:"search_context_size,omitempty"`
	VectorStoreIDs    []string `json:"vector_store_ids,omitempty"`

	Raw map[string]any `json:"raw,omitempty"`
}

type CanonicalToolCall struct {
	ID                       string          `json:"id,omitempty"`
	Type                     string          `json:"type"`
	Name                     string          `json:"name,omitempty"`
	Arguments                json.RawMessage `json:"arguments,omitempty"`
	ArgumentsText            string          `json:"arguments_text,omitempty"`
	ThoughtSignature         string          `json:"thought_signature,omitempty"`
	ThoughtSignatureProvider string          `json:"thought_signature_provider,omitempty"`
	Raw                      map[string]any  `json:"raw,omitempty"`
}

type CanonicalReasoning struct {
	Effort           string                      `json:"effort,omitempty"`
	Summary          string                      `json:"summary,omitempty"`
	SummaryParts     []CanonicalReasoningSummary `json:"summary_parts,omitempty"`
	Text             string                      `json:"text,omitempty"`
	EncryptedContent string                      `json:"encrypted_content,omitempty"`
	Raw              map[string]any              `json:"raw,omitempty"`
}

type CanonicalThinking struct {
	Enabled        bool   `json:"enabled"`
	Effort         string `json:"effort,omitempty"`
	BudgetTokens   int    `json:"budget_tokens,omitempty"`
	IncludeSummary bool   `json:"include_summary,omitempty"`
}

type CanonicalAudioConfig struct {
	Voice      string `json:"voice,omitempty"`
	Format     string `json:"format,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

type CanonicalSafetySetting struct {
	Category  string `json:"category,omitempty"`
	Threshold string `json:"threshold,omitempty"`
	Action    string `json:"action,omitempty"`
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

	StopReason        string         `json:"stop_reason,omitempty"`
	IncompleteDetails map[string]any `json:"incomplete_details,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	ServiceTier       string         `json:"service_tier,omitempty"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`

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

	CallID    string              `json:"call_id,omitempty"`
	Name      string              `json:"name,omitempty"`
	Arguments json.RawMessage     `json:"arguments,omitempty"`
	ToolCalls []CanonicalToolCall `json:"tool_calls,omitempty"`
	Reasoning *CanonicalReasoning `json:"reasoning,omitempty"`

	Summary  []CanonicalReasoningSummary `json:"summary,omitempty"`
	Metadata map[string]any              `json:"metadata,omitempty"`

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

	TextInputTokens          int `json:"text_input_tokens,omitempty"`
	TextOutputTokens         int `json:"text_output_tokens,omitempty"`
	ImageInputTokens         int `json:"image_input_tokens,omitempty"`
	ImageOutputTokens        int `json:"image_output_tokens,omitempty"`
	AudioInputTokens         int `json:"audio_input_tokens,omitempty"`
	AudioOutputTokens        int `json:"audio_output_tokens,omitempty"`
	ToolUseTokens            int `json:"tool_use_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`

	WebSearchCallCount       int `json:"web_search_call_count,omitempty"`
	FileSearchCallCount      int `json:"file_search_call_count,omitempty"`
	ImageGenerationCallCount int `json:"image_generation_call_count,omitempty"`
	CodeInterpreterCallCount int `json:"code_interpreter_call_count,omitempty"`
	ComputerUseCallCount     int `json:"computer_use_call_count,omitempty"`

	EstimatedInputTokens  int  `json:"estimated_input_tokens,omitempty"`
	EstimatedOutputTokens int  `json:"estimated_output_tokens,omitempty"`
	EstimatedTotalTokens  int  `json:"estimated_total_tokens,omitempty"`
	Estimated             bool `json:"estimated,omitempty"`

	Source   string         `json:"source,omitempty"`
	Raw      map[string]any `json:"raw,omitempty"`
	Provider string         `json:"provider,omitempty"`
}

type CanonicalError struct {
	Message string         `json:"message"`
	Type    string         `json:"type,omitempty"`
	Param   string         `json:"param,omitempty"`
	Code    string         `json:"code,omitempty"`
	Details any            `json:"details,omitempty"`
	Raw     map[string]any `json:"raw,omitempty"`
}

type CanonicalStreamEvent struct {
	Type string `json:"type"`

	ResponseID string `json:"response_id,omitempty"`
	ItemID     string `json:"item_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Status     string `json:"status,omitempty"`

	OutputIndex  int `json:"output_index,omitempty"`
	ContentIndex int `json:"content_index,omitempty"`

	Role string `json:"role,omitempty"`

	Delta    string `json:"delta,omitempty"`
	TextDone string `json:"text_done,omitempty"`

	ToolCallID                 string `json:"tool_call_id,omitempty"`
	ToolCallIndex              int    `json:"tool_call_index,omitempty"`
	ToolName                   string `json:"tool_name,omitempty"`
	ToolArgumentsDelta         string `json:"tool_arguments_delta,omitempty"`
	ToolArgumentsDone          string `json:"tool_arguments_done,omitempty"`
	ReasoningDelta             string `json:"reasoning_delta,omitempty"`
	ReasoningDone              string `json:"reasoning_done,omitempty"`
	ReasoningSignatureDelta    string `json:"reasoning_signature_delta,omitempty"`
	ReasoningSignatureProvider string `json:"reasoning_signature_provider,omitempty"`
	RefusalDelta               string `json:"refusal_delta,omitempty"`
	RefusalDone                string `json:"refusal_done,omitempty"`
	FinishReason               string `json:"finish_reason,omitempty"`
	StopSequence               string `json:"stop_sequence,omitempty"`
	ChoiceIndex                int    `json:"choice_index,omitempty"`
	Sequence                   int64  `json:"sequence,omitempty"`
	CreatedAt                  int64  `json:"created_at,omitempty"`

	Usage       *CanonicalUsage       `json:"usage,omitempty"`
	Response    *CanonicalResponse    `json:"response,omitempty"`
	OutputItem  *CanonicalOutputItem  `json:"output_item,omitempty"`
	ContentPart *CanonicalContentPart `json:"content_part,omitempty"`
	Error       *CanonicalError       `json:"error,omitempty"`

	Raw map[string]any `json:"raw,omitempty"`
}

// Maheshvara is the stable internal protocol name. These aliases preserve
// the existing Canonical API while exposing the new protocol vocabulary.
type MaheshvaraRequest = CanonicalRequest
type MaheshvaraMessage = CanonicalMessage
type MaheshvaraContentPart = CanonicalContentPart
type MaheshvaraInputItem = CanonicalInputItem
type MaheshvaraTool = CanonicalTool
type MaheshvaraToolCall = CanonicalToolCall
type MaheshvaraReasoning = CanonicalReasoning
type MaheshvaraResponse = CanonicalResponse
type MaheshvaraOutputItem = CanonicalOutputItem
type MaheshvaraUsage = CanonicalUsage
type MaheshvaraError = CanonicalError
type MaheshvaraStreamEvent = CanonicalStreamEvent

type OpenAIResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input,omitempty"`
	Instructions string          `json:"instructions,omitempty"`

	MaxOutputTokens *uint `json:"max_output_tokens,omitempty"`
	MaxToolCalls    *uint `json:"max_tool_calls,omitempty"`

	Metadata           map[string]any `json:"metadata,omitempty"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Stream             *bool          `json:"stream,omitempty"`

	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopLogprobs      *int     `json:"top_logprobs,omitempty"`
	Truncation       string   `json:"truncation,omitempty"`
	User             string   `json:"user,omitempty"`
	SafetyIdentifier string   `json:"safety_identifier,omitempty"`
	ServiceTier      string   `json:"service_tier,omitempty"`

	Tools      []map[string]any `json:"tools,omitempty"`
	ToolChoice any              `json:"tool_choice,omitempty"`

	Reasoning map[string]any `json:"reasoning,omitempty"`
	Text      map[string]any `json:"text,omitempty"`

	Include              []string        `json:"include,omitempty"`
	Background           *bool           `json:"background,omitempty"`
	Conversation         any             `json:"conversation,omitempty"`
	Prompt               any             `json:"prompt,omitempty"`
	StreamOptions        map[string]any  `json:"stream_options,omitempty"`
	PromptCacheKey       string          `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`

	RawExtra map[string]json.RawMessage `json:"-"`
}

type OpenAIResponsesResponse struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	CreatedAt         int64             `json:"created_at"`
	Status            string            `json:"status"`
	Model             string            `json:"model"`
	Output            []ResponsesOutput `json:"output"`
	Usage             *ResponsesUsage   `json:"usage,omitempty"`
	Error             any               `json:"error,omitempty"`
	IncompleteDetails map[string]any    `json:"incomplete_details,omitempty"`
	Metadata          map[string]any    `json:"metadata,omitempty"`
	ServiceTier       string            `json:"service_tier,omitempty"`
}

type ResponsesOutput struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
	Role   string `json:"role,omitempty"`

	Content []ResponsesOutputContent `json:"content,omitempty"`

	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Input            string          `json:"input,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	Metadata         map[string]any  `json:"metadata,omitempty"`

	Summary []ResponsesReasoningSummaryPart `json:"summary,omitempty"`

	Quality string `json:"quality,omitempty"`
	Size    string `json:"size,omitempty"`
}

type ResponsesOutputContent struct {
	Type        string           `json:"type"`
	Text        string           `json:"text,omitempty"`
	Refusal     string           `json:"refusal,omitempty"`
	ImageURL    string           `json:"image_url,omitempty"`
	FileID      string           `json:"file_id,omitempty"`
	FileURL     string           `json:"file_url,omitempty"`
	Filename    string           `json:"filename,omitempty"`
	Audio       map[string]any   `json:"audio,omitempty"`
	Annotations []map[string]any `json:"annotations,omitempty"`
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
				image := map[string]any{"url": url}
				if part.Detail != "" {
					image["detail"] = part.Detail
				}
				out = append(out, map[string]any{"type": "image_url", "image_url": image})
			}
		case CanonicalContentAudio:
			// OpenAI Chat 协议要求 input_audio.data 为裸 base64、format 为
			// mp3/wav 短格式；data: URI 或完整 MIME 会被上游校验拒绝。
			data := firstNonEmptyString(part.AudioBase64, part.Data)
			mediaType := firstNonEmptyString(part.MediaType, part.MimeType)
			if uriMime, b64, isURI := splitAudioDataURL(data); isURI {
				data = b64
				if mediaType == "" {
					mediaType = uriMime
				}
			}
			if data != "" {
				out = append(out, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": data, "format": audioFormatFromMediaType(mediaType)}})
			}
		case CanonicalContentVideo:
			url := firstNonEmptyString(part.VideoURL, part.URI)
			if url == "" && part.VideoBase64 != "" {
				url = "data:" + firstNonEmptyString(part.MediaType, part.MimeType, "video/mp4") + ";base64," + part.VideoBase64
			}
			if url != "" {
				out = append(out, map[string]any{"type": "video_url", "video_url": map[string]any{"url": url}})
			}
		case CanonicalContentFile, CanonicalContentDocument:
			file := map[string]any{"type": "file"}
			if part.FileID != "" {
				file["file_id"] = part.FileID
			}
			if part.FileName != "" {
				file["filename"] = part.FileName
			}
			if part.FileData != "" {
				file["file_data"] = part.FileData
			}
			if part.URI != "" {
				file["file_url"] = part.URI
			}
			if part.MediaType != "" {
				file["mime_type"] = part.MediaType
			}
			if len(file) > 1 {
				out = append(out, file)
			}
		case CanonicalContentReasoning:
			text := firstNonEmptyString(part.ReasoningText, part.Text)
			if text != "" {
				out = append(out, map[string]any{"type": "text", "text": text, "thought": true})
			}
		case CanonicalContentToolOutput:
			if part.ToolOutput != "" {
				out = append(out, map[string]any{"type": "text", "text": part.ToolOutput})
			}
		case CanonicalContentRefusal:
			if part.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": part.Text})
			}
		default:
			if part.Raw != nil {
				out = append(out, part.Raw)
			}
		}
	}
	return out
}

// splitAudioDataURL 解析 "data:<mime>;base64,<payload>" 形式的音频数据，
// 返回 mime 与裸 base64；非 data URI 或缺少逗号时 ok=false。
func splitAudioDataURL(data string) (mime, base64 string, ok bool) {
	if !strings.HasPrefix(data, "data:") {
		return "", "", false
	}
	rest := data[len("data:"):]
	comma := strings.Index(rest, ",")
	if comma < 0 {
		return "", "", false
	}
	meta := rest[:comma]
	mime = meta
	if strings.HasSuffix(meta, ";base64") {
		mime = meta[:len(meta)-len(";base64")]
	}
	return mime, rest[comma+1:], true
}

// audioFormatFromMediaType 把 MIME 类型或既有短格式归一化为 OpenAI
// input_audio.format 接受的 "mp3"/"wav"。
func audioFormatFromMediaType(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "wav", "wave", "audio/wav", "audio/wave", "audio/x-wav":
		return "wav"
	default:
		// audio/mpeg、audio/mp3、mp3 及未知类型统一归 mp3（OpenAI 仅接受 mp3/wav）。
		return "mp3"
	}
}

func interfaceToContentParts(content any) []CanonicalContentPart {
	if content == nil {
		return nil
	}
	if s, ok := content.(string); ok {
		return []CanonicalContentPart{{Type: CanonicalContentText, Text: s}}
	}
	if object, ok := content.(map[string]any); ok {
		return interfaceToContentParts([]any{object})
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
		case "reasoning", "thinking", "reasoning_content":
			text := firstNonEmptyString(stringValue(m["text"]), stringValue(m["thinking"]), stringValue(m["content"]))
			if text != "" {
				parts = append(parts, CanonicalContentPart{Type: CanonicalContentReasoning, Text: text, ReasoningText: text, Thought: true, Raw: m})
			}
		case "image_url", "input_image", "image":
			url := ""
			if imageURL, ok := m["image_url"].(map[string]any); ok {
				url, _ = imageURL["url"].(string)
			}
			if url == "" {
				url, _ = m["image_url"].(string)
			}
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentImage, ImageURL: url, Raw: m})
		case "input_audio", "audio", "audio_url", "output_audio":
			nested, _ := m["input_audio"].(map[string]any)
			if nested == nil {
				nested, _ = m["audio"].(map[string]any)
			}
			if nested == nil {
				nested, _ = m["audio_url"].(map[string]any)
			}
			dataVal := firstNonEmptyString(stringValue(m["data"]), stringValue(nested["data"]), stringValue(nested["audio_data"]))
			mediaVal := firstNonEmptyString(stringValue(m["format"]), stringValue(m["mime_type"]), stringValue(nested["format"]), stringValue(nested["mime_type"]), stringValue(nested["mimeType"]))
			if uriMime, b64, isURI := splitAudioDataURL(dataVal); isURI {
				dataVal = b64
				if mediaVal == "" {
					mediaVal = uriMime
				}
			}
			parts = append(parts, CanonicalContentPart{
				Type:        CanonicalContentAudio,
				AudioURL:    firstNonEmptyString(stringValue(m["audio_url"]), stringValue(nested["url"]), stringValue(nested["audio_url"])),
				AudioBase64: dataVal,
				Data:        dataVal,
				Text:        firstNonEmptyString(stringValue(m["transcript"]), stringValue(nested["transcript"])),
				MediaType:   mediaVal,
				Raw:         m,
			})
		case "video_url", "input_video", "video":
			videoURL := stringValue(m["video_url"])
			if nested, ok := m["video_url"].(map[string]any); ok {
				videoURL = stringValue(nested["url"])
			}
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentVideo, VideoURL: videoURL, URI: videoURL, Raw: m})
		case "input_file", "file":
			file, _ := m["file"].(map[string]any)
			parts = append(parts, CanonicalContentPart{
				Type:     CanonicalContentFile,
				FileID:   firstNonEmptyString(stringValue(m["file_id"]), stringValue(file["file_id"]), stringValue(file["id"])),
				FileName: firstNonEmptyString(stringValue(m["filename"]), stringValue(file["filename"]), stringValue(file["name"])),
				FileData: firstNonEmptyString(stringValue(m["file_data"]), stringValue(file["file_data"]), stringValue(file["data"])),
				URI:      firstNonEmptyString(stringValue(m["file_url"]), stringValue(file["file_url"]), stringValue(file["url"])),
				Raw:      m,
			})
		case "document":
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentDocument, FileID: stringValue(m["file_id"]), FileName: stringValue(m["filename"]), FileData: stringValue(m["file_data"]), Raw: m})
		case "refusal":
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentRefusal, Text: firstNonEmptyString(stringValue(m["refusal"]), stringValue(m["text"])), Raw: m})
		case "tool_result", "function_response":
			parts = append(parts, CanonicalContentPart{Type: CanonicalContentToolOutput, ToolCallID: firstNonEmptyString(stringValue(m["tool_call_id"]), stringValue(m["call_id"])), ToolOutput: firstNonEmptyString(contentValueToString(m["content"]), contentValueToString(m["output"]), contentValueToString(m["response"])), Raw: m})
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

func contentValueToString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

func newCanonicalResponseID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
