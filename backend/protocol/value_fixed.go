package protocol

// A closed set of semantic constants has compile-time JSON spellings. This is
// not a dynamic interning cache: arbitrary model text is never retained globally.
// Authored literal outputs already retain their immutable Value at compilation.
func fixedStringValue(text string) (Value, bool) {
	switch text {
	case "":
		return Value{raw: `""`}, true
	case "message":
		return Value{raw: `"message"`}, true
	case "text":
		return Value{raw: `"text"`}, true
	case "image":
		return Value{raw: `"image"`}, true
	case "audio":
		return Value{raw: `"audio"`}, true
	case "video":
		return Value{raw: `"video"`}, true
	case "document":
		return Value{raw: `"document"`}, true
	case "reasoning":
		return Value{raw: `"reasoning"`}, true
	case "refusal":
		return Value{raw: `"refusal"`}, true
	case "tool_call":
		return Value{raw: `"tool_call"`}, true
	case "tool_result":
		return Value{raw: `"tool_result"`}, true
	case "server_tool":
		return Value{raw: `"server_tool"`}, true
	case "opaque":
		return Value{raw: `"opaque"`}, true
	case "summary":
		return Value{raw: `"summary"`}, true
	case "structured":
		return Value{raw: `"structured"`}, true
	case "json":
		return Value{raw: `"json"`}, true
	case "function":
		return Value{raw: `"function"`}, true
	case "free_text":
		return Value{raw: `"free_text"`}, true
	case "server":
		return Value{raw: `"server"`}, true
	case "user":
		return Value{raw: `"user"`}, true
	case "assistant":
		return Value{raw: `"assistant"`}, true
	case "system":
		return Value{raw: `"system"`}, true
	case "developer":
		return Value{raw: `"developer"`}, true
	case "tool":
		return Value{raw: `"tool"`}, true
	case "none":
		return Value{raw: `"none"`}, true
	case "auto":
		return Value{raw: `"auto"`}, true
	case "required":
		return Value{raw: `"required"`}, true
	case "enabled":
		return Value{raw: `"enabled"`}, true
	case "disabled":
		return Value{raw: `"disabled"`}, true
	case "between_tools":
		return Value{raw: `"between_tools"`}, true
	case "all":
		return Value{raw: `"all"`}, true
	case "minimal":
		return Value{raw: `"minimal"`}, true
	case "low":
		return Value{raw: `"low"`}, true
	case "medium":
		return Value{raw: `"medium"`}, true
	case "high":
		return Value{raw: `"high"`}, true
	case "xhigh":
		return Value{raw: `"xhigh"`}, true
	case "max":
		return Value{raw: `"max"`}, true
	case "generation":
		return Value{raw: `"generation"`}, true
	case "explicit":
		return Value{raw: `"explicit"`}, true
	case "implicit":
		return Value{raw: `"implicit"`}, true
	case "breakpoint":
		return Value{raw: `"breakpoint"`}, true
	case "key":
		return Value{raw: `"key"`}, true
	case "retention":
		return Value{raw: `"retention"`}, true
	case "resource":
		return Value{raw: `"resource"`}, true
	case "mode":
		return Value{raw: `"mode"`}, true
	case "options.ttl":
		return Value{raw: `"options.ttl"`}, true
	case "prewarm":
		return Value{raw: `"prewarm"`}, true
	case "content":
		return Value{raw: `"content"`}, true
	case "response":
		return Value{raw: `"response"`}, true
	case "usage":
		return Value{raw: `"usage"`}, true
	case "visible-text":
		return Value{raw: `"visible-text"`}, true
	case "state":
		return Value{raw: `"state"`}, true
	case "information":
		return Value{raw: `"information"`}, true
	case "true":
		return Value{raw: `"true"`}, true
	case "false":
		return Value{raw: `"false"`}, true
	case "automatic":
		return Value{raw: `"automatic"`}, true
	default:
		return Value{}, false
	}
}
