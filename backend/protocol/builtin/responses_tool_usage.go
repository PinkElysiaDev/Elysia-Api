package builtin

import (
	"maps"
	"slices"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

// Consume only declared leaf counters. Unrecognized nested extensions remain
// under their original wire key and retain the normal cross-protocol guard.
func decodeResponsesToolUsage(fields p.Object, usage *p.Usage) (*p.Usage, error) {
	raw := fields["tool_usage"]
	if raw.IsZero() || p.EmptyResponsesToolUsage(raw) {
		return usage, nil
	}
	mapping := p.ResponsesToolUsageFields()
	var visit func(p.Value, string, string) (p.Value, error)
	visit = func(value p.Value, prefix, at string) (p.Value, error) {
		objectFields, err := value.ReadObject()
		if err != nil {
			return p.Value{}, toolUsageInputError(at, "tool usage must be an object")
		}
		for _, key := range slices.Sorted(maps.Keys(objectFields)) {
			path := prefix + key
			if semantic, ok := mapping[path]; ok {
				count, err := counter(objectFields[key])
				if err != nil {
					return p.Value{}, toolUsageInputError(at+"/"+key, "tool usage counter must be a nonnegative int64")
				}
				if usage == nil {
					usage = &p.Usage{}
				}
				if usage.Details == nil {
					usage.Details = map[string]p.Counter{}
				}
				usage.Details[semantic] = *count
				delete(objectFields, key)
				continue
			}
			for leaf := range mapping {
				if strings.HasPrefix(leaf, path+".") {
					if child, e := objectFields[key].ReadObject(); e == nil && len(child) == 0 {
						break
					}
					residual, e := visit(objectFields[key], path+".", at+"/"+key)
					if e != nil {
						return p.Value{}, e
					}
					if residual.IsZero() {
						delete(objectFields, key)
					} else {
						objectFields[key] = residual
					}
					break
				}
			}
		}
		if len(objectFields) == 0 {
			return p.Value{}, nil
		}
		return object(objectFields), nil
	}
	remaining, err := visit(raw, "", "/tool_usage")
	if err != nil {
		return nil, err
	}
	if remaining.IsZero() {
		delete(fields, "tool_usage")
	} else {
		fields["tool_usage"] = remaining
	}
	if err := p.ValidateUsageArithmetic(usage); err != nil {
		return nil, err
	}
	return usage, nil
}

func toolUsageInputError(path, reason string) error {
	return p.IssuesError([]p.ConversionIssue{{Code: p.InvalidInput, Severity: p.SeverityError, Path: path, Reason: reason}})
}

func encodeResponsesToolUsage(usage *p.Usage) p.Value {
	if usage == nil {
		return p.Value{}
	}
	root := p.Object{}
	var put func(p.Object, []string, p.Value)
	put = func(fields p.Object, path []string, value p.Value) {
		if len(path) == 1 {
			fields[path[0]] = value
			return
		}
		child := p.Object{}
		if !fields[path[0]].IsZero() {
			child, _ = fields[path[0]].ReadObject()
		}
		put(child, path[1:], value)
		fields[path[0]] = object(child)
	}
	for path, semantic := range p.ResponsesToolUsageFields() {
		if count, ok := usage.Details[semantic]; ok {
			put(root, strings.Split(path, "."), counterValue(&count))
		}
	}
	if len(root) == 0 {
		return p.Value{}
	}
	return object(root)
}
