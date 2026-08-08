package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

func applyOpenAIRequestExtensions(raw map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if value, ok := numberValue(raw["n"]); ok {
		v := int(value)
		req.N = &v
	}
	if value, ok := numberValue(raw["seed"]); ok {
		v := int64(value)
		req.Seed = &v
	}
	req.PresencePenalty = floatPointer(raw["presence_penalty"])
	req.FrequencyPenalty = floatPointer(raw["frequency_penalty"])
	req.RepetitionPenalty = floatPointer(raw["repetition_penalty"])
	req.LogProbs = boolPointer(raw["logprobs"])
	if value, ok := numberValue(raw["top_logprobs"]); ok {
		v := int(value)
		req.TopLogProbs = &v
	}
	req.TypicalP = floatPointer(raw["typical_p"])
	req.MinP = floatPointer(raw["min_p"])
	req.TopA = floatPointer(raw["top_a"])
	if raw["modalities"] != nil {
		req.Modalities = stringSlice(raw["modalities"])
	}
	if raw["audio"] != nil {
		req.Audio = audioConfigFromAny(raw["audio"])
	}
	if raw["prediction"] != nil {
		req.Prediction = raw["prediction"]
	}
	if value := stringValue(raw["service_tier"]); value != "" {
		req.ServiceTier = value
	}
	req.SafetyIdentifier = firstNonEmptyString(req.SafetyIdentifier, stringValue(raw["safety_identifier"]))
	req.Verbosity = firstNonEmptyString(req.Verbosity, stringValue(raw["verbosity"]))
	if value := raw["store"]; value != nil {
		req.Store = boolPointer(value)
	}
	req.RawExtra = rawFields(raw)
}

func applyClaudeRequestExtensions(raw map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if value, ok := numberValue(raw["top_k"]); ok {
		v := int(value)
		req.TopK = &v
	}
	if raw["metadata"] != nil {
		req.Metadata = mapValue(raw["metadata"])
	}
	if raw["tool_choice"] != nil {
		req.ToolChoice = raw["tool_choice"]
	}
	if raw["service_tier"] != nil {
		req.ServiceTier = stringValue(raw["service_tier"])
	}
	if raw["cache_control"] != nil {
		req.CacheControl = raw["cache_control"]
	}
	if raw["output_config"] != nil {
		req.RawExtra = rawFields(raw)
	}
	if req.RawExtra == nil {
		req.RawExtra = rawFields(raw)
	}
}

func applyGeminiRequestExtensions(raw map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	req.Stream = boolValue(raw["stream"])
	if raw["safetySettings"] != nil {
		req.SafetySettings = safetySettingsFromAny(raw["safetySettings"])
	}
	if raw["cachedContent"] != nil {
		req.CacheControl = raw["cachedContent"]
	}
	if cfg, ok := raw["generationConfig"].(map[string]any); ok {
		if value, ok := numberValue(cfg["candidateCount"]); ok {
			v := int(value)
			req.N = &v
		}
		if value, ok := numberValue(cfg["seed"]); ok {
			v := int64(value)
			req.Seed = &v
		}
		req.PresencePenalty = floatPointer(cfg["presencePenalty"])
		req.FrequencyPenalty = floatPointer(cfg["frequencyPenalty"])
		req.LogProbs = boolPointer(cfg["responseLogprobs"])
		if value, ok := numberValue(cfg["logprobs"]); ok {
			v := int(value)
			req.TopLogProbs = &v
		}
		if values := stringSlice(cfg["stopSequences"]); len(values) > 0 {
			req.Stop = values
		}
		if values := stringSlice(cfg["responseModalities"]); len(values) > 0 {
			req.Modalities = values
		}
	}
	req.RawExtra = rawFields(raw)
}

func applyResponsesRequestExtensions(raw map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if value, ok := numberValue(raw["seed"]); ok {
		v := int64(value)
		req.Seed = &v
	}
	if raw["service_tier"] != nil {
		req.ServiceTier = stringValue(raw["service_tier"])
	}
	if value, ok := numberValue(raw["max_tool_calls"]); ok {
		v := int(value)
		req.MaxToolCalls = &v
	}
	if value, ok := numberValue(raw["top_logprobs"]); ok {
		v := int(value)
		req.TopLogProbs = &v
	}
	req.SafetyIdentifier = firstNonEmptyString(req.SafetyIdentifier, stringValue(raw["safety_identifier"]))
	if streamOptions := mapValue(raw["stream_options"]); streamOptions != nil {
		req.StreamOptions = &CanonicalStreamOptions{
			IncludeUsage:       boolValue(streamOptions["include_usage"]),
			IncludeObfuscation: boolPointer(streamOptions["include_obfuscation"]),
			Raw:                streamOptions,
		}
	}
	if raw["prompt_cache_key"] != nil {
		req.PromptCacheKey = stringValue(raw["prompt_cache_key"])
	}
	if raw["prompt_cache_retention"] != nil {
		req.PromptCacheRetention = rawMessage(raw["prompt_cache_retention"])
	}
	req.RawExtra = rawFields(raw)
}

func applyOpenAIRequestExtensionsToBody(out map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if req.N != nil {
		out["n"] = *req.N
	}
	if req.Seed != nil {
		out["seed"] = *req.Seed
	}
	if req.PresencePenalty != nil {
		out["presence_penalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		out["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.RepetitionPenalty != nil {
		out["repetition_penalty"] = *req.RepetitionPenalty
	}
	if req.LogProbs != nil {
		out["logprobs"] = *req.LogProbs
	}
	if req.TopLogProbs != nil {
		out["top_logprobs"] = *req.TopLogProbs
	}
	if req.TypicalP != nil {
		out["typical_p"] = *req.TypicalP
	}
	if req.MinP != nil {
		out["min_p"] = *req.MinP
	}
	if req.TopA != nil {
		out["top_a"] = *req.TopA
	}
	if len(req.Modalities) > 0 {
		out["modalities"] = req.Modalities
	}
	if req.Audio != nil {
		out["audio"] = req.Audio
	}
	if req.Prediction != nil {
		out["prediction"] = req.Prediction
	}
	if req.ServiceTier != "" {
		out["service_tier"] = req.ServiceTier
	}
	if req.SafetyIdentifier != "" {
		out["safety_identifier"] = req.SafetyIdentifier
	}
	if req.Verbosity != "" {
		out["verbosity"] = req.Verbosity
	}
	if req.Store != nil {
		out["store"] = *req.Store
	}
}

func applyClaudeRequestExtensionsToBody(out map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if req.TopK != nil {
		out["top_k"] = *req.TopK
	}
	if req.Metadata != nil {
		out["metadata"] = req.Metadata
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = req.ToolChoice
	}
	if req.ServiceTier != "" {
		out["service_tier"] = req.ServiceTier
	}
	if req.CacheControl != nil {
		out["cache_control"] = req.CacheControl
	}
}

func applyGeminiRequestExtensionsToBody(out map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if len(req.SafetySettings) > 0 {
		settings := make([]map[string]any, 0, len(req.SafetySettings))
		for _, setting := range req.SafetySettings {
			item := map[string]any{}
			if setting.Category != "" {
				item["category"] = setting.Category
			}
			if setting.Threshold != "" {
				item["threshold"] = setting.Threshold
			}
			if setting.Action != "" {
				item["action"] = setting.Action
			}
			if len(item) > 0 {
				settings = append(settings, item)
			}
		}
		if len(settings) > 0 {
			out["safetySettings"] = settings
		}
	}
	if req.CacheControl != nil {
		out["cachedContent"] = req.CacheControl
	}
	cfg, _ := out["generationConfig"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	}
	if req.N != nil {
		cfg["candidateCount"] = *req.N
	}
	if req.Seed != nil {
		cfg["seed"] = *req.Seed
	}
	if req.PresencePenalty != nil {
		cfg["presencePenalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		cfg["frequencyPenalty"] = *req.FrequencyPenalty
	}
	if req.LogProbs != nil {
		cfg["responseLogprobs"] = *req.LogProbs
	}
	if req.TopLogProbs != nil {
		cfg["logprobs"] = *req.TopLogProbs
	}
	if len(req.Modalities) > 0 {
		cfg["responseModalities"] = req.Modalities
	}
	if stopSequences := canonicalStopSequences(req.Stop); len(stopSequences) > 0 {
		cfg["stopSequences"] = stopSequences
	}
	if len(cfg) > 0 {
		out["generationConfig"] = cfg
	}
}

func canonicalStopSequences(value any) []string {
	if value == nil {
		return nil
	}
	if text := stringValue(value); text != "" {
		return []string{text}
	}
	return stringSlice(value)
}

func applyResponsesRequestExtensionsToBody(out map[string]any, req *CanonicalRequest) {
	if req == nil {
		return
	}
	if req.Seed != nil {
		out["seed"] = *req.Seed
	}
	if req.ServiceTier != "" {
		out["service_tier"] = req.ServiceTier
	}
	if req.MaxToolCalls != nil {
		out["max_tool_calls"] = *req.MaxToolCalls
	}
	if req.TopLogProbs != nil {
		out["top_logprobs"] = *req.TopLogProbs
	}
	if req.SafetyIdentifier != "" {
		out["safety_identifier"] = req.SafetyIdentifier
	}
	if req.StreamOptions != nil {
		streamOptions := map[string]any{}
		for key, value := range req.StreamOptions.Raw {
			streamOptions[key] = value
		}
		if req.StreamOptions.IncludeUsage {
			streamOptions["include_usage"] = true
		}
		if req.StreamOptions.IncludeObfuscation != nil {
			streamOptions["include_obfuscation"] = *req.StreamOptions.IncludeObfuscation
		}
		if len(streamOptions) > 0 {
			out["stream_options"] = streamOptions
		}
	}
	if req.PromptCacheKey != "" {
		out["prompt_cache_key"] = req.PromptCacheKey
	}
	if len(req.PromptCacheRetention) > 0 {
		out["prompt_cache_retention"] = jsonRawToAny(req.PromptCacheRetention)
	}
}

func floatPointer(value any) *float64 {
	number, ok := numberValue(value)
	if !ok {
		return nil
	}
	return &number
}

func boolPointer(value any) *bool {
	boolean, ok := value.(bool)
	if !ok {
		return nil
	}
	return &boolean
}

func stringSlice(value any) []string {
	var values []any
	switch typed := value.(type) {
	case []any:
		values = typed
	case []string:
		values = make([]any, len(typed))
		for index, item := range typed {
			values[index] = item
		}
	default:
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text := stringValue(item); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func audioConfigFromAny(value any) *CanonicalAudioConfig {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return &CanonicalAudioConfig{
		Voice:      firstNonEmptyString(stringValue(object["voice"]), stringValue(object["voice_name"])),
		Format:     firstNonEmptyString(stringValue(object["format"]), stringValue(object["audio_format"])),
		Codec:      stringValue(object["codec"]),
		SampleRate: intValue(firstNonNilValue(object["sample_rate"], object["sampleRate"])),
		Channels:   intValue(firstNonNilValue(object["channels"], object["channel_count"])),
	}
}

func safetySettingsFromAny(value any) []CanonicalSafetySetting {
	array, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]map[string]any); typedOK {
			array = make([]any, len(typed))
			for index, item := range typed {
				array[index] = item
			}
		} else {
			return nil
		}
	}
	result := make([]CanonicalSafetySetting, 0, len(array))
	for _, item := range array {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, CanonicalSafetySetting{
			Category:  stringValue(object["category"]),
			Threshold: stringValue(object["threshold"]),
			Action:    stringValue(object["action"]),
		})
	}
	return result
}

func intValue(value any) int {
	number, ok := numberValue(value)
	if !ok {
		return 0
	}
	return int(number)
}

func firstNonNilMap(values ...map[string]any) map[string]any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func rawMessage(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func rawFields(raw map[string]any) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	result := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if encoded, err := json.Marshal(value); err == nil {
			result[key] = encoded
		}
	}
	return result
}

func validateCanonicalRequestForTarget(req *CanonicalRequest, format FormatType) error {
	if req == nil {
		return fmt.Errorf("cannot render %s request from nil Maheshvara request", format)
	}
	if strings.TrimSpace(req.Model) == "" && format != FormatGemini {
		return fmt.Errorf("cannot render %s request without model", format)
	}
	return nil
}

func canonicalToolChoiceToGemini(value any) any {
	if object, ok := value.(map[string]any); ok {
		if _, exists := object["functionCallingConfig"]; exists {
			return object
		}
		choiceType := strings.ToLower(stringValue(object["type"]))
		name := firstNonEmptyString(stringValue(object["name"]), stringValue(object["function_name"]))
		if function, ok := object["function"].(map[string]any); ok {
			name = firstNonEmptyString(name, stringValue(function["name"]))
		}
		return geminiToolConfig(choiceType, name)
	}
	return geminiToolConfig(strings.ToLower(stringValue(value)), "")
}

func geminiToolConfig(choiceType, name string) map[string]any {
	mode := "AUTO"
	switch strings.ToLower(strings.TrimSpace(choiceType)) {
	case "none":
		mode = "NONE"
	case "required", "any", "force", "function":
		mode = "ANY"
	}
	config := map[string]any{"mode": mode}
	if name != "" {
		config["allowedFunctionNames"] = []string{name}
	}
	return map[string]any{"functionCallingConfig": config}
}

func canonicalToolChoiceToClaude(value any) any {
	if object, ok := value.(map[string]any); ok {
		if function, ok := object["function"].(map[string]any); ok && stringValue(function["name"]) != "" {
			return map[string]any{"type": "tool", "name": stringValue(function["name"])}
		}
		if stringValue(object["type"]) == "function" && stringValue(object["name"]) != "" {
			return map[string]any{"type": "tool", "name": stringValue(object["name"])}
		}
		if choiceType := strings.ToLower(strings.TrimSpace(stringValue(object["type"]))); choiceType != "" {
			if choiceType == "required" {
				choiceType = "any"
			}
			return map[string]any{"type": choiceType}
		}
	}
	choice := strings.ToLower(strings.TrimSpace(stringValue(value)))
	if choice == "" {
		return nil
	}
	return map[string]any{"type": choice}
}

func canonicalToolChoiceToOpenAI(value any) any {
	if object, ok := value.(map[string]any); ok {
		if config, ok := object["functionCallingConfig"].(map[string]any); ok {
			mode := strings.ToLower(strings.TrimSpace(stringValue(config["mode"])))
			if mode == "any" {
				mode = "required"
			} else if mode == "none" {
				mode = "none"
			} else {
				mode = "auto"
			}
			if names, ok := config["allowedFunctionNames"].([]any); ok && len(names) > 0 {
				return map[string]any{"type": "function", "function": map[string]any{"name": stringValue(names[0])}}
			}
			if names, ok := config["allowedFunctionNames"].([]string); ok && len(names) > 0 {
				return map[string]any{"type": "function", "function": map[string]any{"name": names[0]}}
			}
			return mode
		}
		if function, ok := object["function"].(map[string]any); ok {
			if stringValue(object["type"]) == "function" {
				return object
			}
			return map[string]any{"type": "function", "function": map[string]any{"name": stringValue(function["name"])}}
		}
		if stringValue(object["type"]) == "tool" && stringValue(object["name"]) != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": stringValue(object["name"])}}
		}
	}
	choice := strings.ToLower(strings.TrimSpace(stringValue(value)))
	if choice == "required" {
		return "required"
	}
	if choice == "none" || choice == "auto" {
		return choice
	}
	return value
}

func claudeDocumentBlockToPart(block map[string]any) CanonicalContentPart {
	part := CanonicalContentPart{Type: CanonicalContentDocument, Raw: block}
	if source, ok := block["source"].(map[string]any); ok {
		part.MediaType = firstNonEmptyString(stringValue(source["media_type"]), stringValue(source["mimeType"]))
		part.MimeType = part.MediaType
		part.FileData = stringValue(source["data"])
		part.FileID = stringValue(source["file_id"])
		part.FileName = stringValue(source["filename"])
		part.URI = firstNonEmptyString(stringValue(source["url"]), stringValue(source["file_uri"]))
	}
	part.FileName = firstNonEmptyString(part.FileName, stringValue(block["name"]), stringValue(block["filename"]))
	return part
}

func claudeMediaBlockToPart(block map[string]any, partType string) CanonicalContentPart {
	part := CanonicalContentPart{Type: partType, Raw: block}
	if source, ok := block["source"].(map[string]any); ok {
		part.MediaType = firstNonEmptyString(stringValue(source["media_type"]), stringValue(source["mimeType"]))
		part.MimeType = part.MediaType
		part.Data = stringValue(source["data"])
		part.URI = firstNonEmptyString(stringValue(source["url"]), stringValue(source["file_uri"]))
	}
	return part
}

func canonicalDocumentToClaudeBlock(part CanonicalContentPart) map[string]any {
	if part.FileData != "" {
		mediaType := firstNonEmptyString(part.MediaType, part.MimeType, "application/octet-stream")
		return map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": mediaType, "data": part.FileData}}
	}
	if part.URI != "" || part.ImageURL != "" {
		return map[string]any{"type": "document", "source": map[string]any{"type": "url", "url": firstNonEmptyString(part.URI, part.ImageURL)}}
	}
	if part.FileID != "" {
		return map[string]any{"type": "document", "source": map[string]any{"type": "file", "file_id": part.FileID}}
	}
	return nil
}

func canonicalMediaToClaudeBlock(part CanonicalContentPart) map[string]any {
	data := firstNonEmptyString(part.Data, part.AudioBase64, part.VideoBase64)
	mediaType := firstNonEmptyString(part.MediaType, part.MimeType)
	if data != "" {
		return map[string]any{"type": part.Type, "source": map[string]any{"type": "base64", "media_type": mediaType, "data": data}}
	}
	uri := firstNonEmptyString(part.URI, part.AudioURL, part.VideoURL, part.ImageURL)
	if uri != "" {
		return map[string]any{"type": part.Type, "source": map[string]any{"type": "url", "url": uri}}
	}
	return nil
}

func canonicalPartToGeminiPart(part CanonicalContentPart) map[string]any {
	mediaType := firstNonEmptyString(part.MediaType, part.MimeType)
	data := firstNonEmptyString(part.Data, part.AudioBase64, part.VideoBase64, part.FileData)
	uri := firstNonEmptyString(part.URI, part.AudioURL, part.VideoURL, part.ImageURL)
	if data != "" {
		if mediaType == "" {
			switch part.Type {
			case CanonicalContentAudio:
				mediaType = "audio/mpeg"
			case CanonicalContentVideo:
				mediaType = "video/mp4"
			default:
				mediaType = "application/octet-stream"
			}
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": mediaType, "data": data}}
	}
	if uri != "" {
		fileData := map[string]any{"fileUri": uri}
		if mediaType != "" {
			fileData["mimeType"] = mediaType
		}
		return map[string]any{"fileData": fileData}
	}
	return nil
}
