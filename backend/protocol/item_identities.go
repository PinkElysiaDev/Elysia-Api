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

// Parent resolves an existing association without minting an alias for an orphan.
func (index *ItemIdentities) Parent(value Value) (string, error) {
	if value.IsZero() {
		return "", nil
	}
	id, err := readString(value)
	if err != nil || id == "" || index.aliases["item:"+id] == "" {
		return "", streamIssue(InvalidAssociation, "/parentId", "parent must reference an already started item")
	}
	return index.aliases["item:"+id], nil
}

// Resolve returns a stable key and records only unambiguous new aliases.
func (index *ItemIdentities) Resolve(event Event) (string, error) {
	key, aliases, err := resolveItemIdentity(index.aliases, nil, event, index.limit)
	if err != nil {
		return "", err
	}
	for _, alias := range aliases.values[:aliases.count] {
		index.aliases[alias] = key
	}
	return key, nil
}

func readItemAlias(aliases, pending map[string]string, id string) string {
	if pending != nil {
		if key := pending[id]; key != "" {
			return key
		}
	}
	return aliases[id]
}

type itemAliasList struct {
	values [itemIdentityKinds]string
	count  int
}

func resolveItemIdentity(aliases, pending map[string]string, event Event, limit int) (string, itemAliasList, error) {
	var storage [itemIdentityKinds]string
	identities := storage[:0]
	for _, entry := range []struct {
		prefix string
		value  Value
	}{{"item:", event.ItemID}, {"call:", event.CallID}} {
		if !entry.value.IsZero() {
			id, err := readString(entry.value)
			if err != nil || id == "" {
				return "", itemAliasList{}, streamIssue(InvalidAssociation, "/itemId", "item/call identity must be a nonempty string")
			}
			identities = append(identities, entry.prefix+id)
		}
	}
	if event.Index != nil {
		identities = append(identities, "index:"+strconv.Itoa(*event.Index))
	}
	if len(identities) == 0 {
		return "", itemAliasList{}, streamIssue(InvalidAssociation, "/itemId", "item has no identity")
	}
	key := ""
	for _, id := range identities {
		if previous := readItemAlias(aliases, pending, id); previous != "" {
			if key != "" && key != previous {
				return "", itemAliasList{}, streamIssue(InvalidAssociation, "/itemId", "event links two distinct items")
			}
			key = previous
		}
	}
	if key == "" {
		key = identities[0]
	}
	newAliases := 0
	for _, id := range identities {
		if readItemAlias(aliases, pending, id) == "" {
			newAliases++
		}
	}
	if len(aliases)+len(pending)+newAliases > limit {
		return "", itemAliasList{}, streamIssue(LimitExceeded, "/itemId", "too many item aliases")
	}
	var additions itemAliasList
	for _, id := range identities {
		if readItemAlias(aliases, pending, id) == "" {
			additions.values[additions.count] = id
			additions.count++
		}
	}
	return key, additions, nil
}
