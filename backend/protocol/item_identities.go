package protocol

import "strconv"

const itemIdentityKinds = 3

// ItemIdentities associates late item/call IDs with their original stream index.
// Each response owns one instance; associations never merge two existing items.
type ItemIdentities struct {
	aliases map[string]string
	limit   int
}

// NewItemIdentities bounds aliases independently of retained item payloads.
func NewItemIdentities(maxItems int) *ItemIdentities {
	return &ItemIdentities{aliases: map[string]string{}, limit: maxItems * itemIdentityKinds}
}

// Resolve returns a stable key and records only unambiguous new aliases.
func (index *ItemIdentities) Resolve(event Event) (string, error) {
	var identities []string
	for _, entry := range []struct {
		prefix string
		value  Value
	}{{"item:", event.ItemID}, {"call:", event.CallID}} {
		if !entry.value.IsZero() {
			id, err := readString(entry.value)
			if err != nil || id == "" {
				return "", streamIssue(InvalidAssociation, "/itemId", "item/call identity must be a nonempty string")
			}
			identities = append(identities, entry.prefix+id)
		}
	}
	if event.Index != nil {
		identities = append(identities, "index:"+strconv.Itoa(*event.Index))
	}
	if len(identities) == 0 {
		return "", streamIssue(InvalidAssociation, "/itemId", "item has no identity")
	}
	key := ""
	for _, id := range identities {
		if previous := index.aliases[id]; previous != "" {
			if key != "" && key != previous {
				return "", streamIssue(InvalidAssociation, "/itemId", "event links two distinct items")
			}
			key = previous
		}
	}
	if key == "" {
		key = identities[0]
	}
	newAliases := 0
	for _, id := range identities {
		if index.aliases[id] == "" {
			newAliases++
		}
	}
	if len(index.aliases)+newAliases > index.limit {
		return "", streamIssue(LimitExceeded, "/itemId", "too many item aliases")
	}
	for _, id := range identities {
		index.aliases[id] = key
	}
	return key, nil
}
