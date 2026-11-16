package protocol

import "fmt"

// ResponseMetadata is a recognized wire field, attached to its owning response
// or node. Path and SourceCodec are diagnostic provenance, not target routing.
// Unknown fields remain in Native/Unmapped and retain their existing guards.
type ResponseMetadata struct {
	// Meaning identifies the core semantics declared by the owning source mapping.
	// Value retains the source representation for its own encoder. Shared scalar
	// meanings and token probabilities already use their canonical Value shape.
	Meaning string `json:"meaning,omitempty"`
	// References contains canonical URL references, independent of wire citation
	// wrappers: [{"url":...,"title":...,"start":...,"end":...}]. Other meanings
	// leave this absent; opaque resource citations never acquire URL semantics.
	References  Value  `json:"references,omitzero"`
	Name        string `json:"name"`
	Location    string `json:"location"`
	Codec       string `json:"codec"`
	SourceCodec string `json:"sourceCodec"`
	Path        string `json:"path"`
	Value       Value  `json:"value"`
	// Update is append only for an item delta; collected metadata is a snapshot.
	Update string `json:"update,omitempty"`
}

func validateCoreMetadataValue(field ResponseMetadata, path string) error {
	if field.Meaning == "url-references" {
		return validateMetadataReferences(field.References, path+"/references")
	}
	if !field.References.IsZero() {
		return streamIssue(InvalidInput, path+"/references", "canonical URL references require their declared metadata meaning")
	}
	if field.Meaning == "token-probabilities" {
		return validateCoreLogprobs(field)
	}
	switch field.Meaning {
	case metadataImageCompression, metadataInputTransformations:
		return validateResponseObservation(field)
	case "service-tier":
		if field.Value.IsNull() {
			return nil
		}
		fallthrough
	case "visible-text", "padding":
		var value string
		if field.Value.IsZero() || field.Value.IsNull() || field.Value.Decode(&value) != nil {
			return streamIssue(InvalidInput, field.Path, "core metadata meaning requires a string value")
		}
	case "buffering":
		var value bool
		if field.Value.IsZero() || field.Value.IsNull() || field.Value.Decode(&value) != nil {
			return streamIssue(InvalidInput, field.Path, "buffering metadata requires a boolean value")
		}
	}
	return nil
}

func validateMetadataReferences(value Value, path string) error {
	var references []Value
	if value.IsZero() || value.IsNull() || value.Decode(&references) != nil {
		return streamIssue(InvalidInput, path, "canonical URL references require an array")
	}
	for index, reference := range references {
		location := fmt.Sprintf("%s/%d", path, index)
		fields, err := reference.ReadObject()
		if err != nil || len(fields) != 4 {
			return streamIssue(InvalidInput, location, "URL reference requires url, title, start and end")
		}
		for _, name := range []string{"url", "title"} {
			var text string
			if fields[name].IsZero() || fields[name].IsNull() || fields[name].Decode(&text) != nil {
				return streamIssue(InvalidInput, location+"/"+name, "URL reference field requires a string")
			}
		}
		var start, end int64
		if fields["start"].IsZero() || fields["start"].IsNull() || fields["start"].Decode(&start) != nil || start < 0 {
			return streamIssue(InvalidInput, location+"/start", "URL reference start requires a nonnegative integer")
		}
		if fields["end"].IsZero() || fields["end"].IsNull() || fields["end"].Decode(&end) != nil || end < start {
			return streamIssue(InvalidInput, location+"/end", "URL reference end requires an integer at or after its start")
		}
	}
	return nil
}

func validateCoreLogprobs(field ResponseMetadata) error {
	if field.Location != "content" {
		return streamIssue(InvalidInput, field.Path, "token probabilities require a content owner")
	}
	return validateLogprobTokens(field.Value, field.Path)
}

func validateLogprobTokens(value Value, at string) error {
	var entries []Value
	if value.IsNull() || value.Decode(&entries) != nil {
		return streamIssue(InvalidInput, at, "logprobs must be an array")
	}
	var token func(Value, string, bool) error
	token = func(value Value, at string, top bool) error {
		fields, err := value.ReadObject()
		if err != nil {
			return streamIssue(InvalidInput, at, "logprob token must be an object")
		}
		for k := range fields {
			if k != "token" && k != "logprob" && k != "bytes" && !(k == "top_logprobs" && !top) {
				return streamIssue(UnsupportedCapability, at+"/"+k, "unknown token probability field")
			}
		}
		var text string
		var prob float64
		if fields["token"].IsNull() || fields["token"].Decode(&text) != nil {
			return streamIssue(InvalidInput, at+"/token", "token must be a string")
		}
		if fields["logprob"].IsNull() || fields["logprob"].Decode(&prob) != nil {
			return streamIssue(InvalidInput, at+"/logprob", "logprob must be a number")
		}
		if b := fields["bytes"]; !b.IsZero() && !b.IsNull() {
			var bytes []int
			if b.Decode(&bytes) != nil {
				return streamIssue(InvalidInput, at+"/bytes", "token bytes must be an integer array")
			}
			for _, n := range bytes {
				if n < 0 || n > 255 {
					return streamIssue(InvalidInput, at+"/bytes", "byte outside 0..255")
				}
			}
		}
		if alternatives := fields["top_logprobs"]; !alternatives.IsZero() && !alternatives.IsNull() {
			var a []Value
			if alternatives.Decode(&a) != nil {
				return streamIssue(InvalidInput, at+"/top_logprobs", "expected array")
			}
			for i, v := range a {
				if err := token(v, fmt.Sprintf("%s/top_logprobs/%d", at, i), true); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i, v := range entries {
		if err := token(v, fmt.Sprintf("%s/%d", at, i), false); err != nil {
			return err
		}
	}
	return nil
}
