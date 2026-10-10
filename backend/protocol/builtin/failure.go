package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

var failureNull, _ = p.EncodeValue(nil)

// Responses uses both flat and nested error events. The event discriminator
// and sequence belong to framing, not to the provider's failure payload.
// Unknown fields remain scoped by captureFrameExtensions/decodeFailure.
func responsesFailurePayload(fields p.Object) (p.Value, error) {
	payload, path := fields, ""
	if nested := fields["error"]; !nested.IsZero() {
		if !nested.IsObject() {
			return p.Value{}, unsupported("/error", "Responses error payload requires an object")
		}
		for _, key := range []string{"message", "code", "param"} {
			if !fields[key].IsZero() {
				return p.Value{}, unsupported("/"+key, "Responses error cannot combine nested and flat failure fields")
			}
		}
		payload, _ = nested.ReadObject()
		path = "/error"
		if name, err := stringValue(payload["type"]); err != nil || name == "" {
			return p.Value{}, unsupported(path+"/type", "Responses nested error requires a nonempty type string")
		}
	}
	if _, err := stringValue(payload["message"]); err != nil {
		return p.Value{}, unsupported(path+"/message", "Responses error requires a message string")
	}
	for _, key := range []string{"code", "param"} {
		if value := payload[key]; !value.IsZero() && !value.IsNull() {
			if _, err := stringValue(value); err != nil {
				return p.Value{}, unsupported(path+"/"+key, "Responses error field requires a string or null")
			}
		}
	}
	if path == "" {
		return object(p.Object{"message": payload["message"], "code": payload["code"], "param": payload["param"]}), nil
	}
	return fields["error"], nil
}

// Failure payloads use message/category/code/param/details. Vendor-specific
// fields remain scoped extensions; a target cannot silently discard them.
func (adapter module) decodeFailure(value p.Value, options p.EvaluationContext) (p.Value, error) {
	if !value.IsObject() {
		message, err := stringValue(value)
		if err != nil {
			return p.Value{}, err
		}
		return object(p.Object{"message": p.StringValue(message)}), nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	result := p.Object{"message": fields["message"]}
	known := []string{"message"}
	var category ErrorClass
	if adapter.name == Gemini {
		status, err := optionalString(fields["status"])
		if err != nil {
			return p.Value{}, err
		}
		category = classFromGeminiStatus(status)
		if category != "" && status == category.geminiStatus() {
			known = append(known, "status")
		}
		var code int
		if !fields["code"].IsZero() && fields["code"].Decode(&code) == nil && code == category.HTTPStatus() {
			known = append(known, "code")
		}
		result["details"] = fields["details"]
		known = append(known, "details")
	} else {
		typeName, err := optionalString(fields["type"])
		if err != nil {
			return p.Value{}, err
		}
		category = classFromOpenAIType(typeName)
		if adapter.name == Anthropic {
			category = classFromAnthropicType(typeName)
		}
		canonicalType, _ := category.openAITypeCode()
		if adapter.name == Anthropic {
			canonicalType = category.anthropicType()
		}
		// OpenAI also uses server_error as the type itself. It denotes the
		// existing server category; treating it as a vendor extension would
		// replace a real provider outage with an unsupported conversion error.
		serverTypeAlias := (adapter.name == Chat || adapter.name == Responses) && typeName == "server_error"
		if category != "" && (typeName == canonicalType || serverTypeAlias) {
			known = append(known, "type")
		}
		if adapter.name == Chat || adapter.name == Responses {
			code := ""
			var err error
			if !fields["code"].IsNull() {
				code, err = optionalString(fields["code"])
			}
			if err != nil {
				return p.Value{}, err
			}
			switch code {
			case "invalid_api_key":
				category = ErrorClassAuthentication
			case "model_not_found":
				category = ErrorClassModelNotFound
			case "server_is_overloaded":
				category = ErrorClassOverloaded
			default:
				result["code"] = fields["code"]
			}
			result["param"] = fields["param"]
			if category == ErrorClassModelNotFound && fields["param"] == p.StringValue("model") {
				delete(result, "param")
			}
			known = append(known, "code", "param")
		}
		// Some compatible providers repeat the canonical category in code.
		// It adds no independent detail. Preserve the original same-wire shape
		// through the native snapshot, while foreign targets use the category.
		if category != "" && typeName == canonicalType && fields["code"] == p.StringValue(typeName) {
			delete(result, "code")
			known = append(known, "code")
		}
		if adapter.name == Anthropic {
			// Enumerated nullable compatibility fields denote no detail. Actual
			// nonempty provider codes/parameters still require an explicit mapping.
			for _, key := range []string{"code", "param"} {
				if fields[key].IsNull() {
					known = append(known, key)
				}
			}
		}
	}
	if category == "" && !options.Values["httpStatus"].IsZero() {
		var status int
		if err := options.Values["httpStatus"].Decode(&status); err != nil {
			return p.Value{}, err
		}
		category = ClassFromStatus(status)
	}
	if category != "" {
		result["category"] = p.StringValue(string(category))
	}
	// Null code/param/details mean no detail. The native snapshot retains their
	// original presence for same-wire replay; foreign errors use target rules.
	for _, key := range []string{"code", "param", "details"} {
		if result[key].IsNull() {
			delete(result, key)
		}
	}
	if unknown := collectUnknown(fields, known); !unknown.IsZero() {
		result[wireExtensionPrefix+adapter.family] = unknown
	}
	return object(result), nil
}

func (adapter module) encodeFailure(value p.Value, options p.EvaluationContext) (p.Value, error) {
	fields, err := value.ReadObject()
	if err != nil {
		return p.Value{}, unsupported("/error", "target errors require a mapped semantic failure object")
	}
	for key := range fields {
		switch key {
		case "message", "category", "code", "param", "details":
		default:
			if !strings.HasPrefix(key, wireExtensionPrefix) {
				return p.Value{}, unsupported("/error/"+key, "target has no declared error field mapping")
			}
		}
	}
	categoryText, err := optionalString(fields["category"])
	if err != nil {
		return p.Value{}, err
	}
	category := ErrorClass(categoryText)
	status := 0
	if !options.Values["httpStatus"].IsZero() {
		if err := options.Values["httpStatus"].Decode(&status); err != nil {
			return p.Value{}, err
		}
	}
	if category == "" {
		category = ErrorClassUpstream
		if status != 0 {
			category = ClassFromStatus(status)
		}
	}
	switch category {
	case ErrorClassInvalidRequest, ErrorClassAuthentication, ErrorClassPermission, ErrorClassModelNotFound, ErrorClassRateLimit, ErrorClassOverloaded, ErrorClassUpstream, ErrorClassServer:
	default:
		return p.Value{}, unsupported("/error/category", "target has no equivalent failure category")
	}
	output := p.Object{"message": fields["message"]}
	if err := adapter.preserveExtensions(output, fields); err != nil {
		return p.Value{}, err
	}
	if adapter.name == Anthropic || adapter.name == Gemini {
		for _, key := range []string{"code", "param"} {
			if !fields[key].IsZero() && !fields[key].IsNull() {
				return p.Value{}, unsupported("/error/"+key, "target has no equivalent provider error field")
			}
		}
	}
	if adapter.name != Gemini && !fields["details"].IsZero() && !fields["details"].IsNull() {
		return p.Value{}, unsupported("/error/details", "target has no structured error details field")
	}
	switch adapter.name {
	case Chat, Responses:
		typeName, code := category.openAITypeCode()
		if output["type"].IsZero() {
			output["type"] = p.StringValue(typeName)
		}
		output["code"], output["param"] = fields["code"], fields["param"]
		if output["code"].IsZero() {
			output["code"] = failureNull
			if code != "" {
				output["code"] = p.StringValue(code)
			}
		}
		if output["param"].IsZero() {
			output["param"] = failureNull
			if category == ErrorClassModelNotFound {
				output["param"] = p.StringValue("model")
			}
		}
	case Anthropic:
		if output["type"].IsZero() {
			output["type"] = p.StringValue(category.anthropicType())
		}
	case Gemini:
		if status == 0 {
			status = category.HTTPStatus()
		}
		if output["code"].IsZero() {
			output["code"], err = p.EncodeValue(status)
			if err != nil {
				return p.Value{}, err
			}
		}
		if output["status"].IsZero() {
			output["status"] = p.StringValue(category.geminiStatus())
		}
		output["details"] = fields["details"]
	default:
		return p.Value{}, fmt.Errorf("unsupported failure encoder")
	}
	return object(output), nil
}

func (stream *streamModule) decodeFailureEvent(value p.Value, options p.EvaluationContext) ([]p.Event, error) {
	failure, err := stream.module.decodeFailure(value, options)
	if err != nil {
		return nil, err
	}
	return []p.Event{{Type: p.OperationFailed, Error: failure}}, nil
}
