package builtin

import p "github.com/elysia-api/backend/protocol"

var inputUsageDetails = []string{"cached_tokens", "text_tokens", "image_tokens", "audio_tokens"}
var outputUsageDetails = []string{"reasoning_tokens", "text_tokens", "image_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"}
var creationUsageDetails = []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"}

func (adapter module) usageExtensions(value p.Value) (p.Value, error) {
	if value.IsZero() || value.IsNull() {
		return p.Value{}, nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	known := []string{"input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"}
	nested := map[string][]string{}
	switch adapter.name {
	case Chat:
		known = []string{"prompt_tokens", "completion_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "prompt_tokens_details", "completion_tokens_details"}
		nested = map[string][]string{"prompt_tokens_details": inputUsageDetails, "completion_tokens_details": outputUsageDetails}
	case Responses:
		known = append(known, "input_tokens_details", "output_tokens_details")
		nested = map[string][]string{"input_tokens_details": inputUsageDetails, "output_tokens_details": outputUsageDetails}
	case Anthropic:
		known = append(known, "cache_creation")
		nested = map[string][]string{"cache_creation": creationUsageDetails}
	case Gemini:
		known = []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount", "thoughtsTokenCount", "toolUsePromptTokenCount", "cache_creation_input_tokens"}
	}
	extra := copyFields(fields)
	for _, key := range known {
		delete(extra, key)
	}
	for key, keys := range nested {
		if fields[key].IsZero() {
			continue
		}
		object, err := fields[key].ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		if remaining := collectUnknown(object, keys); !remaining.IsZero() {
			extra[key] = remaining
		}
	}
	if len(extra) == 0 {
		return p.Value{}, nil
	}
	return object(extra), nil
}

func mergeUsageExtensions(known, extra p.Value) (p.Value, error) {
	if extra.IsZero() {
		return known, nil
	}
	fields := p.Object{}
	var err error
	if !known.IsZero() {
		fields, err = known.ReadObject()
		if err != nil {
			return p.Value{}, err
		}
	}
	unknown, err := extra.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	for key, value := range unknown {
		if existing := fields[key]; !existing.IsZero() {
			if !existing.IsObject() || !value.IsObject() {
				return p.Value{}, unsupported("/usage/"+key, "usage extension collides with a mapped counter")
			}
			value, err = mergeUsageExtensions(existing, value)
			if err != nil {
				return p.Value{}, err
			}
		}
		fields[key] = value
	}
	return object(fields), nil
}
