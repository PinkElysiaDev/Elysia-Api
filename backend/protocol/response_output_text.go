package protocol

import "strings"

// ValidateResponseOutputText prevents a derived text field from hiding a
// contradictory answer, including after a custom semantic mapping.
func ValidateResponseOutputText(response *Response) error {
	for _, field := range response.Metadata {
		if field.Codec != "responses" || field.Location != "response" || field.Name != "output_text" {
			continue
		}
		if err := ValidateMetadataValue(field.Codec, field.Location, field.Name, field.Value, field.Path); err != nil {
			return err
		}
		var expected strings.Builder
		for _, message := range response.Content {
			if message.Kind != MessageNode {
				continue
			}
			for _, part := range message.Children {
				if part.Kind != TextNode {
					continue
				}
				var text string
				if part.Payload.IsNull() || part.Payload.Decode(&text) != nil {
					return streamIssue(InvalidInput, field.Path, "output_text requires valid visible text content")
				}
				expected.WriteString(text)
			}
		}
		var actual string
		_ = field.Value.Decode(&actual)
		if actual != expected.String() {
			return streamIssue(InvalidInput, field.Path, "output_text conflicts with ordered visible output")
		}
	}
	return nil
}
