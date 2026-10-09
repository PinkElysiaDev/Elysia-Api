package protocol

import (
	"fmt"
	"unicode/utf16"
)

// Citation positions use the text owner's character offsets. Moving text into
// Chat's single assistant content field rebases only the public URL form.
func RebaseURLMetadata(items []ResponseMetadata, offset int) ([]ResponseMetadata, error) {
	out := append([]ResponseMetadata(nil), items...)
	for i, m := range out {
		if m.Name != "annotations" || m.Codec != "openai-chat" || m.Value.IsNull() {
			continue
		}
		var entries []Value
		if err := m.Value.Decode(&entries); err != nil {
			return nil, err
		}
		for j, v := range entries {
			f, _ := v.ReadObject()
			citation, err := f["url_citation"].ReadObject()
			if err != nil {
				return nil, err
			}
			for _, key := range []string{"start_index", "end_index"} {
				var n int64
				if citation[key].Decode(&n) != nil {
					return nil, streamIssue(InvalidInput, m.Path, "invalid citation offset")
				}
				citation[key], _ = EncodeValue(n + int64(offset))
			}
			f["url_citation"], _ = EncodeValue(citation)
			entries[j], _ = EncodeValue(f)
		}
		out[i].Value, _ = EncodeValue(entries)
	}
	return out, nil
}

func (c *CompiledConversion) chatContentShape(nodes []Node, phase ConversionPhase, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) ([]Node, error) {
	textIndex, offset := -1, 0
	var out []Node
	for _, n := range nodes {
		if n.Kind != TextNode {
			out = append(out, n)
			continue
		}
		var value string
		if n.Payload.Decode(&value) != nil {
			return nil, streamIssue(InvalidInput, "/content", "text payload must be a string")
		}
		if textIndex < 0 {
			textIndex = len(out)
			out = append(out, n)
			offset = len(utf16.Encode([]rune(value)))
			continue
		}
		if len(n.Cache) > 0 || len(n.Resources) > 0 || len(n.Attributes) > 0 || !n.ID.IsZero() || !n.Status.IsZero() {
			return nil, streamIssue(UnsupportedCapability, "/content", "cannot merge text carrying scoped state")
		}
		if err := c.issue(rule, phase, route, "/content", "Chat combines text blocks and cannot retain their boundaries or interleaving with tools", sink, true); err != nil {
			return nil, err
		}
		metadata, err := RebaseURLMetadata(n.Metadata, offset)
		if err != nil {
			return nil, err
		}
		for _, m := range metadata {
			merged := false
			for j, old := range out[textIndex].Metadata {
				if old.Name != m.Name || old.Codec != m.Codec {
					continue
				}
				if m.Name == "annotations" {
					var a, b []Value
					_ = old.Value.Decode(&a)
					_ = m.Value.Decode(&b)
					m.Value, _ = EncodeValue(append(a, b...))
				} else if m.Name == "logprobs" {
					m.Value = appendLogprobValues(old.Value, m.Value, m.Codec)
				} else {
					return nil, streamIssue(UnsupportedCapability, m.Path, "metadata has no concatenation mapping")
				}
				out[textIndex].Metadata[j] = m
				merged = true
				break
			}
			if !merged {
				out[textIndex].Metadata = append(out[textIndex].Metadata, m)
			}
		}
		var previous string
		_ = out[textIndex].Payload.Decode(&previous)
		out[textIndex].Payload = StringValue(previous + value)
		offset += len(utf16.Encode([]rune(value)))
	}
	return out, nil
}

// Positions are tracked in UTF-16 code units, like the public OpenAI citation
// offsets. Contiguous deltas coalesce; interleaving is bounded by StateItems.
type chatTextSpan struct{ local, global, length int }
type chatTextItem struct {
	kind   NodeKind
	length int
	spans  []chatTextSpan
}
type chatTextProjection struct {
	identities               *ItemIdentities
	items                    map[string]*chatTextItem
	length, spans, textItems int
}

func (c *CompiledConversion) chatEventShape(e *Event, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	if e.Type != ItemStarted && e.Type != ItemDelta && e.Type != ItemSnapshot && e.Type != ItemFinished {
		return nil
	}
	if route.Delivery == nil {
		return streamIssue(InvalidInput, "/conversion/events", "stream shape projection requires request-owned delivery state")
	}
	state := route.Delivery.chatText
	if state == nil {
		state = &chatTextProjection{identities: NewItemIdentities(c.limits.StateItems), items: map[string]*chatTextItem{}}
		route.Delivery.chatText = state
	}
	key, err := state.identities.Resolve(*e)
	if err != nil {
		return err
	}
	item := state.items[key]
	if item == nil {
		if e.Item == nil {
			return streamIssue(InvalidAssociation, "/item", "text projection requires an item start")
		}
		if len(state.items) >= c.limits.StateItems {
			return streamIssue(UnsupportedCapability, "/content", "text projection item limit exceeded")
		}
		item = &chatTextItem{kind: e.Item.Kind}
		state.items[key] = item
		if item.kind == TextNode {
			state.textItems++
			if state.textItems > 1 {
				if err := c.issue(rule, ConversionEvent, route, "/content", "Chat combines text blocks and cannot retain their boundaries or interleaving with tools", sink, true); err != nil {
					return err
				}
			}
		}
	}
	if item.kind != TextNode {
		return nil
	}
	value := e.Delta
	snapshot := IsItemSnapshot(e.Type)
	if snapshot && e.Item != nil {
		value = e.Item.Payload
	}
	if !value.IsZero() {
		var text string
		if value.Decode(&text) != nil {
			return streamIssue(InvalidInput, "/item/payload", "text must be a string")
		}
		length := len(utf16.Encode([]rune(text)))
		if snapshot {
			length -= item.length
		}
		if length < 0 {
			return streamIssue(InvalidAssociation, "/item/payload", "text snapshot shrank")
		}
		if length > 0 {
			n := len(item.spans)
			if n > 0 && item.spans[n-1].global+item.spans[n-1].length == state.length {
				item.spans[n-1].length += length
			} else {
				if state.spans >= c.limits.StateItems {
					return streamIssue(UnsupportedCapability, "/content", "text projection span limit exceeded")
				}
				item.spans = append(item.spans, chatTextSpan{local: item.length, global: state.length, length: length})
				state.spans++
			}
			item.length += length
			state.length += length
		}
	}
	rebase := func(metadata []ResponseMetadata) ([]ResponseMetadata, error) {
		out := append([]ResponseMetadata(nil), metadata...)
		for i, m := range out {
			if m.Codec != "openai-chat" || m.Name != "annotations" || m.Value.IsNull() {
				continue
			}
			var entries []Value
			if err := m.Value.Decode(&entries); err != nil {
				return nil, err
			}
			for j, v := range entries {
				f, _ := v.ReadObject()
				citation, err := f["url_citation"].ReadObject()
				if err != nil {
					return nil, err
				}
				var start, end int
				if citation["start_index"].Decode(&start) != nil || citation["end_index"].Decode(&end) != nil || start < 0 || end < start {
					return nil, streamIssue(InvalidInput, m.Path, "invalid citation range")
				}
				found := false
				for _, span := range item.spans {
					if start >= span.local && end <= span.local+span.length {
						citation["start_index"], _ = EncodeValue(start + span.global - span.local)
						citation["end_index"], _ = EncodeValue(end + span.global - span.local)
						found = true
						break
					}
				}
				if !found {
					return nil, streamIssue(UnsupportedCapability, fmt.Sprintf("%s/%d", m.Path, j), "citation extends beyond delivered text or crosses interleaved text from another item")
				}
				f["url_citation"], _ = EncodeValue(citation)
				entries[j], _ = EncodeValue(f)
			}
			out[i].Value, _ = EncodeValue(entries)
		}
		return out, nil
	}
	e.Metadata, err = rebase(e.Metadata)
	if err != nil {
		return err
	}
	if e.Item != nil {
		e.Item.Metadata, err = rebase(e.Item.Metadata)
	}
	return err
}
