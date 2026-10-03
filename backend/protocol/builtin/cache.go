package builtin

import p "github.com/elysia-api/backend/protocol"

func decodeCache(fields p.Object, location string) ([]p.CacheIntent, error) {
	value := fields["cache_control"]
	if value.IsZero() {
		return nil, nil
	}
	intent := p.CacheIntent{Kind: "breakpoint", Location: location, Value: value}
	if !value.IsNull() {
		policy, err := value.ReadObject()
		if err != nil {
			return nil, err
		}
		intent.TTL = policy["ttl"]
		delete(policy, "ttl")
		intent.Value = object(policy)
	}
	return []p.CacheIntent{intent}, nil
}

func encodeCache(fields p.Object, intents []p.CacheIntent) error {
	if len(intents) > 1 {
		return unsupported("/cache", "one wire cache_control cannot express multiple policies")
	}
	for _, intent := range intents {
		if intent.Kind != "breakpoint" {
			return unsupported("/cache", "this node accepts only cache breakpoints")
		}
		value := intent.Value
		if !value.IsNull() {
			policy, err := value.ReadObject()
			if err != nil {
				return err
			}
			// TTL has one semantic owner; deleting it must not revive a native value.
			policy["ttl"] = intent.TTL
			value = object(policy)
		}
		fields["cache_control"] = value
	}
	return nil
}
