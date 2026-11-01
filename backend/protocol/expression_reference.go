package protocol

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
)

// References share instructions only when their static inputs, lexical slots
// and permissions agree. Runtime values, captures and state remain call-owned.
type referenceContext struct {
	name                                        string
	input, root, item                           *ValueSchema
	coreInput, coreRoot, coreItem, walkCoreItem reflect.Type
	permissions                                 uint16
	bindingDepth, indexDepth                    int
	streamPrimitiveUsed                         *bool
}

type compiledReference struct {
	expansionDepth int
	expression     *compiledExpression
	dependencies   []string
	bindings       map[string]expressionBinding
	stateSlots     map[string]expressionBinding
	indexes        map[string]int
	streamIndexes  map[string]int
	trackers       map[string]bool
	namespaces     map[string]int
}

func referenceKey(name string, scope expressionScope) referenceContext {
	key := referenceContext{name: name, input: scope.input, root: scope.root, item: scope.item,
		coreInput: scope.coreInput, coreRoot: scope.coreRoot, coreItem: scope.coreItem, walkCoreItem: scope.walkCoreItem,
		bindingDepth: scope.bindingDepth, indexDepth: scope.indexDepth, streamPrimitiveUsed: scope.streamPrimitiveUsed}
	for index, enabled := range [...]bool{scope.canReadNativeFrame, scope.canUseStreamPrimitives, scope.hasInputScope,
		scope.isIteration, scope.isSingleVisit, scope.isWalk, scope.wireCapture, scope.canReadRuntime,
		scope.canReadOutputStream, scope.canReadNative, scope.canWriteStreamIndexes, scope.isWireContract, scope.canReadFrameUsage, scope.canReadOperation} {
		if enabled {
			key.permissions |= 1 << index
		}
	}
	return key
}

func (reference compiledReference) matches(scope expressionScope) bool {
	return maps.Equal(reference.bindings, scope.bindings) && maps.Equal(reference.stateSlots, scope.stateSlots) &&
		maps.Equal(reference.indexes, scope.indexes) && maps.Equal(reference.streamIndexes, scope.streamIndexes) &&
		maps.Equal(reference.trackers, scope.trackerChannels) && maps.Equal(reference.namespaces, scope.outputNamespaces)
}

func captureReferenceScope(scope expressionScope) compiledReference {
	return compiledReference{bindings: scope.bindings, stateSlots: scope.stateSlots, indexes: scope.indexes,
		streamIndexes: scope.streamIndexes, trackers: maps.Clone(scope.trackerChannels), namespaces: scope.outputNamespaces}
}

func (compiler *expressionCompiler) compileReference(name, path string, scope expressionScope, depth int) (*compiledExpression, error) {
	definition, exists := compiler.references[name]
	if !exists || compiler.resolving[name] {
		return nil, fmt.Errorf("%s/ref: unknown or recursive expression %q", path, name)
	}
	compiler.used[name] = true
	key := referenceKey(name, scope)
	for _, reference := range compiler.instances[key] {
		if !reference.matches(scope) {
			continue
		}
		maximumDepth := depth + reference.expansionDepth
		if maximumDepth > compiler.limits.Depth {
			return nil, fmt.Errorf("%s: referenced program exceeds expression depth limit (%d/%d)", path, maximumDepth, compiler.limits.Depth)
		}
		compiler.maximumDepth = max(compiler.maximumDepth, maximumDepth)
		compiler.includeReferences(reference.dependencies)
		return reference.expression, nil
	}
	parent := compiler.mappingReferences
	dependencies := map[string]Expression{name: definition}
	compiler.mappingReferences = dependencies
	compiler.resolving[name] = true
	parentDepth := compiler.maximumDepth
	compiler.maximumDepth = depth
	compiled, err := compiler.compile(definition, referencePath(path, name), scope, depth+1)
	expansionDepth := compiler.maximumDepth - depth
	compiler.maximumDepth = max(parentDepth, compiler.maximumDepth)
	delete(compiler.resolving, name)
	compiler.mappingReferences = parent
	if err != nil {
		return nil, err
	}
	reference := captureReferenceScope(scope)
	reference.expression, reference.expansionDepth, reference.dependencies = compiled, expansionDepth, sortedKeys(dependencies)
	if compiler.instances == nil {
		compiler.instances = make(map[referenceContext][]compiledReference)
	}
	compiler.instances[key] = append(compiler.instances[key], reference)
	compiler.includeReferences(reference.dependencies)
	return compiled, nil
}

func (compiler *expressionCompiler) includeReferences(names []string) {
	if compiler.mappingReferences != nil {
		for _, name := range names {
			compiler.mappingReferences[name] = compiler.references[name]
		}
	}
}

func referencePath(callPath, name string) string {
	base := "/expressions/"
	for _, prefix := range []string{"/spec/", "/mapping/"} {
		if strings.HasPrefix(callPath, prefix) {
			base = prefix + "expressions/"
			break
		}
	}
	return base + escapePointer(name)
}
