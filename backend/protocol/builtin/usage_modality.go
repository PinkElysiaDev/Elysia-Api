package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

var geminiModalityBuckets = []struct{ field, prefix string }{
	{"promptTokensDetails", "input"},
	{"candidatesTokensDetails", "output"},
	{"cacheTokensDetails", "cache"},
	{"toolUsePromptTokensDetails", "tool_input"},
}

// An unknown modality or property keeps the entire breakdown native.
func decodeModalityCounts(value p.Value, prefix, path string) (map[string]p.Counter, bool, error) {
	entries, err := readArray(value)
	if err != nil {
		return nil, false, fmt.Errorf("%s: modality usage must be an array", path)
	}
	counts := map[string]p.Counter{}
	known := true
	seen := map[string]bool{}
	for i, entry := range entries {
		at := fmt.Sprintf("%s/%d", path, i)
		fields, err := entry.ReadObject()
		if err != nil {
			return nil, false, fmt.Errorf("%s: modality usage entry must be an object", at)
		}
		var modality string
		if fields["modality"].IsNull() || fields["modality"].Decode(&modality) != nil || modality == "" {
			return nil, false, fmt.Errorf("%s/modality: expected a nonempty string", at)
		}
		count, err := counter(fields["tokenCount"])
		if err != nil || count == nil {
			return nil, false, fmt.Errorf("%s/tokenCount: expected a nonnegative int64", at)
		}
		if seen[modality] {
			return nil, false, fmt.Errorf("%s/modality: duplicate modality", at)
		}
		seen[modality] = true
		name := prefix + "." + strings.ToLower(modality) + "_tokens"
		_, recognized := p.ModalityUsageDetail(name)
		if recognized != modality || !collectUnknown(fields, []string{"modality", "tokenCount"}).IsZero() {
			known = false
		}
		counts[name] = *count
	}
	return counts, known, nil
}

func encodeModalityCounts(usage *p.Usage, prefix string) p.Value {
	var entries []p.Value
	for _, modality := range []string{"TEXT", "IMAGE", "AUDIO", "VIDEO", "DOCUMENT"} {
		if count, ok := usage.Details[prefix+"."+strings.ToLower(modality)+"_tokens"]; ok {
			entries = append(entries, object(p.Object{"modality": p.StringValue(modality), "tokenCount": counterValue(&count)}))
		}
	}
	if len(entries) == 0 {
		return p.Value{}
	}
	return array(entries)
}
