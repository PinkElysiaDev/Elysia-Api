package relay

import (
	"fmt"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

func decodeCustomWireResponse(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	adapter, err := findWireAdapter(config.Response.Adapter)
	if err != nil {
		return nil, err
	}
	response, err := adapter.DecodeResponse(body)
	if err != nil {
		return nil, fmt.Errorf("custom protocol %q %s response: %w", config.ID, adapter.name, err)
	}
	if config.Response.UsagePath != "" || (config.Aliases != nil && len(config.Aliases.Usage) > 0) {
		root, err := decodeJSONUseNumber(body)
		if err != nil {
			return nil, err
		}
		path := config.Response.UsagePath
		if path == "" {
			path = wireUsagePath(adapter.format)
		}
		var aliases map[string][]string
		if config.Aliases != nil {
			aliases = config.Aliases.Usage
		}
		response.Usage = customUsageAtWithAliases(root, path, aliases)
	}
	return response, nil
}

func wireUsagePath(format FormatType) string {
	if format == FormatGemini {
		return "usageMetadata"
	}
	return "usage"
}

func (decoder *CustomProtocolStreamDecoder) decodeNativeEvent(wireEvent SSEEvent) ([]MaheshvaraStreamEvent, bool, error) {
	events, err := decoder.native.Decode(wireEvent)
	if err != nil {
		return nil, false, err
	}
	var nativeRoot map[string]any
	var nativeUsage *MaheshvaraUsage
	if decoder.aliases != nil && len(decoder.aliases.Usage) > 0 {
		for index := range events {
			event := &events[index]
			if event.Usage == nil && (event.Response == nil || event.Response.Usage == nil) {
				continue
			}
			root := event.Raw
			path := wireUsagePath(decoder.native.format)
			if decoder.native.format == FormatResponses && root["response"] != nil {
				root = mapValue(root["response"])
			}
			if decoder.native.format == FormatClaude && root["message"] != nil {
				root = mapValue(root["message"])
			}
			usage := customUsageAtWithAliases(root, path, decoder.aliases.Usage)
			if event.Usage != nil {
				event.Usage = usage
			}
			if event.Response != nil {
				event.Response.Usage = usage
			}
			// Same-wire Responses events retain their native payload. Apply the
			// declared counter override there as well as to accounting semantics.
			if decoder.native.format == FormatResponses && usage != nil {
				nativeRoot, nativeUsage = root, usage
			}
		}
	}
	if nativeRoot != nil {
		if err := updateNativeResponsesUsage(nativeRoot, nativeUsage); err != nil {
			return nil, false, err
		}
	}
	for _, event := range events {
		decoder.usage = mergeMaheshvaraStreamUsage(decoder.usage, event.Usage)
	}
	return events, strings.TrimSpace(wireEvent.Data) == customDoneSentinel, nil
}

func updateNativeResponsesUsage(root map[string]any, usage *MaheshvaraUsage) error {
	original, err := protocol.EncodeValue(root["usage"])
	if err != nil {
		return err
	}
	before, err := protocol.EncodeValue(responsesUsageFromMaheshvara(maheshvaraUsageFromRawMap(mapValue(root["usage"]))))
	if err != nil {
		return err
	}
	after, err := protocol.EncodeValue(responsesUsageFromMaheshvara(usage))
	if err != nil {
		return err
	}
	merged, err := overlayKnownUsage(original, before, after)
	if err != nil {
		return err
	}
	var value map[string]any
	if err := merged.Decode(&value); err != nil {
		return err
	}
	root["usage"] = value
	return nil
}
