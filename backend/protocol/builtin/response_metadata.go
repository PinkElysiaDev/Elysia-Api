package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
	"sort"
)

// extractMetadata removes only recognized fields from a private wire copy.
// JSON and stream extension capture use this exact field/type table.
func (adapter module) extractMetadata(fields p.Object, location, base string) ([]p.ResponseMetadata, error) {
	var items []p.ResponseMetadata
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := fields[key]
		kind := p.MetadataFieldType(adapter.name, location, key)
		if kind == "" || kind == "null-only" && !value.IsNull() {
			continue
		}
		at := base + "/" + key
		if err := p.ValidateMetadataValue(adapter.name, location, key, value, at); err != nil {
			return nil, err
		}
		items = append(items, p.ResponseMetadata{Name: key, Location: location, Codec: adapter.name, SourceCodec: adapter.name, Path: at, Value: value})
		delete(fields, key)
	}
	return items, nil
}

func (adapter module) writeMetadata(fields p.Object, items []p.ResponseMetadata, location string) error {
	for _, item := range items {
		if item.Codec != adapter.name {
			return unsupported(item.Path, "response metadata requires response_metadata projection")
		}
		if item.Location != location {
			continue
		}
		if existing := fields[item.Name]; !existing.IsZero() && string(existing.Bytes()) != string(item.Value.Bytes()) {
			return unsupported(item.Path, "response metadata collides with a mapped field")
		}
		fields[item.Name] = item.Value
	}
	return nil
}

func (adapter module) contentMetadata(nodes []p.Node) []p.ResponseMetadata {
	var result []p.ResponseMetadata
	for _, n := range nodes {
		result = append(result, n.Metadata...)
		result = append(result, adapter.contentMetadata(n.Children)...)
	}
	return result
}

// Metadata in stream extension locations remains associated with the source
// frame. Content metadata is attached by the part decoders, not duplicated here.
func (stream *streamModule) splitFrameMetadata(extra p.Value) (p.Value, []p.ResponseMetadata, error) {
	if extra.IsZero() {
		return extra, nil, nil
	}
	locations, err := extra.ReadObject()
	if err != nil {
		return p.Value{}, nil, err
	}
	var metadata []p.ResponseMetadata
	for path, value := range locations {
		fields, err := value.ReadObject()
		if err != nil {
			return p.Value{}, nil, err
		}
		location := ""
		switch path {
		case "/", "/message", "/response":
			location = "response"
		case "/delta":
			if stream.name == Anthropic {
				location = "response"
			}
		case "/usage", "/usageMetadata", "/message/usage":
			location = "usage"
		}
		var i int
		if _, err := fmt.Sscanf(path, "/choices/%d", &i); err == nil && path == fmt.Sprintf("/choices/%d", i) {
			location = "choice"
		}
		if _, err := fmt.Sscanf(path, "/choices/%d/delta", &i); err == nil && path == fmt.Sprintf("/choices/%d/delta", i) {
			location = "message"
		}
		if _, err := fmt.Sscanf(path, "/candidates/%d", &i); err == nil && path == fmt.Sprintf("/candidates/%d", i) {
			location = "candidate"
		}
		if location != "" {
			base := path
			if base == "/" {
				base = ""
			}
			items, err := stream.module.extractMetadata(fields, location, base)
			if err != nil {
				return p.Value{}, nil, err
			}
			metadata = append(metadata, items...)
		}
		if len(fields) == 0 {
			delete(locations, path)
		} else {
			locations[path] = object(fields)
		}
	}
	if len(locations) == 0 {
		return p.Value{}, metadata, nil
	}
	return object(locations), metadata, nil
}
