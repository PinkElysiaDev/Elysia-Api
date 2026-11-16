package protocol

import (
	"fmt"
	"strconv"
)

const (
	metadataScope = "metadata"
	nodeScope     = "node"
)

func isOwnerEffectScope(scope string) bool {
	return scope == metadataScope || scope == nodeScope
}

func validateOwnerEffect(direction Direction, effect ProtocolSemanticEffect) error {
	if isPackageDecodeDirection(direction) || effect.Kind != "reject" || effect.When == nil {
		return fmt.Errorf("owner scope requires an encode rejection with a local condition")
	}
	if effect.Scope == metadataScope && effect.Path != "" || effect.Target != "" || effect.Layout != nil || effect.ValueMapping != nil || !effect.Default.IsZero() || effect.Severity != "" {
		return fmt.Errorf("owner rejection cannot declare target, layout, default or severity; metadata selects its own diagnostic path")
	}
	return nil
}

func (state evaluation) readCoreDocument(direction Direction) (any, error) {
	input := JSONLeaf(state.input)
	switch direction {
	case EncodeRequest:
		return decodeCoreRequestOutputWithin(input, state.valueReader)
	case EncodeResponse:
		return decodeCoreResponseOutputWithin(input, state.valueReader)
	default:
		return decodeCoreEventOutputWithin(input, state.valueReader)
	}
}

// Each rejection observes every owner, including nested history and terminal
// snapshots. A disabled conversion rule cannot make encoding discard the field.
func (compiled *Compiled) checkOwnerEffect(direction Direction, state evaluation, effect compiledSemanticEffect, provenance *provenanceReadMemo) error {
	document := state.coreDocument
	if effect.scope == nodeScope {
		reader := coreReadContext{cache: state.cache, provenance: provenance}
		return visitControlNodes(state, document, func(node *Node, path string) error {
			read := func(pointer []string) (JSONOutput, error) { return reader.readNode(*node, pointer) }
			return compiled.checkSelectedOwner(direction, state, effect, read, path+effect.path)
		})
	}
	return visitControlMetadata(state, document, func(fields *[]ResponseMetadata, path string) error {
		for index, field := range *fields {
			read := func(pointer []string) (JSONOutput, error) {
				return readCoreResponseMetadata(field, pointer, state.cache)
			}
			location := field.Path
			if location == "" {
				location = path + "/" + strconv.Itoa(index)
			}
			if err := compiled.checkSelectedOwner(direction, state, effect, read, location); err != nil {
				return err
			}
		}
		return nil
	})
}

func (compiled *Compiled) checkSelectedOwner(direction Direction, state evaluation, effect compiledSemanticEffect, read func([]string) (JSONOutput, error), path string) error {
	local := evaluation{ctx: state.ctx, inputReader: read, rootReader: read, budget: state.budget, valueReader: state.valueReader}
	condition, err := effect.when.evaluate(local)
	if err != nil {
		return err
	}
	shouldReject, err := readBoolean(condition)
	if err != nil {
		return compiled.packageEffectError(direction, effect, "owner condition must produce a boolean", err)
	}
	if !shouldReject {
		return nil
	}
	failure := compiled.packageEffectError(direction, effect, effect.reason, nil)
	issue := &failure.Issues[0]
	issue.Path = path
	if diagnostic := effect.diagnostic; diagnostic != nil {
		issue.Code, issue.Stage, issue.Suggestion = diagnostic.Code, diagnostic.Stage, diagnostic.Suggestion
	}
	return failure
}
