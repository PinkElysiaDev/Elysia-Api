package protocol

import (
	"fmt"
	"slices"
)

func hasStreamEntitySelectors(spec ProtocolStreamActionSpec) bool {
	return spec.Entity != "" || spec.Namespace != "" || spec.Key != nil || spec.Parent != nil || spec.Ordinal != nil || spec.Offset != nil || len(spec.VerifyChildren) != 0 || spec.Payload != nil || spec.Encoding != "" || len(spec.Identities) != 0 || spec.Properties != nil || spec.ChildrenCount != nil || spec.ChildrenNamespace != "" || spec.SealPayload || spec.VerifyCompleted || len(spec.RequireVerified) != 0 || spec.Diagnostic != nil
}

func (compiler *packageStreamCompiler) compilePredicate(value *Expression, path string) (*compiledOutputPlan, error) {
	if value == nil {
		return nil, nil
	}
	field := streamField{}
	if err := compiler.compileValue(value, path, &field); err != nil {
		return nil, err
	}
	if !isAssignable(field.value.expression.result, BooleanType) {
		return nil, fmt.Errorf("%s: condition must produce a boolean", path)
	}
	return field.value, nil
}

func (compiler *packageStreamCompiler) bindIteration(spec ProtocolStreamActionSpec, event *compiledPackageStreamEvent, path string) error {
	scope := compiler.scope
	scope.bindings = make(map[string]expressionBinding, len(compiler.scope.bindings)+2)
	for name, binding := range compiler.scope.bindings {
		scope.bindings[name] = binding
	}
	for _, binding := range []struct {
		name string
		kind JSONType
		slot *int
		set  *bool
	}{{spec.As, AnyType, &event.itemBinding, &event.canBindItem}, {spec.IndexAs, IntegerType, &event.indexBinding, &event.canBindIndex}} {
		if binding.name == "" {
			continue
		}
		if !definitionIdentifier.MatchString(binding.name) || spec.As != "" && spec.As == spec.IndexAs {
			return fmt.Errorf("%s: iteration bindings require distinct identifiers", path)
		}
		*binding.slot, *binding.set = scope.bindingDepth, true
		scope.bindings[binding.name] = expressionBinding{slot: scope.bindingDepth, result: binding.kind}
		scope.bindingDepth++
	}
	scope.isIteration = true
	compiler.scope = scope
	compiler.contract.bindingSlots = max(compiler.contract.bindingSlots, scope.bindingDepth)
	return nil
}

func (compiler *packageStreamCompiler) compileEntities(spec *ProtocolStreamSpec, path string) error {
	if len(spec.Namespaces)+len(spec.Entities) > compiler.limits.Nodes {
		return fmt.Errorf("%s/entities: declaration count exceeds configuration node limit", path)
	}
	for _, name := range sortedKeys(spec.Namespaces) {
		declaration := spec.Namespaces[name]
		if !definitionIdentifier.MatchString(name) || declaration.KeyType != StringType && declaration.KeyType != IntegerType {
			return fmt.Errorf("%s/namespaces/%s: requires an identifier and string or integer keys", path, name)
		}
		compiler.contract.namespaceSlots[name] = len(compiler.contract.namespaces)
		compiler.contract.namespaces = append(compiler.contract.namespaces, compiledWireNamespace{name: name, keyType: declaration.KeyType})
	}
	for _, name := range sortedKeys(spec.Entities) {
		declaration := spec.Entities[name]
		at := path + "/entities/" + escapePointer(name)
		namespace, ok := compiler.contract.namespaceSlots[declaration.Namespace]
		if !definitionIdentifier.MatchString(name) || !ok {
			return fmt.Errorf("%s: requires a name and declared namespace", at)
		}
		if declaration.Payload != "none" && declaration.Payload != "text" && declaration.Payload != "json" {
			return fmt.Errorf("%s/payload: requires none, text or json", at)
		}
		if declaration.PayloadSchema != nil && declaration.Payload != "json" {
			return fmt.Errorf("%s/payloadSchema: only json entities have a completion schema", at)
		}
		entity := compiledWireEntity{name: name, namespace: namespace, payload: declaration.Payload, parentNamespace: -1, closeWithParent: declaration.CloseWithParent, isArray: declaration.PayloadSchema != nil && declaration.PayloadSchema.Type == ArrayType}
		var err error
		entity.schema, err = compileWireSchemaPlan(declaration.PayloadSchema, compiler.limits)
		if err != nil {
			return locateMappingError(at+"/payloadSchema", err)
		}
		if entity.isArray {
			entity.arrayItems, err = compileWireSchemaPlan(declaration.PayloadSchema.Items, compiler.limits)
			if err != nil {
				return locateMappingError(at+"/payloadSchema/items", err)
			}
		}
		if declaration.ParentNamespace != "" {
			entity.parentNamespace, ok = compiler.contract.namespaceSlots[declaration.ParentNamespace]
			if !ok || entity.parentNamespace == namespace {
				return fmt.Errorf("%s/parentNamespace: requires a distinct declared namespace", at)
			}
		}
		if declaration.CloseWithParent && entity.parentNamespace < 0 {
			return fmt.Errorf("%s/closeWithParent: requires a parent namespace", at)
		}
		for _, identityName := range sortedKeys(declaration.Identities) {
			identity := declaration.Identities[identityName]
			if !definitionIdentifier.MatchString(identityName) || identity.Type != StringType && identity.Type != IntegerType || identity.RequiredAt != "start" && identity.RequiredAt != "finish" || identity.Comparison != "" && identity.Comparison != "typed" && identity.Comparison != "raw_json" || identity.AllowEmpty && identity.Type != StringType {
				return fmt.Errorf("%s/identities/%s: invalid identity contract", at, identityName)
			}
			entity.identities = append(entity.identities, compiledWireEntityIdentity{name: identityName, kind: identity.Type, requiredAtStart: identity.RequiredAt == "start", isRaw: identity.Comparison == "raw_json", isUnique: identity.Unique, allowsEmpty: identity.AllowEmpty})
		}
		compiler.contract.entitySlots[name] = len(compiler.contract.entities)
		compiler.contract.entities = append(compiler.contract.entities, entity)
	}
	for _, name := range sortedKeys(spec.Entities) {
		declaration := spec.Entities[name]
		entity := &compiler.contract.entities[compiler.contract.entitySlots[name]]
		for _, parentName := range declaration.ParentEntities {
			slot, exists := compiler.contract.entitySlots[parentName]
			if !exists || entity.parentNamespace < 0 || compiler.contract.entities[slot].namespace != entity.parentNamespace || slices.Contains(entity.parentEntities, slot) {
				return fmt.Errorf("%s/entities/%s/parentEntities: requires distinct entities in its parent namespace", path, name)
			}
			entity.parentEntities = append(entity.parentEntities, slot)
		}
	}
	for parentIndex := range compiler.contract.entities {
		parent := &compiler.contract.entities[parentIndex]
		for _, child := range compiler.contract.entities {
			if child.parentNamespace == parent.namespace && (len(child.parentEntities) == 0 || slices.Contains(child.parentEntities, parentIndex)) && !slices.Contains(parent.childNamespaces, child.namespace) {
				parent.childNamespaces = append(parent.childNamespaces, child.namespace)
			}
		}
	}
	return nil
}

func (compiler *packageStreamCompiler) compileEntitySelectors(spec ProtocolStreamActionSpec, event *compiledPackageStreamEvent, path string) error {
	isEntity := event.action >= wireEntityStart && event.action <= wireEntityFinish
	if isEntity {
		if spec.Key == nil || (spec.Entity == "") == (spec.Namespace == "") {
			return fmt.Errorf("%s: entity actions require key and exactly one entity or namespace", path)
		}
		if spec.Namespace != "" && event.action != wireEntityFinish {
			return fmt.Errorf("%s/namespace: namespace-only lookup requires entity_finish", path)
		}
		var ok bool
		if spec.Entity != "" {
			event.entity, ok = compiler.contract.entitySlots[spec.Entity]
			if ok {
				event.namespace = compiler.contract.entities[event.entity].namespace
			}
		} else {
			event.namespace, ok = compiler.contract.namespaceSlots[spec.Namespace]
		}
		if !ok {
			return fmt.Errorf("%s: unknown entity or namespace", path)
		}
	} else if spec.Entity != "" || spec.Namespace != "" || spec.Key != nil || spec.Parent != nil || spec.Ordinal != nil || spec.Offset != nil || len(spec.VerifyChildren) != 0 || spec.Payload != nil || spec.Encoding != "" || len(spec.Identities) != 0 || spec.Properties != nil || spec.ChildrenCount != nil || spec.SealPayload || spec.VerifyCompleted {
		return fmt.Errorf("%s: entity selectors require an entity action", path)
	}
	if isEntity && event.entity < 0 && (spec.Parent != nil || spec.Ordinal != nil || spec.Offset != nil || len(spec.VerifyChildren) != 0 || spec.Payload != nil || spec.Properties != nil || spec.ChildrenCount != nil || len(spec.Identities) != 0 || spec.VerifyCompleted) {
		return fmt.Errorf("%s: namespace finish cannot redefine entity payloads", path)
	}
	if spec.SealPayload && event.action != wireEntitySnapshot || spec.VerifyCompleted && event.action != wireEntityFinish {
		return fmt.Errorf("%s: sealPayload requires snapshot and verifyCompleted requires finish", path)
	}
	if spec.Ordinal != nil && spec.Parent == nil {
		return fmt.Errorf("%s/ordinal: requires parent", path)
	}
	if event.entity >= 0 {
		entity := compiler.contract.entities[event.entity]
		if (spec.Parent != nil) != (entity.parentNamespace >= 0) {
			return fmt.Errorf("%s/parent: must agree with entity parent namespace", path)
		}
		if spec.Payload != nil && entity.payload == "none" || (event.action == wireEntityAppend || event.action == wireEntitySnapshot && entity.payload != "none") && spec.Payload == nil {
			return fmt.Errorf("%s/payload: missing or incompatible entity payload", path)
		}
		if entity.isArray && spec.Payload != nil {
			if spec.Encoding != "value" && !(spec.Encoding == "item" && event.action == wireEntityAppend) {
				return fmt.Errorf("%s/encoding: array payloads require value; append also permits item", path)
			}
			event.isJSONValue = true
			event.isArrayItem = spec.Encoding == "item"
		} else if entity.payload == "json" && spec.Payload != nil && event.action != wireEntityAppend {
			if spec.Encoding != "json_text" && spec.Encoding != "value" {
				return fmt.Errorf("%s/encoding: json snapshots require json_text or value", path)
			}
			event.isJSONValue = spec.Encoding == "value"
		} else if spec.Encoding != "" {
			return fmt.Errorf("%s/encoding: encoding belongs to json payloads", path)
		}
		if spec.Offset != nil && (!entity.isArray || event.action != wireEntityAppend) {
			return fmt.Errorf("%s/offset: requires an array append", path)
		}
		if len(spec.VerifyChildren) > 0 && event.action != wireEntityVerify && event.action != wireEntityFinish {
			return fmt.Errorf("%s/verifyChildren: requires entity verification or completion", path)
		}
		for _, name := range spec.VerifyChildren {
			slot, exists := compiler.contract.namespaceSlots[name]
			if !exists || !slices.Contains(entity.childNamespaces, slot) || slices.Contains(event.verifiedChildren, slot) {
				return fmt.Errorf("%s/verifyChildren: requires distinct child namespaces", path)
			}
			event.verifiedChildren = append(event.verifiedChildren, slot)
		}

		for _, name := range sortedKeys(spec.Identities) {
			slot := -1
			for index, identity := range entity.identities {
				if identity.name == name {
					slot = index
					break
				}
			}
			if slot < 0 {
				return fmt.Errorf("%s/identities/%s: undeclared identity", path, name)
			}
			selected := spec.Identities[name]
			field, err := compiler.compileSelection(&selected, path+"/identities/"+escapePointer(name))
			if err != nil {
				return err
			}
			event.identities = append(event.identities, compiledEntityIdentitySelection{slot: slot, field: field})
		}
		if spec.ChildrenCount != nil {
			children := map[int]bool{}
			for _, child := range compiler.contract.entities {
				if child.parentNamespace == entity.namespace {
					children[child.namespace] = true
				}
			}
			if spec.ChildrenNamespace != "" {
				var ok bool
				event.childrenNamespace, ok = compiler.contract.namespaceSlots[spec.ChildrenNamespace]
				if !ok || !children[event.childrenNamespace] {
					return fmt.Errorf("%s/childrenNamespace: requires a child namespace", path)
				}
			} else {
				if len(children) != 1 {
					return fmt.Errorf("%s/childrenNamespace: required for multiple child namespaces", path)
				}
				for slot := range children {
					event.childrenNamespace = slot
				}
			}
		} else if spec.ChildrenNamespace != "" {
			return fmt.Errorf("%s/childrenNamespace: requires childrenCount", path)
		}
	}
	for _, selection := range []struct {
		name  string
		spec  *ProtocolStreamValueSpec
		field *streamField
	}{
		{"key", spec.Key, &event.key}, {"parent", spec.Parent, &event.parent}, {"ordinal", spec.Ordinal, &event.ordinal}, {"offset", spec.Offset, &event.offset}, {"payload", spec.Payload, &event.delta}, {"properties", spec.Properties, &event.properties}, {"childrenCount", spec.ChildrenCount, &event.childrenCount}} {
		field, err := compiler.compileSelection(selection.spec, path+"/"+selection.name)
		if err != nil {
			return err
		}
		*selection.field = field
	}
	if len(spec.RequireVerified) > 0 && event.action != wireFinish {
		return fmt.Errorf("%s/requireVerified: requires stream_finish", path)
	}
	seen := map[int]bool{}
	for _, name := range spec.RequireVerified {
		slot, ok := compiler.contract.entitySlots[name]
		if !ok || seen[slot] {
			return fmt.Errorf("%s/requireVerified: requires distinct declared entities", path)
		}
		event.requiredEntities = append(event.requiredEntities, slot)
		seen[slot] = true
	}
	if spec.Diagnostic != nil {
		if err := validateBoundaryDiagnostic(*spec.Diagnostic, path+"/diagnostic"); err != nil {
			return err
		}
		copy := *spec.Diagnostic
		event.diagnostic = &copy
	}
	event.sealsPayload, event.verifiesCompleted = spec.SealPayload, spec.VerifyCompleted
	return nil
}
