package relay

import (
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// builtinWireAdapter is the single module selector for native and declared
// custom wire shapes. Vendor parsing/rendering stays in the existing domain
// files until the remaining legacy boundaries are removed.
type builtinWireAdapter struct {
	format         FormatType
	name           string
	decodeRequest  func([]byte, protocol.DecodeOptions) (*MaheshvaraRequest, error)
	encodeRequest  func(*MaheshvaraRequest) ([]byte, error)
	decodeResponse func([]byte) (*MaheshvaraResponse, error)
	encodeResponse func(*MaheshvaraResponse) ([]byte, error)
}

var _ protocol.Adapter[MaheshvaraRequest, MaheshvaraResponse] = builtinWireAdapter{}

var builtinWireAdapters = []builtinWireAdapter{
	{format: FormatOpenAIChat, name: "openai-chat",
		decodeRequest: func(body []byte, _ protocol.DecodeOptions) (*MaheshvaraRequest, error) {
			return OpenAIChatToMaheshvara(body)
		},
		encodeRequest:  MaheshvaraToOpenAIChat,
		decodeResponse: decodeWireResponse[OpenAIResponse](OpenAIChatResponseToMaheshvara),
		encodeResponse: encodeWireResponse[OpenAIResponse](MaheshvaraToOpenAIChatResponse)},
	{format: FormatClaude, name: "anthropic",
		decodeRequest: func(body []byte, _ protocol.DecodeOptions) (*MaheshvaraRequest, error) {
			return AnthropicToMaheshvara(body)
		},
		encodeRequest:  MaheshvaraToAnthropic,
		decodeResponse: decodeWireResponse[ClaudeResponse](AnthropicResponseToMaheshvara),
		encodeResponse: encodeWireResponse[ClaudeResponse](MaheshvaraToAnthropicResponse)},
	{format: FormatGemini, name: "gemini",
		decodeRequest: func(body []byte, options protocol.DecodeOptions) (*MaheshvaraRequest, error) {
			return GeminiToMaheshvara(body, options.Model)
		},
		encodeRequest:  MaheshvaraToGemini,
		decodeResponse: decodeWireResponse[GeminiResponse](GeminiResponseToMaheshvara),
		encodeResponse: encodeWireResponse[GeminiResponse](MaheshvaraToGeminiResponse)},
	{format: FormatResponses, name: "responses",
		decodeRequest: func(body []byte, _ protocol.DecodeOptions) (*MaheshvaraRequest, error) {
			request, _, err := OpenAIResponsesToMaheshvara(body)
			return request, err
		},
		encodeRequest:  func(request *MaheshvaraRequest) ([]byte, error) { return MaheshvaraToOpenAIResponses(request, nil) },
		decodeResponse: decodeWireResponse[OpenAIResponsesResponse](OpenAIResponsesResponseToMaheshvara),
		encodeResponse: encodeWireResponse[OpenAIResponsesResponse](MaheshvaraToOpenAIResponsesResponse)},
}

func (adapter builtinWireAdapter) Identity() protocol.Identity {
	return protocol.Identity{Family: string(adapter.format), WireVersion: legacyWireContractVersion, DefinitionID: adapter.name, Revision: "1"}
}

func (adapter builtinWireAdapter) DecodeRequest(body []byte, options protocol.DecodeOptions) (*MaheshvaraRequest, error) {
	return adapter.decodeRequest(body, options)
}
func (adapter builtinWireAdapter) EncodeRequest(request *MaheshvaraRequest) ([]byte, error) {
	return adapter.encodeRequest(request)
}
func (adapter builtinWireAdapter) DecodeResponse(body []byte) (*MaheshvaraResponse, error) {
	value, err := protocol.ParseValue(body)
	if err != nil {
		return nil, err
	}
	fields, err := value.ReadObject()
	if err != nil {
		return nil, err
	}
	if errorValue, hasError := fields["error"]; hasError && !errorValue.IsNull() {
		response := &MaheshvaraResponse{Error: ParseUpstreamError(adapter.format, 502, body)}
		if err := captureWireResponse(response, body, adapter); err != nil {
			return nil, err
		}
		return response, nil
	}
	response, err := adapter.decodeResponse(body)
	if err != nil {
		return nil, err
	}
	if err := captureWireResponse(response, body, adapter); err != nil {
		return nil, err
	}
	return response, nil
}
func (adapter builtinWireAdapter) EncodeResponse(response *MaheshvaraResponse) ([]byte, error) {
	if response != nil && response.nativeSnapshot != nil && response.nativeSnapshot.format == adapter.format {
		return preserveWireResponse(response, adapter)
	}
	if response != nil && response.Error != nil {
		_, body := ProtocolErrorBody(adapter.format, response.Error)
		return body, nil
	}
	return adapter.encodeResponse(response)
}

// EncodeMaheshvaraResponse uses the same encoder for native and custom paths.
func EncodeMaheshvaraResponse(response *MaheshvaraResponse, format FormatType) ([]byte, error) {
	adapter, err := wireAdapterForFormat(format)
	if err != nil {
		return nil, err
	}
	return adapter.EncodeResponse(response)
}

func decodeWireResponse[T any](convert func(*T) (*MaheshvaraResponse, error)) func([]byte) (*MaheshvaraResponse, error) {
	return func(body []byte) (*MaheshvaraResponse, error) {
		var response T
		if err := decodeWireJSON(body, &response); err != nil {
			return nil, err
		}
		return convert(&response)
	}
}

func encodeWireResponse[T any](convert func(*MaheshvaraResponse) (*T, error)) func(*MaheshvaraResponse) ([]byte, error) {
	return func(response *MaheshvaraResponse) ([]byte, error) {
		converted, err := convert(response)
		if err != nil {
			return nil, err
		}
		return json.Marshal(converted)
	}
}

func findWireAdapter(name string) (builtinWireAdapter, error) {
	for _, adapter := range builtinWireAdapters {
		if adapter.name == name {
			return adapter, nil
		}
	}
	return builtinWireAdapter{}, fmt.Errorf("unsupported wire adapter %q", name)
}

func wireAdapterForFormat(format FormatType) (builtinWireAdapter, error) {
	if format == FormatOpenAI || format == "" {
		format = FormatOpenAIChat
	}
	for _, adapter := range builtinWireAdapters {
		if adapter.format == format {
			return adapter, nil
		}
	}
	return builtinWireAdapter{}, fmt.Errorf("unsupported wire format %q", format)
}
