package protocol

import "testing"

func arrayStreamSpec(t *testing.T) *ProtocolStreamSpec {
	t.Helper()
	spec := eventProtocolPackage(t).Spec.Documents["provider-event"].Stream
	spec.Namespaces["metadata"] = ProtocolStreamNamespaceSpec{KeyType: StringType}
	spec.Entities["scores"] = ProtocolStreamEntitySpec{
		Namespace: "metadata", Payload: "json", ParentNamespace: "item", ParentEntities: []string{"text"}, CloseWithParent: true,
		PayloadSchema: &ValueSchema{Type: ArrayType, Nullable: true, Items: &ValueSchema{Type: ObjectType, AllowUnknown: true}},
	}
	action := func(kind, encoding, payload string) ProtocolStreamActionSpec {
		value := ProtocolStreamActionSpec{Action: kind, Entity: "scores", Key: &ProtocolStreamValueSpec{Path: "/item_id"}, Parent: &ProtocolStreamValueSpec{Path: "/item_id"}, Encoding: encoding}
		if payload != "" {
			value.Payload = &ProtocolStreamValueSpec{Path: payload}
		}
		return value
	}
	start := action("entity_start", "", "")
	start.Implicit = true
	appendValues := action("entity_append", "value", "/values")
	appendValues.Offset = &ProtocolStreamValueSpec{Path: "/offset"}
	appendItem := action("entity_append", "item", "/value")
	appendItem.Offset = &ProtocolStreamValueSpec{Path: "/offset"}
	verifyParent := ProtocolStreamActionSpec{Action: "entity_verify", Entity: "text", Key: &ProtocolStreamValueSpec{Path: "/item_id"}, Payload: &ProtocolStreamValueSpec{Path: "/text"}, VerifyChildren: []string{"metadata"}}
	spec.Events["array.delta"] = ProtocolStreamEventSpec{start, appendValues}
	spec.Events["array.item"] = ProtocolStreamEventSpec{start, appendItem}
	spec.Events["array.verify"] = ProtocolStreamEventSpec{action("entity_verify", "value", "/values"), verifyParent}
	spec.Events["parent.verify"] = ProtocolStreamEventSpec{verifyParent}
	return spec
}

func TestPackageWireArrayLifecycle(t *testing.T) {
	prefix := []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(ItemStarted, 1, `,"item_id":"t"`),
		packageStreamFrame(ItemDelta, 2, `,"item_id":"t","text":"hello"`),
		packageStreamFrame("array.item", 3, `,"item_id":"t","offset":0,"value":{"score":9007199254740993}`),
		packageStreamFrame("array.delta", 4, `,"item_id":"t","offset":1,"values":[{"score":2}]`),
		packageStreamFrame(ItemFinished, 5, `,"item_id":"t"`),
	}
	consumePrefix := func(frames []Value) *packageStreamValidation {
		state := newSparseIdentityValidation(t, arrayStreamSpec(t))
		for _, frame := range frames {
			if err := state.consume(t.Context(), frame); err != nil {
				t.Fatal("valid array prefix rejected", err)
			}
		}
		return state
	}
	state := consumePrefix(prefix)
	if state.entityCount != 2 || state.openItems != 0 || state.bufferedBytes == 0 {
		t.Fatal("array was duplicated, left open, or discarded before its final verification")
	}
	verified := packageStreamFrame("array.verify", 6, `,"item_id":"t","text":"hello","values":[{"score":9007199254740993.0},{"score":2}]`)
	if err := state.consume(t.Context(), verified); err != nil {
		t.Fatal("equivalent exact numbers changed the array prefix", err)
	}
	if err := state.consume(t.Context(), packageStreamFrame(ResponseFinished, 7, "")); err != nil || state.finish() != nil || state.bufferedBytes != 0 {
		t.Fatal("completed stream retained collection payloads", err)
	}
	for _, fixture := range []struct {
		name   string
		prefix int
		frame  Value
		path   string
	}{
		{"offset", 4, packageStreamFrame("array.delta", 4, `,"item_id":"t","offset":0,"values":[{"score":2}]`), "/offset"},
		{"element-shape", 3, packageStreamFrame("array.item", 3, `,"item_id":"t","offset":0,"value":42`), "/value"},
		{"exact-number", 6, packageStreamFrame("array.verify", 6, `,"item_id":"t","text":"hello","values":[{"score":9007199254740992},{"score":2}]`), "/values"},
		{"finished-prefix", 6, packageStreamFrame("array.verify", 6, `,"item_id":"t","text":"hello","values":[{"score":9007199254740993},{"score":2},{"score":3}]`), "/values"},
		{"missing-collection", 6, packageStreamFrame("parent.verify", 6, `,"item_id":"t","text":"hello"`), "/item_id"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			state := consumePrefix(prefix[:fixture.prefix])
			assertPackageRuntimeIssue(t, state.consume(t.Context(), fixture.frame), UpstreamContractViolation, fixture.path)
		})
	}
	t.Run("null-and-empty-delta", func(t *testing.T) {
		state := consumePrefix(prefix[:3])
		for _, frame := range []Value{
			packageStreamFrame("array.delta", 3, `,"item_id":"t","offset":0,"values":null`),
			packageStreamFrame("array.delta", 4, `,"item_id":"t","offset":0,"values":[]`),
			packageStreamFrame(ItemFinished, 5, `,"item_id":"t"`),
			packageStreamFrame("array.verify", 6, `,"item_id":"t","text":"hello","values":null`),
		} {
			if err := state.consume(t.Context(), frame); err != nil {
				t.Fatal("empty delta erased the observed null representation", err)
			}
		}
	})
	t.Run("cancel", func(t *testing.T) {
		state := consumePrefix(prefix[:5])
		if err := state.consume(t.Context(), packageStreamFrame(OperationCancelled, 5, "")); err != nil || state.bufferedBytes != 0 || state.finish() != nil {
			t.Fatal("cancel retained collection payloads", err)
		}
	})
}
