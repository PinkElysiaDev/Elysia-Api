package builtin

// Platform is the stored upstream label used for usage records.
type Platform string

// FormatType selects public ingress envelopes before a definition is pinned.
type FormatType string

const (
	FormatOpenAI     FormatType = "openai"
	FormatOpenAIChat FormatType = "openai_chat"
	FormatResponses  FormatType = "openai_responses"
	FormatGemini     FormatType = "gemini"
	FormatClaude     FormatType = "claude"
)
