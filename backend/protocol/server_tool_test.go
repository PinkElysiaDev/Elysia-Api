package protocol

import (
	"strings"
	"testing"
)

func serverReplayTarget() Target {
	target := replayTarget()
	target.coreVersion = ProtocolCoreVersion
	target.Scope = Scope{Provider: "fixture", Account: "account", Model: "model"}
	return target
}

func TestServerExecutionContractAndCapabilityEvidence(t *testing.T) {
	target := serverReplayTarget()
	node := Node{Kind: ServerToolNode, ID: StringValue("execution"), Name: StringValue("search"), Status: StringValue("completed"),
		Input:     &ToolInput{Kind: JSONInput, Value: parseTestValue(t, `{"query":"question"}`)},
		Resources: []Resource{{Kind: "container", ID: StringValue("container"), Scope: target.Scope}},
		Children: []Node{
			{Kind: ImageNode, Payload: parseTestValue(t, `{"data":"AQID","mime":"image/png"}`)},
			{Kind: DocumentNode, Resources: []Resource{{Kind: "file_id", ID: StringValue("file"), Scope: target.Scope}}},
		}}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) != 0 {
		t.Fatal(issues)
	}
	if issues := CheckResponse(response, target, DefaultLimits()); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, semantic := range []any{request, response, []Event{{Item: &node}}} {
		observed := observeCapabilities(semantic).observed
		if !observed[ServerToolsCapability] || !observed[ImagesCapability] || !observed[DocumentsCapability] || observed[FunctionToolsCapability] {
			t.Fatal("execution evidence confused artifacts or client tools", observed)
		}
	}
	result := Node{Kind: ToolResultNode, CallID: node.ID, Payload: StringValue("not a pending call")}
	if err := IssuesError(CheckToolAssociations([]Node{node, result}, DefaultLimits())); err == nil {
		t.Fatal("upstream execution authorized a client result")
	}
	response.Content[0].Children = []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: ToolCallNode, CallID: StringValue("call"), Name: StringValue("local"), Input: &ToolInput{Kind: JSONInput, Value: parseTestValue(t, `{}`)}}}}}
	if err := IssuesError(CheckResponse(response, target, DefaultLimits())); err == nil {
		t.Fatal("server artifact smuggled a local tool call")
	}
	response.Content = []Node{node}
	legacy := target
	legacy.coreVersion, response.SchemaVersion = SemanticSchemaVersion, SemanticSchemaVersion
	if err := IssuesError(CheckResponse(response, legacy, DefaultLimits())); err == nil {
		t.Fatal("legacy core silently accepted upstream execution")
	}
	response.SchemaVersion = ProtocolCoreVersion
	for _, kind := range []string{"container", "audio"} {
		response.Content = []Node{{Kind: AudioNode, Resources: []Resource{{Kind: kind, ID: StringValue("resource"), Scope: target.Scope}}}}
		if err := IssuesError(CheckResponse(response, target, DefaultLimits())); err != nil {
			t.Fatal("known scoped resource rejected", kind, err)
		}
		response.Content[0].Resources[0].Scope.Account = "other"
		if issues := CheckResponse(response, target, DefaultLimits()); len(issues) == 0 || issues[0].Code != ResourceScopeMismatch {
			t.Fatal("resource crossed its account", kind, issues)
		}
		response.Content[0].Resources[0].Scope.Account = ""
		if err := IssuesError(CheckResponse(response, target, DefaultLimits())); err == nil {
			t.Fatal("resource accepted missing source provenance", kind)
		}
	}
	header := Node{Kind: ServerToolNode, Name: StringValue("code"), Status: StringValue("interpreting")}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: StringValue("execution"), Item: &header}
	if issues := CheckEvent(event, target, DefaultLimits()); len(issues) != 0 {
		t.Fatal("server progress required an invented text delta", issues)
	}
	for _, mutate := range []func(*Event){
		func(event *Event) { event.Delta = StringValue("") },
		func(event *Event) { event.Item.Status = StringValue("") },
		func(event *Event) { event.Item.Payload = StringValue("unmapped result") },
		func(event *Event) { event.Item.Input = &ToolInput{Kind: TextInput} },
	} {
		invalid, item := event, header
		invalid.Item = &item
		mutate(&invalid)
		if issues := CheckEvent(invalid, target, DefaultLimits()); len(issues) == 0 {
			t.Fatal("progress accepted an empty status or mixed payload", invalid)
		}
	}
}

func TestCollectorServerSnapshotsOwnArtifactsAndInput(t *testing.T) {
	target := serverReplayTarget()
	collector, err := NewResponseCollector(target, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	node := Node{Kind: ServerToolNode, ID: StringValue("execution"), Name: StringValue("code"), Input: &ToolInput{Kind: TextInput}, Status: StringValue("in_progress")}
	largeSource := Value{raw: `{"id":"execution","name":"code","padding":"` + strings.Repeat("x", 64<<10) + `"}`}
	for _, field := range []*Value{&node.ID, &node.Name} {
		start := strings.Index(largeSource.raw, field.raw)
		field.raw = largeSource.raw[start : start+field.Len()]
	}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemStarted, ItemID: node.ID, Item: &node}
	consume := func(event Event) {
		t.Helper()
		_, text, err := collector.Consume(event)
		if err != nil || text != "" {
			t.Fatal("server observation became assistant output or failed", text, err)
		}
	}
	consume(event)
	key, err := collector.replay.itemKey(event)
	if err != nil {
		t.Fatal(err)
	}
	identity := collector.replay.items[key]
	if sharesValueStorage(largeSource, identity.wireID) || sharesValueStorage(largeSource, Value{raw: identity.name}) {
		t.Fatal("small replay identity retained its original large frame")
	}
	for _, text := range []string{"print(", "1)"} {
		consume(Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: node.ID, Delta: StringValue(text)})
	}
	if len(collector.replay.state.tools) != 0 || len(collector.replay.state.callIDs) != 0 || collector.item(key).Input.Value.IsString("print(1)") {
		t.Fatal("server input registered a client call or materialized the whole prefix")
	}
	buffer := collector.texts[key]
	header := Node{Kind: ServerToolNode, ID: node.ID, Name: node.Name, Status: StringValue("interpreting"), Source: &Provenance{Path: "/progress"}}
	progress := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: node.ID, Item: &header}
	consume(progress)
	header.Source.Path = "/changed"
	if collector.texts[key] != buffer || buffer.String() != "print(1)" || !collector.item(key).Input.Value.IsZero() ||
		!collector.item(key).Status.IsString("interpreting") || collector.item(key).Source.Path != "/progress" || collector.replay.items[key].isFinished {
		t.Fatal("progress copied input, lost its status, aliased source or finished the item", collector.item(key))
	}
	header.ID = StringValue("different execution")
	if _, _, err := collector.Consume(progress); err == nil {
		t.Fatal("progress changed an established execution identity")
	}
	header.ID = node.ID
	node.Input.Value, node.Status = StringValue("print(1)"), StringValue("completed")
	node.Payload = parseTestValue(t, `{"exit":0}`)
	node.Children = []Node{{Kind: TextNode, Payload: StringValue("tool log")}, {Kind: ImageNode, Payload: parseTestValue(t, `{"data":"AQID"}`)}}
	node.Resources = []Resource{{Kind: "container", ID: StringValue("container"), Scope: target.Scope}}
	node.Attributes = Object{"outputs_shape": StringValue("array")}
	node.Native = &Native{Value: parseTestValue(t, `{"vendor":1}`)}
	event.Type = ItemFinished
	consume(event)
	if len(collector.texts) != 0 {
		t.Fatal("completed server input retained its accumulation buffer")
	}
	node.Input.Value = StringValue("mutated")
	node.Children[0].Payload = StringValue("mutated")
	node.Resources[0].ID = StringValue("mutated")
	node.Native.Value = StringValue("mutated")
	node.Attributes["outputs_shape"] = StringValue("mutated")
	completed, _, ok := collector.CompletedNode(event)
	if !ok || !completed.Input.Value.IsString("print(1)") || !completed.Children[0].Payload.IsString("tool log") || !completed.Resources[0].ID.IsString("container") || !completed.Native.Value.IsObject() || !completed.Attributes["outputs_shape"].IsString("array") {
		t.Fatal("server snapshot retained mutable caller containers", completed)
	}
	completed.Native.Value = StringValue("changed completion copy")
	consume(Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished})
	response, err := collector.Finish()
	if err != nil || !response.Content[0].Native.Value.IsObject() || len(response.Content[0].Children) != 2 {
		t.Fatal("completion copy changed retained artifacts", response, err)
	}
}

func TestServerSnapshotReplacementAndTerminalBounds(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 2048
	target := serverReplayTarget()
	collector, err := NewResponseCollector(target, limits)
	if err != nil {
		t.Fatal(err)
	}
	node := Node{Kind: ServerToolNode, Name: StringValue("search"), Payload: StringValue(strings.Repeat("a", 900))}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemStarted, ItemID: StringValue("execution"), Item: &node}
	if _, _, err := collector.Consume(event); err != nil {
		t.Fatal(err)
	}
	key, err := collector.replay.itemKey(event)
	if err != nil {
		t.Fatal(err)
	}
	retained := collector.bytes
	event.Type = ItemSnapshot
	for range 8 {
		node.Payload = StringValue(strings.Repeat("b", 900))
		if _, _, err := collector.Consume(event); err != nil || collector.bytes != retained {
			t.Fatal("snapshots retained replaced results", collector.bytes, err)
		}
	}
	node.Payload = Value{}
	node.Children = nil
	if _, _, err := collector.Consume(event); err != nil || collector.bytes >= retained || !collector.item(key).Payload.IsZero() {
		t.Fatal("complete snapshot did not clear its previous result", collector.bytes, err)
	}
	header := Node{Kind: ServerToolNode, Name: node.Name, Status: StringValue("completed")}
	if _, _, err := collector.Consume(Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: event.ItemID, Item: &header}); err != nil || len(collector.texts) != 0 {
		t.Fatal("header-only progress created an input buffer", err)
	}
	if _, _, err := collector.Consume(Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished}); err == nil {
		t.Fatal("response terminal completed an unfinished server execution")
	}
	node.Payload = StringValue(strings.Repeat("x", limits.BufferBytes))
	if _, _, err := collector.Consume(event); err == nil || !collector.item(key).Payload.IsZero() {
		t.Fatal("oversized server snapshot changed the retained result", err)
	}
}
