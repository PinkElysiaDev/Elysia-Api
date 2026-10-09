package builtin

import p "github.com/elysia-api/backend/protocol"

// RepairOutputFixtures corrects a specific old builtin encoder expectation.
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
		if (s.Direction != p.EncodeResponse && s.Direction != p.EncodeEvent) || m.Module != Responses || m.After != nil || s.ExpectedIssue != "" {
			continue
		}
		// A native replay fixture asserts exact original bytes, not generated
		// output. Keep that assertion; final delivery independently checks shape.
		if s.Direction == p.EncodeResponse {
			input, _ := s.Input.ReadObject()
			if !input["native"].IsZero() && !input["native"].IsNull() {
				continue
			}
		}
		sequence = s.Direction == p.EncodeEvent
		d.Samples[i].Expected = repair(s.Expected)
	}
	return d, changed
}
