package relay

import (
	"encoding/json"

	"github.com/elysia-api/backend/protocol"
)

var streamLimits = protocol.DefaultLimits()

func checkStreamSize(items, bytes int) error {
	if items <= streamLimits.StateItems && bytes <= streamLimits.BufferBytes {
		return nil
	}
	return &protocol.ConversionError{Issues: []protocol.ConversionIssue{{
		Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "stream", Path: "buffer",
		Reason:     "stream state exceeds the configured item or buffer limit",
		Suggestion: "reduce retained snapshots or configure a suitable engine limit",
	}}}
}

func (decoder *MaheshvaraStreamDecoder) checkBuffers() error {
	bytes := 0
	for _, tool := range decoder.openAITools {
		bytes += tool.arguments.Len()
	}
	for _, block := range decoder.anthropicBlocks {
		bytes += block.arguments.Len() + len(block.initialInput)
	}
	return checkStreamSize(len(decoder.openAITools)+len(decoder.anthropicBlocks)+len(decoder.seenChoices), bytes)
}

func (decoder *CustomProtocolStreamDecoder) checkBuffers() error {
	bytes := 0
	for _, arguments := range decoder.toolArguments {
		bytes += len(arguments)
	}
	return checkStreamSize(len(decoder.toolMeta)+len(decoder.signatureSent)+len(decoder.frameTools), bytes)
}

func (renderer *MaheshvaraStreamRenderer) checkBuffers() error {
	items, bytes := 0, 0
	switch renderer.format {
	case FormatOpenAIChat:
		items = len(renderer.openAI.tools) + len(renderer.openAI.roleSent)
		for _, tool := range renderer.openAI.tools {
			bytes += tool.arguments.Len()
		}
		for _, signature := range renderer.openAI.pendingGeminiSignatures {
			bytes += len(signature)
		}
	case FormatClaude:
		items = len(renderer.claude.tools)
		for _, tool := range renderer.claude.tools {
			bytes += tool.arguments.Len()
		}
	case FormatGemini:
		items = len(renderer.gemini.tools)
		for _, tool := range renderer.gemini.tools {
			bytes += tool.arguments.Len()
		}
	case FormatResponses:
		items = len(renderer.responses.messages) + len(renderer.responses.reasoning) + len(renderer.responses.tools) + len(renderer.responses.opaqueItems)
		for _, message := range renderer.responses.messages {
			for _, part := range message.parts {
				bytes += part.text.Len()
			}
			encoded, err := json.Marshal(message.extraParts)
			if err != nil {
				return err
			}
			bytes += len(encoded)
		}
		for _, reasoning := range renderer.responses.reasoning {
			bytes += reasoning.text.Len() + reasoning.signature.Len() + len(reasoning.encrypted)
		}
		for _, tool := range renderer.responses.tools {
			bytes += tool.arguments.Len()
		}
		for _, item := range renderer.responses.opaqueItems {
			encoded, err := json.Marshal(item)
			if err != nil {
				return err
			}
			bytes += len(encoded)
		}
	}
	return checkStreamSize(items, bytes)
}
