package builtin

import (
	p "github.com/elysia-api/backend/protocol"
)

func mergeResponseMetadata(left, right []p.ResponseMetadata) []p.ResponseMetadata {
	out := append([]p.ResponseMetadata(nil), left...)
	for _, v := range right {
		found := false
		for i := range out {
			if out[i].Codec == v.Codec && out[i].Location == v.Location && out[i].Name == v.Name && out[i].Path == v.Path {
				out[i] = v
				found = true
				break
			}
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}

// Metadata has explicit node ownership. A metadata-only update emits a frame
// for that same item; it never waits for an unrelated subsequent text delta.
func (stream *streamModule) renderMetadata(frames []p.Value, event *p.Event) ([]p.Value, error) {
	var local []p.ResponseMetadata
	var item *streamItem
	var key string
	if event != nil {
		items := append([]p.ResponseMetadata(nil), event.Metadata...)
		if event.Response != nil {
			items = append(items, event.Response.Metadata...)
		}
		if event.Item != nil {
			items = append(items, event.Item.Metadata...)
		}
		for _, m := range items {
			if m.Location == "response" || m.Location == "usage" {
				stream.metadata = mergeResponseMetadata(stream.metadata, []p.ResponseMetadata{m})
			} else {
				local = append(local, m)
			}
		}
		if len(local) > 0 && (!event.ItemID.IsZero() || event.Index != nil) {
			key, _ = stream.identities.Resolve(*event)
			item = stream.items[key]
		}
	}
	if len(local) > 0 && item == nil {
		return nil, unsupported("/metadata", "content metadata has no explicit stream item association")
	}
	var extra []p.Value
	for _, m := range local {
		if m.Codec != stream.name {
			return nil, unsupported(m.Path, "stream metadata requires target projection")
		}
		if stream.name == Chat {
			// Snapshot logprobs were already delivered by their token deltas.
			if m.Name == "logprobs" && event.Type != p.ItemDelta {
				continue
			}
			delta, choice := p.Object{}, p.Object{}
			if m.Location == "message" {
				delta[m.Name] = m.Value
			} else if m.Location == "choice" {
				choice[m.Name] = m.Value
			} else {
				return nil, unsupported(m.Path, "unsupported Chat metadata owner")
			}
			if m.Name == "annotations" {
				if item.deliveredAnnotations == m.Value {
					continue
				}
				item.deliveredAnnotations = m.Value
			}
			attached := false
			for i, v := range frames {
				f, _ := v.ReadObject()
				choices, _ := readArray(f["choices"])
				if len(choices) != 1 {
					continue
				}
				ch, _ := choices[0].ReadObject()
				d, _ := ch["delta"].ReadObject()
				if d["content"].IsZero() {
					continue
				}
				for k, v := range choice {
					ch[k] = v
				}
				for k, v := range delta {
					d[k] = v
				}
				ch["delta"] = object(d)
				choices[0] = object(ch)
				f["choices"] = array(choices)
				frames[i] = object(f)
				attached = true
				break
			}
			if !attached {
				f, _ := stream.chatChunk(object(delta), p.Value{}, p.Value{}).ReadObject()
				choices, _ := readArray(f["choices"])
				ch, _ := choices[0].ReadObject()
				for k, v := range choice {
					ch[k] = v
				}
				choices[0] = object(ch)
				f["choices"] = array(choices)
				extra = append(extra, object(f))
			}
		} else if stream.name == Responses {
			if m.Name == "annotations" {
				if item.deliveredAnnotations == m.Value {
					continue
				}
				var old, entries []p.Value
				_ = item.deliveredAnnotations.Decode(&old)
				_ = m.Value.Decode(&entries)
				if len(entries) < len(old) {
					return nil, unsupported(m.Path, "citation snapshot removed previously emitted entries")
				}
				for i := range old {
					if old[i] != entries[i] {
						return nil, unsupported(m.Path, "citation snapshot rewrote previously emitted entries")
					}
				}
				for i := len(old); i < len(entries); i++ {
					f, _ := stream.responsesEvent("response.output_text.annotation.added", key, item, "annotation", entries[i]).ReadObject()
					f["annotation_index"], _ = p.EncodeValue(i)
					extra = append(extra, object(f))
				}
				item.deliveredAnnotations = m.Value
			} else if m.Name == "logprobs" && event.Type == p.ItemDelta {
				attached := false
				for i, v := range frames {
					f, _ := v.ReadObject()
					if f["type"] == p.StringValue("response.output_text.delta") {
						f["logprobs"] = m.Value
						frames[i] = object(f)
						attached = true
						break
					}
				}
				if !attached {
					f, _ := stream.responsesEvent("response.output_text.delta", key, item, "delta", p.StringValue("")).ReadObject()
					f["logprobs"] = m.Value
					extra = append(extra, object(f))
				}
			}
		}
	}
	if event != nil && event.Type == p.ItemFinished {
		frames = append(extra, frames...)
	} else {
		frames = append(frames, extra...)
	}
	for i, v := range frames {
		f, _ := v.ReadObject()
		switch stream.name {
		case Chat:
			if err := stream.module.writeMetadata(f, stream.metadata, "response"); err != nil {
				return nil, err
			}
		case Responses:
			if !f["response"].IsZero() {
				r, _ := f["response"].ReadObject()
				if err := stream.module.writeMetadata(r, stream.metadata, "response"); err != nil {
					return nil, err
				}
				f["response"] = object(r)
			}
		case Anthropic:
			if !f["usage"].IsZero() {
				u, _ := f["usage"].ReadObject()
				if err := stream.module.writeMetadata(u, stream.metadata, "usage"); err != nil {
					return nil, err
				}
				f["usage"] = object(u)
			}
		}
		frames[i] = object(f)
	}
	metadata, _ := p.EncodeValue(stream.metadata)
	if len(metadata.Bytes()) > stream.limits.BufferBytes {
		return nil, unsupported("/metadata", "stream metadata exceeds buffer limit")
	}
	return frames, nil
}

func (stream *streamModule) attachFrameMetadata(events []p.Event, metadata []p.ResponseMetadata) ([]p.Event, error) {
	for _, m := range metadata {
		if m.Location == "response" || m.Location == "usage" {
			if len(events) == 0 {
				events = append(events, p.Event{Type: p.MetadataUpdated})
			}
			events[0].Metadata = append(events[0].Metadata, m)
			continue
		}
		if m.Location == "candidate" { // Candidate details have no text dependency.
			if len(events) == 0 {
				events = append(events, p.Event{Type: p.MetadataUpdated})
			}
			events[0].Metadata = append(events[0].Metadata, m)
			continue
		}
		found := false
		for i, e := range events {
			if e.Type == p.ItemDelta && e.ItemID == p.StringValue("text") {
				events[i].Metadata = append(events[i].Metadata, m)
				found = true
				break
			}
		}
		if !found {
			item := stream.items["text"]
			if item == nil {
				if m.Value.IsNull() {
					if len(events) == 0 {
						events = append(events, p.Event{Type: p.MetadataUpdated})
					}
					events[0].Metadata = append(events[0].Metadata, m)
					continue
				}
				return nil, unsupported(m.Path, "text metadata arrived without a text item")
			}
			node := item.node
			node.Metadata = nil
			e, err := stream.itemEvent(p.ItemSnapshot, "text", &node, p.Value{})
			if err != nil {
				return nil, err
			}
			e.Metadata = []p.ResponseMetadata{m}
			// Keep terminal last, so a metadata-only tail remains an item update.
			pos := len(events)
			for i, e := range events {
				if e.Type == p.ResponseFinished {
					pos = i
					break
				}
			}
			events = append(events, p.Event{})
			copy(events[pos+1:], events[pos:])
			events[pos] = e
		}
	}
	return events, nil
}
