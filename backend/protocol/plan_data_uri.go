package protocol

import "strings"

const (
	dataURIPrefix       = "data:"
	dataURIBase64Suffix = ";base64"
)

func splitDataURI(source JSONOutput) (JSONOutput, error) {
	uri, err := readPlannedString(source)
	if err != nil {
		return JSONOutput{}, err
	}
	content, hasPrefix := strings.CutPrefix(uri, dataURIPrefix)
	if !hasPrefix {
		return JSONObject(map[string]JSONOutput{"kind": JSONLeaf(StringValue("other"))}), nil
	}
	metadata, payload, hasSeparator := strings.Cut(content, ",")
	if !hasSeparator || !strings.HasSuffix(metadata, dataURIBase64Suffix) {
		return JSONObject(map[string]JSONOutput{"kind": JSONLeaf(StringValue("data"))}), nil
	}
	// Owned JSON strings let a retained MIME field release the original URI.
	return JSONObject(map[string]JSONOutput{
		"kind": JSONLeaf(StringValue("base64")),
		"mime": JSONLeaf(StringValue(strings.TrimSuffix(metadata, dataURIBase64Suffix))),
		"data": JSONLeaf(StringValue(payload)),
	}), nil
}
