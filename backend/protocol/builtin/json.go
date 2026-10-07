package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

// object constructs only present fields. Its typed values have already passed
// JSON parsing, so marshaling this closed container cannot fail.
func object(fields p.Object) p.Value {
	for key, value := range fields {
		if value.IsZero() {
			delete(fields, key)
		}
	}
	value, _ := p.EncodeValue(fields)
	return value
}

func array(items []p.Value) p.Value {
	if items == nil {
		items = []p.Value{}
	}
	value, _ := p.EncodeValue(items)
	return value
}

func stringValue(value p.Value) (string, error) {
	if value.IsNull() {
		return "", fmt.Errorf("expected string, got null")
	}
	var result string
	err := value.Decode(&result)
	return result, err
}

func optionalString(value p.Value) (string, error) {
	if value.IsZero() {
		return "", nil
	}
	return stringValue(value)
}

func readArray(value p.Value) ([]p.Value, error) {
	if value.IsZero() {
		return nil, nil
	}
	if value.IsNull() {
		return nil, fmt.Errorf("expected array, got null")
	}
	var result []p.Value
	err := value.Decode(&result)
	return result, err
}

func copyFields(fields p.Object) p.Object {
	result := p.Object{}
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func readJSONArguments(value p.Value) (p.Value, error) {
	text, err := stringValue(value)
	if err != nil {
		return p.Value{}, err
	}
	return p.ParseValue([]byte(text))
}

func encodeJSONArguments(value p.Value) p.Value {
	// Wire arguments are a JSON string. Compact valid JSON explicitly instead
	// of relying on an incidental semantic serialization between adapters.
	encoded, _ := p.EncodeValue(value)
	return p.StringValue(string(encoded.Bytes()))
}

func collectUnknown(fields p.Object, known []string) p.Value {
	extra := copyFields(fields)
	for _, name := range known {
		delete(extra, name)
	}
	if len(extra) == 0 {
		return p.Value{}
	}
	return object(extra)
}

// wireExtensionPrefix 标记「源协议原生、无声明式映射」的字段证据；后缀为族名。
const wireExtensionPrefix = "wire:"

func (adapter module) preserveExtensions(fields p.Object, extensions p.Object) error {
	for key, value := range extensions {
		if !strings.HasPrefix(key, wireExtensionPrefix) {
			continue
		}
		if strings.TrimPrefix(key, wireExtensionPrefix) != adapter.family {
			return unsupported("/"+key, "protocol-specific fields have no declared cross-protocol mapping")
		}
		values, err := value.ReadObject()
		if err != nil {
			return err
		}
		for name, extra := range values {
			merged, err := mergeExtensionValue(fields[name], extra, "/"+name)
			if err != nil {
				return err
			}
			fields[name] = merged
		}
	}
	return nil
}

func mergeExtensionValue(mapped, extra p.Value, path string) (p.Value, error) {
	if mapped.IsZero() {
		return extra, nil
	}
	if !mapped.IsObject() || !extra.IsObject() {
		return p.Value{}, unsupported(path, "native extension collides with a mapped field")
	}
	fields, _ := mapped.ReadObject()
	unknown, _ := extra.ReadObject()
	for key, value := range unknown {
		merged, err := mergeExtensionValue(fields[key], value, path+"/"+key)
		if err != nil {
			return p.Value{}, err
		}
		fields[key] = merged
	}
	return object(fields), nil
}

func (adapter module) nestedExtensions(fields p.Object, known []string, nested map[string][]string) (p.Object, error) {
	extra := copyFields(fields)
	for _, key := range known {
		delete(extra, key)
	}
	for key, keys := range nested {
		if fields[key].IsZero() {
			continue
		}
		values, err := fields[key].ReadObject()
		if err != nil {
			return nil, err
		}
		if remaining := collectUnknown(values, keys); !remaining.IsZero() {
			extra[key] = remaining
		}
	}
	return adapter.extensions(extra, nil), nil
}

func (adapter module) extensions(fields p.Object, known []string) p.Object {
	extra := collectUnknown(fields, known)
	if extra.IsZero() {
		return nil
	}
	return p.Object{wireExtensionPrefix + adapter.family: extra}
}
