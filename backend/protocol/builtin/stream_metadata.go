package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
)

func mergeResponseMetadata(left, right []p.ResponseMetadata) []p.ResponseMetadata {
	out := append([]p.ResponseMetadata(nil), left...)
	for _, v := range right {
		hasMatch := false
		for i := range out {
			// Response/usage fields have one owner per stream. A decoder may
			// expose the same snapshot field via /response/name or /name;
			// Path identifies its diagnostic source, not a second field. Keep
			// path identity for metadata belonging to distinct choices/items.
			hasSameOwner := out[i].Path == v.Path || v.Location == "response" || v.Location == "usage"
			if out[i].Codec == v.Codec && out[i].Location == v.Location && out[i].Name == v.Name && hasSameOwner {
				out[i] = v
				hasMatch = true
				break
			}
		}
		if !hasMatch {
			out = append(out, v)
		}
	}
	return out
}

type streamMetadataGroups struct {
	local, choices, frames []p.ResponseMetadata
	item                   *streamItem
	key                    string
}

// Metadata has explicit node ownership. A metadata-only update emits a frame
// for that same item; it never waits for an unrelated subsequent text delta.
func (stream *streamModule) renderMetadata(frames []p.Value, event *p.Event, options p.EvaluationContext) ([]p.Value, error) {
	groups, err := stream.classifyMetadata(event)
	if err != nil {
		return nil, err
	}
	extra := stream.renderChatChoiceMetadata(frames, groups.choices)
	var itemFrames []p.Value
	switch stream.name {
	case Chat:
		itemFrames, err = stream.renderChatItemMetadata(frames, event, groups.item, groups.local)
	case Responses:
		itemFrames, err = stream.renderResponsesItemMetadata(frames, event, groups.key, groups.item, groups.local)
	}
	if err != nil {
		return nil, err
	}
	extra = append(extra, itemFrames...)
	if event != nil && event.Type == p.ItemFinished {
		frames = append(extra, frames...)
	} else {
		frames = append(frames, extra...)
	}

	if err := stream.writeResponseMetadata(frames); err != nil {
		return nil, err
	}
	frames, err = stream.renderEventMetadata(frames, event, groups.frames, options)
	if err != nil {
		return nil, err
	}
	metadata, _ := p.EncodeValue(stream.metadata)
	if len(metadata.Bytes()) > stream.limits.BufferBytes {
		return nil, unsupported("/metadata", "stream metadata exceeds buffer limit")
	}
	return frames, nil
}

// All groups are checked against the target codec, including events without
// output frames. Later attachment steps operate only on these checked groups.
func (stream *streamModule) classifyMetadata(event *p.Event) (streamMetadataGroups, error) {
	var local []p.ResponseMetadata
	var choiceMetadata []p.ResponseMetadata
	var frameMetadata []p.ResponseMetadata
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
			// Check even when this event emits no frame (Gemini start, usage
			// updates). A disabled projection must not silently lose metadata.
			if m.Codec != stream.name {
				return streamMetadataGroups{}, unsupported(m.Path, "stream metadata requires target projection")
			}
			if strings.HasPrefix(m.Location, "event:") {
				frameMetadata = append(frameMetadata, m)
			} else if m.Codec == Chat && m.Location == "choice" && m.Name == "native_finish_reason" {
				choiceMetadata = append(choiceMetadata, m)
			} else if m.Location == "response" || m.Location == "usage" {
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
		return streamMetadataGroups{}, unsupported("/metadata", "content metadata has no explicit stream item association")
	}
	return streamMetadataGroups{local: local, choices: choiceMetadata, frames: frameMetadata, item: item, key: key}, nil
}

func (stream *streamModule) renderChatChoiceMetadata(frames []p.Value, choiceMetadata []p.ResponseMetadata) []p.Value {
	var extra []p.Value
	if len(choiceMetadata) > 0 {
		isAttached := false
		for i, frame := range frames {
			fields, _ := frame.ReadObject()
			choices, _ := readArray(fields["choices"])
			if len(choices) != 1 {
				continue
			}
			choice, _ := choices[0].ReadObject()
			for _, m := range choiceMetadata {
				choice[m.Name] = m.Value
			}
			choices[0] = object(choice)
			fields["choices"] = array(choices)
			frames[i] = object(fields)
			isAttached = true
			break
		}
		if !isAttached {
			fields, _ := stream.chatChunk(object(p.Object{}), p.Value{}, p.Value{}).ReadObject()
			choices, _ := readArray(fields["choices"])
			choice, _ := choices[0].ReadObject()
			for _, m := range choiceMetadata {
				choice[m.Name] = m.Value
			}
			choices[0] = object(choice)
			fields["choices"] = array(choices)
			extra = append(extra, object(fields))
		}
	}
	return extra
}

func (stream *streamModule) renderChatItemMetadata(frames []p.Value, event *p.Event, item *streamItem, local []p.ResponseMetadata) ([]p.Value, error) {
	var extra []p.Value
	for _, m := range local {
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
		isAttached := false
		for i, v := range frames {
			fields, _ := v.ReadObject()
			choices, _ := readArray(fields["choices"])
			if len(choices) != 1 {
				continue
			}
			choiceFields, _ := choices[0].ReadObject()
			deltaFields, _ := choiceFields["delta"].ReadObject()
			if deltaFields["content"].IsZero() {
				continue
			}
			for k, v := range choice {
				choiceFields[k] = v
			}
			for k, v := range delta {
				deltaFields[k] = v
			}
			choiceFields["delta"] = object(deltaFields)
			choices[0] = object(choiceFields)
			fields["choices"] = array(choices)
			frames[i] = object(fields)
			isAttached = true
			break
		}
		if !isAttached {
			fields, _ := stream.chatChunk(object(delta), p.Value{}, p.Value{}).ReadObject()
			choices, _ := readArray(fields["choices"])
			choiceFields, _ := choices[0].ReadObject()
			for k, v := range choice {
				choiceFields[k] = v
			}
			choices[0] = object(choiceFields)
			fields["choices"] = array(choices)
			extra = append(extra, object(fields))
		}
	}
	return extra, nil
}

func (stream *streamModule) renderResponsesItemMetadata(frames []p.Value, event *p.Event, key string, item *streamItem, local []p.ResponseMetadata) ([]p.Value, error) {
	var extra []p.Value
	for _, m := range local {
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
				fields, _ := stream.responsesEvent("response.output_text.annotation.added", key, item, "annotation", entries[i]).ReadObject()
				fields["annotation_index"], _ = p.EncodeValue(i)
				extra = append(extra, object(fields))
			}
			item.deliveredAnnotations = m.Value
		} else if m.Name == "logprobs" && event.Type == p.ItemDelta {
			isAttached := false
			for i, v := range frames {
				fields, _ := v.ReadObject()
				if fields["type"].IsString("response.output_text.delta") {
					fields["logprobs"] = m.Value
					frames[i] = object(fields)
					isAttached = true
					break
				}
			}
			if !isAttached {
				fields, _ := stream.responsesEvent("response.output_text.delta", key, item, "delta", p.StringValue("")).ReadObject()
				fields["logprobs"] = m.Value
				extra = append(extra, object(fields))
			}
		}
	}
	return extra, nil
}

func (stream *streamModule) writeResponseMetadata(frames []p.Value) error {
	for i, v := range frames {
		fields, _ := v.ReadObject()
		switch stream.name {
		case Chat:
			if err := stream.module.writeMetadata(fields, stream.metadata, "response"); err != nil {
				return err
			}
		case Responses:
			if !fields["response"].IsZero() {
				r, _ := fields["response"].ReadObject()
				if err := stream.module.writeMetadata(r, stream.metadata, "response"); err != nil {
					return err
				}
				fields["response"] = object(r)
			}
		case Anthropic, Gemini:
			usageKey := "usage"
			if stream.name == Gemini {
				usageKey = "usageMetadata"
			}
			if !fields[usageKey].IsZero() {
				u, _ := fields[usageKey].ReadObject()
				if err := stream.module.writeMetadata(u, stream.metadata, "usage"); err != nil {
					return err
				}
				fields[usageKey] = object(u)
			}
		}
		frames[i] = object(fields)
	}
	return nil
}

func (stream *streamModule) renderEventMetadata(frames []p.Value, event *p.Event, frameMetadata []p.ResponseMetadata, options p.EvaluationContext) ([]p.Value, error) {
	for _, m := range frameMetadata {
		if event != nil && event.Type == p.ResponseFinished && len(frames) == 0 {
			// The terminal is held for late usage. Its frame metadata stays on
			// the pending event and is rendered with that single terminal.
			continue
		}
		if m.Location == "event:response.in_progress" && len(frames) == 0 && event != nil && event.Type == p.MetadataUpdated && event.Response != nil {
			response, err := stream.module.encodeResponse(event.Response, options)
			if err != nil {
				return nil, err
			}
			frames = append(frames, object(p.Object{"type": p.StringValue("response.in_progress"), "response": response}))
		}
		isAttached := false
		for i, frame := range frames {
			fields, _ := frame.ReadObject()
			if fields["type"].IsString(strings.TrimPrefix(m.Location, "event:")) {
				fields[m.Name] = m.Value
				frames[i] = object(fields)
				isAttached = true
			}
		}
		if !isAttached {
			return nil, unsupported(m.Path, "event metadata has no corresponding target frame")
		}
	}
	return frames, nil
}

func (stream *streamModule) attachFrameMetadata(events []p.Event, metadata []p.ResponseMetadata) ([]p.Event, error) {
	for _, m := range metadata {
		if strings.HasPrefix(m.Location, "event:") {
			if len(events) == 0 {
				return nil, unsupported(m.Path, "event padding arrived without a semantic delta")
			}
			events[len(events)-1].Metadata = append(events[len(events)-1].Metadata, m)
			continue
		}
		// This field belongs to the sole supported streaming choice, including
		// tool-only replies. Do not fabricate a text item to carry it.
		if m.Codec == Chat && m.Location == "choice" && m.Name == "native_finish_reason" {
			if len(events) == 0 {
				events = append(events, p.Event{Type: p.MetadataUpdated})
			}
			events[len(events)-1].Metadata = append(events[len(events)-1].Metadata, m)
			continue
		}
		// Response, usage and candidate metadata do not depend on a text item.
		if m.Location == "response" || m.Location == "usage" || m.Location == "candidate" {
			if len(events) == 0 {
				events = append(events, p.Event{Type: p.MetadataUpdated})
			}
			events[0].Metadata = append(events[0].Metadata, m)
			continue
		}
		hasMatch := false
		for i, e := range events {
			if e.Type == p.ItemDelta && e.ItemID.IsString("text") {
				events[i].Metadata = append(events[i].Metadata, m)
				hasMatch = true
				break
			}
		}
		if !hasMatch {
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
