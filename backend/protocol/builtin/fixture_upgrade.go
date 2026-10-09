package builtin

import (
	"crypto/sha256"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

// RepairOutputFixtures corrects known obsolete builtin output expectations,
// including the two fingerprinted Responses message-completion decoder oracles.
// It changes the definition, never comparison semantics or runtime output.
// The caller verifies and commits a new immutable revision and keeps the old
// revision and operator draft. Handwritten/after mappings are not rewritten.
func RepairOutputFixtures(d p.Definition) (p.Definition, bool) {
	changed := false
	sequence := false
	d.Samples = append([]p.Sample(nil), d.Samples...)
	status := func(v p.Value, name string) p.Value {
		f, err := v.ReadObject()
		if err != nil {
			return v
		}
		if !sequence || (f["type"] != p.StringValue("function_call") && f["type"] != p.StringValue("custom_tool_call") && f["type"] != p.StringValue("reasoning")) {
			return v
		}
		if f["status"].IsZero() {
			f["status"] = p.StringValue(name)
			changed = true
		}
		out, _ := p.EncodeValue(f)
		return out
	}
	var repair func(p.Value) p.Value
	repair = func(v p.Value) p.Value {
		if !v.IsObject() {
			var a []p.Value
			if v.IsZero() || v.IsNull() || v.Decode(&a) != nil {
				return v
			}
			for i := range a {
				a[i] = repair(a[i])
			}
			out, _ := p.EncodeValue(a)
			return out
		}
		f, _ := v.ReadObject()
		// Only protocol containers, never arbitrary tool arguments or results.
		switch f["type"] {
		case p.StringValue("output_text"):
			if f["annotations"].IsZero() {
				f["annotations"], _ = p.EncodeValue([]p.Value{})
				changed = true
			}
		case p.StringValue("message"):
			if !f["content"].IsZero() {
				f["content"] = repair(f["content"])
			}
		case p.StringValue("response.created"), p.StringValue("response.in_progress"), p.StringValue("response.completed"), p.StringValue("response.incomplete"):
			if !f["response"].IsZero() {
				f["response"] = repair(f["response"])
			}
		case p.StringValue("response.output_item.added"), p.StringValue("response.output_item.done"):
			if !f["item"].IsZero() {
				name := "completed"
				if f["type"] == p.StringValue("response.output_item.added") {
					name = "in_progress"
				}
				f["item"] = status(f["item"], name)
				f["item"] = repair(f["item"])
			}
		case p.StringValue("response.content_part.added"), p.StringValue("response.content_part.done"):
			if !f["part"].IsZero() {
				f["part"] = repair(f["part"])
			}
		}
		if f["object"] == p.StringValue("response") && !f["output"].IsZero() {
			f["output"] = repair(f["output"])
			if f["status"] == p.StringValue("completed") || f["status"] == p.StringValue("incomplete") {
				var nodes []p.Value
				if f["output"].Decode(&nodes) == nil {
					for i := range nodes {
						nodes[i] = status(nodes[i], "completed")
					}
					f["output"], _ = p.EncodeValue(nodes)
				}
			}
		}
		out, _ := p.EncodeValue(f)
		return out
	}
	for i, s := range d.Samples {
		m := d.Directions[s.Direction]
		if s.Direction == p.DecodeEvent && m.Module == Responses && m.After == nil && s.Sequence && s.ExpectedIssue == "" {
			if expected, repaired := repairResponsesMessageOracle(s); repaired {
				d.Samples[i].Expected, changed = expected, true
			}
		}
		if fixtureHasNativeReplay(s) {
			continue
		}
		if s.Direction == p.EncodeEvent && m.Module == Chat && m.After == nil && s.Sequence && s.ExpectedIssue == "" {
			if fixed, didChange := repairChatToolFrames(s.Expected); didChange {
				d.Samples[i].Expected, changed = fixed, true
			}
		}
		if (s.Direction != p.EncodeResponse && s.Direction != p.EncodeEvent) || m.Module != Responses || m.After != nil || s.ExpectedIssue != "" {
			continue
		}
		sequence = s.Direction == p.EncodeEvent
		d.Samples[i].Expected = repair(s.Expected)
	}
	return d, changed
}

// Only the two exact shipped dev.28 decoder fixtures closed text before its
// owning message. Advance those oracles with the corrected lifecycle. Do not
// recalculate arbitrary user expectations from the implementation under test.
// Canonical JSON keeps json.Number so the native large-number fixture is exact.
func repairResponsesMessageOracle(sample p.Sample) (p.Value, bool) {
	var input, expected any
	if sample.Input.Decode(&input) != nil || sample.Expected.Decode(&expected) != nil {
		return sample.Expected, false
	}
	canonical, err := p.EncodeValue([]any{input, expected})
	if err != nil {
		return sample.Expected, false
	}
	switch fmt.Sprintf("%x", sha256.Sum256(canonical.Bytes())) {
	case "1c4b7aacd0ae89d8312ef93616371b348e0e9e46249ae9337d188c71b76b97a2", "4e1c2a7395c0ce684129ff3e9b1c367ba54302ab5269593c882ab684fc2cb2ac":
	default:
		return sample.Expected, false
	}
	var events []p.Value
	_ = sample.Expected.Decode(&events)
	// Existing event 3 is the complete text snapshot; preserve its payload and
	// association for the part snapshot and the subsequent message completion.
	snapshot := events[3]
	finished, _ := snapshot.ReadObject()
	finished["type"] = p.StringValue(string(p.ItemFinished))
	result := append([]p.Value(nil), events[:4]...)
	result = append(result, snapshot, object(finished))
	result = append(result, events[5:]...)
	return array(result), true
}

// Native replay fixtures assert original output, not an old generated oracle.
// Only inspect semantic containers; tool payload properties are not provenance.
func fixtureHasNativeReplay(s p.Sample) bool {
	if s.Direction != p.EncodeResponse && s.Direction != p.EncodeEvent {
		return false
	}
	inputs := []p.Value{s.Input}
	if s.Sequence {
		if err := s.Input.Decode(&inputs); err != nil {
			return true // Malformed fixtures need explicit repair, never guessing.
		}
	}
	var hasNative func(p.Value) bool
	hasNative = func(value p.Value) bool {
		fields, err := value.ReadObject()
		if err != nil {
			return false
		}
		if !fields["native"].IsZero() && !fields["native"].IsNull() {
			return true
		}
		for _, name := range []string{"item", "response"} {
			if hasNative(fields[name]) {
				return true
			}
		}
		for _, name := range []string{"content", "children"} {
			var nodes []p.Value
			if fields[name].Decode(&nodes) == nil {
				for _, node := range nodes {
					if hasNative(node) {
						return true
					}
				}
			}
		}
		return false
	}
	for _, input := range inputs {
		if hasNative(input) {
			return true
		}
	}
	return false
}

// Only remove a repeated, identical tool identity after its explicit start.
// This repairs the old generated oracle; it never weakens the comparator.
func repairChatToolFrames(value p.Value) (p.Value, bool) {
	var frames []p.Value
	if value.Decode(&frames) != nil {
		return value, false
	}
	type identity struct{ id, name p.Value }
	seen := map[string]identity{}
	changed := false
	for i, frame := range frames {
		f, err := frame.ReadObject()
		if err != nil || f["object"] != p.StringValue("chat.completion.chunk") {
			continue
		}
		choices, _ := readArray(f["choices"])
		for j, choice := range choices {
			ch, _ := choice.ReadObject()
			delta, err := ch["delta"].ReadObject()
			if err != nil {
				continue
			}
			calls, _ := readArray(delta["tool_calls"])
			for k, call := range calls {
				entry, _ := call.ReadObject()
				fn, err := entry["function"].ReadObject()
				if err != nil || entry["index"].IsZero() || ch["index"].IsZero() {
					continue
				}
				key := string(ch["index"].Bytes()) + ":" + string(entry["index"].Bytes())
				previous, exists := seen[key]
				if !exists && entry["type"] == p.StringValue("function") && !entry["id"].IsZero() && !fn["name"].IsZero() {
					seen[key] = identity{entry["id"], fn["name"]}
				} else if exists && entry["type"].IsZero() {
					if entry["id"] == previous.id {
						delete(entry, "id")
						changed = true
					}
					if fn["name"] == previous.name {
						delete(fn, "name")
						changed = true
					}
					entry["function"] = object(fn)
					calls[k] = object(entry)
				}
			}
			if len(calls) > 0 {
				delta["tool_calls"] = array(calls)
			}
			ch["delta"] = object(delta)
			choices[j] = object(ch)
		}
		f["choices"] = array(choices)
		frames[i] = object(f)
	}
	if !changed {
		return value, false
	}
	return array(frames), true
}
