package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	p "github.com/elysia-api/backend/protocol"
)

// PriorDefinitionFingerprints records unmodified shipped definitions. IDs alone
// never authorize replacement of an operator's edited protocol or draft.
var priorDefinitionFingerprints = map[string][]string{
	"anthropic-api":        {"32a99fcf8b3dec80f8f3b5dd2fa9cf4634ca5be5fe21325e1cb36b7a283352d9"},
	"chat-completions-api": {"06c6538deb1bf76a77765b521c0a830a718daa798fb4f9d3be64d08663334fb0"},
	"gemini-api":           {"a229ccb416d1bc144a09eca9030c969595bbf059f5db3ca671480a293ced47e0"},
	"responses-api":        {"b1bcaa9a8d92999bfb51284a328a77b2a8ac0929dd77c77a3daee8fef0833e62"},
}

// DefinitionFingerprint ignores object order and whitespace, while retaining
// array order, explicit nulls and number spellings. It includes unknown keys.
func DefinitionFingerprint(value p.Value) (string, error) {
	var decoded any
	if err := value.Decode(&decoded); err != nil {
		return "", err
	}
	canonical, err := p.EncodeValue(decoded)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

// IsPreviousDefinition only matches cataloged, byte-independent content.
func IsPreviousDefinition(id string, definition p.Value) bool {
	fingerprint, err := DefinitionFingerprint(definition)
	return err == nil && slices.Contains(priorDefinitionFingerprints[id], fingerprint)
}
