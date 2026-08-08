package relay

// The explicit Maheshvara names make the internal protocol usable by new
// callers without forcing them to depend on the historical Canonical names.

func OpenAIChatToMaheshvara(body []byte) (*MaheshvaraRequest, error) {
	return OpenAIChatRequestToCanonical(body)
}

func AnthropicToMaheshvara(body []byte) (*MaheshvaraRequest, error) {
	return ClaudeRequestToCanonical(body)
}

func GeminiToMaheshvara(body []byte, urlModel string) (*MaheshvaraRequest, error) {
	return GeminiRequestToCanonical(body, urlModel)
}

func OpenAIResponsesToMaheshvara(body []byte) (*MaheshvaraRequest, *OpenAIResponsesRequest, error) {
	return ResponsesRequestToCanonical(body)
}

func MaheshvaraToOpenAIChat(req *MaheshvaraRequest) ([]byte, error) {
	return CanonicalToOpenAIChatRequest(req)
}

func MaheshvaraToAnthropic(req *MaheshvaraRequest) ([]byte, error) {
	return CanonicalToClaudeRequest(req)
}

func MaheshvaraToGemini(req *MaheshvaraRequest) ([]byte, error) {
	return CanonicalToGeminiRequest(req)
}

func MaheshvaraToOpenAIResponses(req *MaheshvaraRequest, original *OpenAIResponsesRequest) ([]byte, error) {
	return CanonicalToResponsesRequest(req, original)
}

func OpenAIChatResponseToMaheshvara(resp *OpenAIResponse) (*MaheshvaraResponse, error) {
	return OpenAIChatResponseToCanonical(resp)
}

func AnthropicResponseToMaheshvara(resp *ClaudeResponse) (*MaheshvaraResponse, error) {
	return ClaudeResponseToCanonical(resp)
}

func GeminiResponseToMaheshvara(resp *GeminiResponse) (*MaheshvaraResponse, error) {
	return GeminiResponseToCanonical(resp)
}

func OpenAIResponsesResponseToMaheshvara(resp *OpenAIResponsesResponse) (*MaheshvaraResponse, error) {
	return ResponsesResponseToCanonical(resp)
}

func MaheshvaraToOpenAIChatResponse(resp *MaheshvaraResponse) (*OpenAIResponse, error) {
	return CanonicalToOpenAIChatResponse(resp)
}

func MaheshvaraToAnthropicResponse(resp *MaheshvaraResponse) (*ClaudeResponse, error) {
	return CanonicalToClaudeResponse(resp)
}

func MaheshvaraToGeminiResponse(resp *MaheshvaraResponse) (*GeminiResponse, error) {
	return CanonicalToGeminiResponse(resp)
}

func MaheshvaraToOpenAIResponsesResponse(resp *MaheshvaraResponse) (*OpenAIResponsesResponse, error) {
	return CanonicalToResponsesResponse(resp)
}
