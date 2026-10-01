package relay

import "strings"

// DecodeUsageObject parses a provider usage object using the same field and
// presence semantics as wire adapters and declared aliases.
func DecodeUsageObject(object map[string]any, source string) *MaheshvaraUsage {
	usage := customUsageAtWithAliases(map[string]any{"usage": object}, "usage", nil)
	usage.Source = source
	return usage
}

// customUsageAtWithAliases retains field presence while honoring an explicit
// alias list as a replacement for that counter's built-in field paths.
func customUsageAtWithAliases(root any, path string, aliases map[string][]string) *MaheshvaraUsage {
	object, ok := customValueAt(root, path).(map[string]any)
	if !ok {
		return nil
	}
	usage := &MaheshvaraUsage{Source: UsageSourceProviderResponse, Raw: object, present: make(map[string]bool)}
	counters := usageCounters(usage)
	read := func(name string, paths ...string) {
		for _, fieldPath := range paths {
			if value, exists := customLookupPath(object, fieldPath); exists && value != nil {
				if _, isNumber := numberValue(value); isNumber {
					*counters[name] = customIntPath(object, fieldPath)
					usage.present[name] = true
					return
				}
			}
		}
	}
	read("input", customAliasKeys(aliases, "input", usageAliasTables.input...)...)
	read("output", customAliasKeys(aliases, "output", usageAliasTables.output...)...)
	read("total", customAliasKeys(aliases, "total", usageAliasTables.total...)...)
	read("cached", customAliasKeys(aliases, "cached", usageAliasTables.cached...)...)
	read("reasoning", customAliasKeys(aliases, "reasoning", "reasoning_tokens", "reasoningTokens", "thoughtsTokenCount", "completion_tokens_details.reasoning_tokens", "output_tokens_details.reasoning_tokens")...)
	read("cache_creation", customAliasKeys(aliases, "cache_creation", usageAliasTables.cacheCre...)...)
	// Compatible providers sometimes use a root zero placeholder with a real
	// reading in details. Explicit aliases always retain first-present semantics.
	if len(aliases["cached"]) == 0 && usage.CachedInputTokens == 0 {
		for _, fieldPath := range []string{"prompt_tokens_details.cached_tokens", "input_tokens_details.cached_tokens", "prompt_tokens_details.cache_read_tokens", "input_tokens_details.cache_read_tokens"} {
			if customIntPath(object, fieldPath) > usage.CachedInputTokens || !usage.present["cached"] {
				read("cached", fieldPath)
			}
		}
		if usage.CachedInputTokens == 0 {
			read("cached", customAliasKeys(aliases, "cache_read", usageAliasTables.cacheRd...)...)
		}
	}
	read("cache_5m", "cache_creation.ephemeral_5m_input_tokens")
	read("cache_1h", "cache_creation.ephemeral_1h_input_tokens")
	if len(aliases["cache_creation"]) == 0 && !usage.present["cache_creation"] {
		if usage.present["cache_5m"] || usage.present["cache_1h"] {
			usage.CacheCreationInputTokens = usage.CacheCreation5mTokens + usage.CacheCreation1hTokens
			usage.present["cache_creation"] = true
			usage.cacheCreationInferred = true
		} else {
			read("cache_creation", "prompt_tokens_details.cached_creation_tokens", "input_tokens_details.cached_creation_tokens")
		}
	}
	for name, paths := range map[string][]string{
		"tool_use":     {"toolUsePromptTokenCount", "tool_use_tokens"},
		"tool_prompt":  {"tool_prompt_tokens"},
		"accepted":     {"completion_tokens_details.accepted_prediction_tokens"},
		"rejected":     {"completion_tokens_details.rejected_prediction_tokens"},
		"text_input":   {"prompt_tokens_details.text_tokens", "input_tokens_details.text_tokens"},
		"text_output":  {"completion_tokens_details.text_tokens", "output_tokens_details.text_tokens"},
		"image_input":  {"prompt_tokens_details.image_tokens", "input_tokens_details.image_tokens"},
		"image_output": {"completion_tokens_details.image_tokens", "output_tokens_details.image_tokens"},
		"audio_input":  {"prompt_tokens_details.audio_tokens", "input_tokens_details.audio_tokens"},
		"audio_output": {"completion_tokens_details.audio_tokens", "output_tokens_details.audio_tokens"},
		"web_search":   {"server_tool_use.web_search_requests"},
	} {
		read(name, paths...)
	}
	for _, entry := range []struct{ path, suffix string }{{"promptTokensDetails", "input"}, {"toolUsePromptTokensDetails", "input"}, {"candidatesTokensDetails", "output"}} {
		for _, value := range customArrayAt(object, entry.path) {
			detail := mapValue(value)
			name := strings.ToLower(stringValue(detail["modality"])) + "_" + entry.suffix
			if counter, exists := counters[name]; exists {
				*counter += customIntPath(detail, "tokenCount")
				usage.present[name] = true
			}
		}
	}
	if len(aliases["input"]) == 0 {
		_, hasRead := object["cache_read_input_tokens"]
		_, hasCreation := object["cache_creation_input_tokens"]
		_, hasTiers := object["cache_creation"]
		usage.cacheInputExclusive = hasRead || hasCreation || hasTiers
		if value, exists := object["input_tokens"]; exists && value != nil {
			input := customIntPath(object, "input_tokens")
			usage.rawInputTokens = &input
			if usage.cacheInputExclusive {
				usage.InputTokens += usage.CachedInputTokens + usage.CacheCreationInputTokens
			}
		}
		_, hasPrompt := object["promptTokenCount"]
		_, hasToolPrompt := object["toolUsePromptTokenCount"]
		usage.toolInputExclusive = hasPrompt || hasToolPrompt
		if hasPrompt {
			input := customIntPath(object, "promptTokenCount")
			usage.rawInputTokens = &input
			usage.InputTokens += usage.ToolUseTokens
		}
	}
	if len(aliases["output"]) == 0 {
		_, hasOutput := object["candidatesTokenCount"]
		_, hasReasoning := object["thoughtsTokenCount"]
		usage.reasoningOutputExclusive = hasOutput || hasReasoning
		if hasOutput {
			output := customIntPath(object, "candidatesTokenCount")
			usage.rawOutputTokens = &output
			usage.OutputTokens += usage.ReasoningTokens
		}
	}
	usage.TotalTokensInferred = !usage.present["total"]
	if usage.TotalTokensInferred && (usage.present["input"] || usage.present["output"]) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		usage.present["total"] = true
	}
	return usage
}
