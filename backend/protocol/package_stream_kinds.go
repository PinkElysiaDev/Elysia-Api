package protocol

import (
	"fmt"
	"slices"
)

func (compiler *packageStreamCompiler) recordItemAction(event compiledPackageStreamEvent, path string) error {
	if compiler.itemActions == nil || event.action != ItemStarted && event.action != ItemDelta && event.action != ItemSnapshot && event.action != ItemFinished {
		return nil
	}
	// A generic finish closes the established kind; it declares no new kind.
	if event.action == ItemFinished && !event.isTool && event.kind != MessageNode {
		return nil
	}
	kind := event.kind
	if event.isTool {
		kind = ToolCallNode
	}
	if !slices.Contains(compiler.contract.itemKinds, kind) {
		return fmt.Errorf("%s/action: item kind %q is not declared by this stream", path, kind)
	}
	if compiler.itemActions[kind] == nil {
		compiler.itemActions[kind] = make(map[EventType]bool)
	}
	compiler.itemActions[kind][event.action] = true
	return nil
}

func (compiler *packageStreamCompiler) checkLifecycle(path string) error {
	required := []EventType{ResponseStarted, UsageUpdated, ResponseFinished}
	if compiler.contract.itemKinds == nil {
		// Existing contracts promise a text lifecycle independently of tools.
		required = []EventType{ResponseStarted, ItemStarted, ItemDelta, ItemFinished, UsageUpdated, ResponseFinished}
	}
	for _, action := range required {
		if !compiler.actions[action] && !(action == ItemFinished && compiler.contract.closeItemsOnFinish) {
			return fmt.Errorf("%s/events: missing lifecycle action for %s", path, action)
		}
	}
	for _, kind := range compiler.contract.itemKinds {
		for _, action := range []EventType{ItemStarted, ItemDelta, ItemFinished} {
			if kind == MessageNode && action == ItemDelta {
				continue
			}
			if compiler.itemActions[kind][action] || action == ItemFinished && (compiler.contract.closeItemsOnFinish || compiler.actions[ItemFinished]) {
				continue
			}
			return fmt.Errorf("%s/events: missing %s lifecycle action for %s", path, kind, action)
		}
	}
	if compiler.contract.hasTools {
		for _, action := range []EventType{ItemStarted, ItemDelta} {
			if !compiler.toolActions[action] {
				return fmt.Errorf("%s/events: missing tool lifecycle action for %s", path, action)
			}
		}
	}
	return nil
}

func (contract *compiledPackageStreamContract) checkCapabilities(capabilities CapabilitySet, path string) error {
	if contract.itemKinds == nil {
		if !capabilities[TextCapability] || !capabilities[UsageCapability] {
			return fmt.Errorf("%s: stream contract requires text and usage", path)
		}
		if contract.hasTools && !capabilities[FunctionToolsCapability] {
			return fmt.Errorf("%s: stream tool actions require tools.function", path)
		}
		return nil
	}
	if !capabilities[UsageCapability] {
		return fmt.Errorf("%s: stream contract requires usage", path)
	}
	for _, entry := range []struct {
		capability Capability
		kinds      []NodeKind
	}{
		{TextCapability, []NodeKind{TextNode, RefusalNode}},
		{ReasoningCapability, []NodeKind{ReasoningNode}},
		{FunctionToolsCapability, []NodeKind{ToolCallNode}},
		{FreeTextToolsCapability, nil}, {ImagesCapability, nil}, {AudioCapability, nil}, {VideoCapability, nil}, {DocumentsCapability, nil},
	} {
		isDeclared := false
		for _, kind := range entry.kinds {
			isDeclared = isDeclared || slices.Contains(contract.itemKinds, kind)
		}
		if capabilities[entry.capability] != isDeclared {
			return fmt.Errorf("%s: %s must agree with this stream's explicit itemKinds", path, entry.capability)
		}
	}
	return nil
}
